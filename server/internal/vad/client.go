// Package vad streams 16 kHz int16 PCM to the Silero VAD sidecar over a
// WebSocket and reports speech_start / speech_end events. One Client per
// utterance: Open at listen.start, Feed each decoded opus frame, watch the
// Events channel for speech_end, Close at listen.stop.
//
// Feed is non-blocking: it pushes onto a bounded internal queue that a
// dedicated writer goroutine drains. If the sidecar can't keep up the queue
// fills and frames are dropped with a warn log — the turn loop never stalls.
//
// The sidecar lives at backend/vad-shim. Default URL: ws://127.0.0.1:8005/vad.
package vad

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Event is what the sidecar emits per speech transition.
type Event struct {
	Type string `json:"event"` // "speech_start" or "speech_end"
	MS   int    `json:"ms"`
}

// Options tune the sidecar. Zero values mean "use the sidecar's defaults".
type Options struct {
	Threshold    float64
	MinSilenceMS int
	SpeechPadMS  int
	MinSpeechMS  int
	DialTimeout  time.Duration
	// FeedQueueDepth bounds the internal frame queue. ~30 frames = 1.8 s of
	// 60-ms audio in flight is more than the sidecar should ever lag behind.
	FeedQueueDepth int
}

// Client is one WebSocket connection to the VAD sidecar, scoped to a single
// utterance.
type Client struct {
	conn   *websocket.Conn
	events chan Event
	feeds  chan []int16
	log    *slog.Logger

	cancelFn  context.CancelFunc
	readDone  chan struct{}
	writeDone chan struct{}

	closeOnce sync.Once
	dropMu    sync.Mutex
	drops     int
}

// Open connects to the sidecar. Events streams transitions until the client
// closes; the Feed/queue path runs on a dedicated goroutine.
func Open(ctx context.Context, base string, opts Options, log *slog.Logger) (*Client, error) {
	u, err := buildURL(base, opts)
	if err != nil {
		return nil, fmt.Errorf("vad url: %w", err)
	}

	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 2 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, u, nil)
	if err != nil {
		return nil, fmt.Errorf("vad dial: %w", err)
	}
	conn.SetReadLimit(1 << 16)

	depth := opts.FeedQueueDepth
	if depth <= 0 {
		depth = 32
	}

	loopCtx, loopCancel := context.WithCancel(context.Background())
	c := &Client{
		conn:      conn,
		events:    make(chan Event, 8),
		feeds:     make(chan []int16, depth),
		log:       log,
		cancelFn:  loopCancel,
		readDone:  make(chan struct{}),
		writeDone: make(chan struct{}),
	}
	go c.readLoop(loopCtx)
	go c.writeLoop(loopCtx)
	return c, nil
}

func buildURL(base string, opts Options) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if opts.Threshold > 0 {
		q.Set("threshold", strconv.FormatFloat(opts.Threshold, 'f', 2, 64))
	}
	if opts.MinSilenceMS > 0 {
		q.Set("min_silence_ms", strconv.Itoa(opts.MinSilenceMS))
	}
	if opts.SpeechPadMS > 0 {
		q.Set("speech_pad_ms", strconv.Itoa(opts.SpeechPadMS))
	}
	if opts.MinSpeechMS > 0 {
		q.Set("min_speech_ms", strconv.Itoa(opts.MinSpeechMS))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Feed enqueues a chunk of 16 kHz int16 PCM. Non-blocking. Returns an error
// if the internal queue is full (sidecar lagging), in which case the frame is
// dropped — callers should log but keep going.
func (c *Client) Feed(pcm []int16) error {
	if len(pcm) == 0 {
		return nil
	}
	cp := make([]int16, len(pcm))
	copy(cp, pcm)
	select {
	case c.feeds <- cp:
		return nil
	default:
		c.dropMu.Lock()
		c.drops++
		c.dropMu.Unlock()
		return errors.New("vad: feed queue full")
	}
}

// Events returns a channel of speech transitions that closes when the read
// loop exits.
func (c *Client) Events() <-chan Event { return c.events }

// Drops returns the number of feed frames dropped due to a full queue. Useful
// for logging at end-of-turn.
func (c *Client) Drops() int {
	c.dropMu.Lock()
	defer c.dropMu.Unlock()
	return c.drops
}

// Close shuts down the connection and waits for both loops to exit.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		// Signal writer to drain by closing the feed queue. Reader exits when
		// the WS conn closes.
		close(c.feeds)
		_ = c.conn.Close(websocket.StatusNormalClosure, "done")
		c.cancelFn()
		<-c.readDone
		<-c.writeDone
	})
	return nil
}

func (c *Client) readLoop(ctx context.Context) {
	defer close(c.events)
	defer close(c.readDone)
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			if c.log != nil {
				c.log.Warn("vad: bad event", "err", err, "raw", string(data))
			}
			continue
		}
		select {
		case c.events <- ev:
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	defer close(c.writeDone)
	for {
		select {
		case <-ctx.Done():
			return
		case pcm, ok := <-c.feeds:
			if !ok {
				return
			}
			buf := make([]byte, len(pcm)*2)
			for i, s := range pcm {
				binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
			}
			writeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := c.conn.Write(writeCtx, websocket.MessageBinary, buf)
			cancel()
			if err != nil {
				if c.log != nil && ctx.Err() == nil {
					c.log.Warn("vad: write failed", "err", err)
				}
				return
			}
		}
	}
}
