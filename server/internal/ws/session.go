package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/rebelthor/warble/server/internal/audio"
	"github.com/rebelthor/warble/server/internal/config"
	"github.com/rebelthor/warble/server/internal/llm"
	"github.com/rebelthor/warble/server/internal/pipeline"
	"github.com/rebelthor/warble/server/internal/proto"
	"github.com/rebelthor/warble/server/internal/vad"
)

// audioEvent multiplexes upstream opus frames with listen-state
// transitions on a single channel so the turn goroutine sees them in
// order.
type audioEvent struct {
	kind  audioEventKind
	frame []byte
	text  string // used by aeInjectText
}

type audioEventKind int

const (
	aeFrame       audioEventKind = iota // payload in frame
	aeReset                             // listen.start: clear pcm buffer
	aeStop                              // listen.stop: dispatch the turn
	aeSpeechStart                       // VAD speech_start: trim pre-speech silence from pcm
	aeInjectText                        // debug /say: dispatch a turn with text in place of ASR
)

// speechStartKeepMS is how many milliseconds of audio to retain BEFORE the VAD
// detection point when speech_start fires. The VAD has detection latency
// (typically 100-200 ms with Silero), so a small lookback preserves the actual
// word onset that the user uttered just before the VAD called it.
const speechStartKeepMS = 300

// Session is the per-connection state. One device → one Session.
type Session struct {
	id         string
	deviceID   string
	clientID   string
	cfg        config.Config
	conn       *websocket.Conn
	log        *slog.Logger
	pipeline   *pipeline.Pipeline
	listenMode string

	// audioEvents carries upstream opus frames and listen.start/stop
	// signals to the turn goroutine. Lets us serialize state changes
	// (reset on start, dispatch on stop) with frame arrival.
	audioEvents chan audioEvent

	// writeMu serializes all conn.Write calls. coder/websocket forbids
	// concurrent writes; handleAbort (in the read goroutine) and
	// streamOutput (in the turn goroutine) can race without this.
	writeMu sync.Mutex

	mu         sync.Mutex
	turnCtx    context.Context
	turnCancel context.CancelFunc

	helloDone chan struct{}

	// Last upstream audio frame timestamp. Used by the idle-disconnect
	// watchdog to detect silent sessions.
	lastAudioMu sync.Mutex
	lastAudio   time.Time

	// Per-session conversation history. Reset to nil on listen.start
	// only between distinct utterances within the same connection; new
	// WS connections always start with an empty dialogue, which matches
	// xinnan-tech behavior and stops the LLM prefix from growing past
	// what's needed for context.
	dialogueMu sync.Mutex
	dialogue   []llm.Message

	// runCtx is the read-loop's context — cancelled when the WS closes
	// for any reason. dispatchTextTurn derives from this rather than
	// from turnCtx so that the device's normal listen:start (which
	// resetTurn()s turnCtx) cannot cancel a debug-injected turn that's
	// in flight. Set at the top of run().
	runCtx context.Context
}

const maxDialogueTurns = 8 // user+assistant pairs to retain per session

func newSession(conn *websocket.Conn, deviceID, clientID string, cfg config.Config, pipe *pipeline.Pipeline, log *slog.Logger) *Session {
	s := &Session{
		id:          uuid.NewString(),
		deviceID:    deviceID,
		clientID:    clientID,
		cfg:         cfg,
		conn:        conn,
		log:         log,
		pipeline:    pipe,
		listenMode:  "auto",
		audioEvents: make(chan audioEvent, 512),
		helloDone:   make(chan struct{}),
	}
	s.resetTurn()
	return s
}

func (s *Session) resetTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnCancel != nil {
		s.turnCancel()
	}
	s.turnCtx, s.turnCancel = context.WithCancel(context.Background())
}

func (s *Session) currentTurn() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnCtx
}

// IdleTimeout is the no-traffic duration after which the server tears
// down a session. Matches the firmware's own 120s timeout
// (xiaozhi-esp32 protocols/protocol.cc IsTimeout).
const IdleTimeout = 120 * time.Second

// run is the WS read loop. Returns when the connection closes.
func (s *Session) run(ctx context.Context) error {
	s.runCtx = ctx
	defer close(s.audioEvents)
	s.noteAudioActivity() // count connection-open as activity

	helloDeadline := time.AfterFunc(10*time.Second, func() {
		select {
		case <-s.helloDone:
			return
		default:
			s.log.Warn("hello.timeout", "session_id", s.id)
			_ = s.conn.Close(websocket.StatusPolicyViolation, "no client hello")
		}
	})
	defer helloDeadline.Stop()

	idleCtx, idleCancel := context.WithCancel(ctx)
	defer idleCancel()
	go s.idleWatchdog(idleCtx)

	go s.turnLoop(ctx)

	for {
		typ, payload, err := s.conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				s.log.Info("ws.close", "session_id", s.id, "code", ce.Code, "reason", ce.Reason)
				return nil
			}
			// Device drops TCP without sending a WS Close frame after tts.stop;
			// surface as a normal close, not an error. Same for our side closing.
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				s.log.Info("ws.close", "session_id", s.id, "reason", "peer dropped")
				return nil
			}
			return fmt.Errorf("ws read: %w", err)
		}

		s.noteAudioActivity()

		switch typ {
		case websocket.MessageText:
			if err := s.handleText(ctx, payload); err != nil {
				s.log.Error("text.handle", "session_id", s.id, "err", err)
			}
		case websocket.MessageBinary:
			s.pushAudioEvent(audioEvent{kind: aeFrame, frame: payload})
		}
	}
}

// idleWatchdog closes the session if it sees no incoming traffic for
// IdleTimeout. Matches the firmware's own 120s read-timeout so server
// and client agree on session lifetime.
func (s *Session) idleWatchdog(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.idleSince() > IdleTimeout {
				s.log.Info("idle.disconnect", "session_id", s.id, "idle", s.idleSince().String())
				_ = s.conn.Close(websocket.StatusNormalClosure, "idle")
				return
			}
		}
	}
}

func (s *Session) handleText(ctx context.Context, raw []byte) error {
	var env proto.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("envelope: %w", err)
	}

	switch env.Type {
	case "hello":
		return s.handleHello(ctx, raw)
	case "listen":
		return s.handleListen(ctx, raw)
	case "abort":
		return s.handleAbort(ctx, raw)
	case "mcp":
		s.log.Debug("mcp.dropped", "session_id", s.id)
		return nil
	default:
		s.log.Warn("text.unknown_type", "session_id", s.id, "type", env.Type)
		return nil
	}
}

func (s *Session) handleHello(ctx context.Context, raw []byte) error {
	var msg proto.ClientHello
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("client hello: %w", err)
	}
	s.log.Debug("hello.client",
		"session_id", s.id,
		"version", msg.Version,
		"format", msg.AudioParams.Format,
		"rate", msg.AudioParams.SampleRate,
		"frame_ms", msg.AudioParams.FrameDuration,
		"features", msg.Features,
	)

	reply := proto.ServerHello{
		Type:      "hello",
		Transport: "websocket",
		SessionID: s.id,
		AudioParams: proto.ServerAudio{
			SampleRate:    s.cfg.AudioSampleHz,
			FrameDuration: s.cfg.AudioFrameMS,
		},
	}
	if err := s.sendJSON(ctx, reply); err != nil {
		return err
	}
	close(s.helloDone)
	return nil
}

func (s *Session) handleListen(ctx context.Context, raw []byte) error {
	var msg proto.Listen
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if msg.Mode != "" {
		s.listenMode = msg.Mode
	}
	s.log.Debug("listen", "session_id", s.id, "state", msg.State, "mode", s.listenMode, "text", msg.Text)

	switch msg.State {
	case "start":
		s.resetTurn()
		s.pushAudioEvent(audioEvent{kind: aeReset})
	case "stop":
		s.pushAudioEvent(audioEvent{kind: aeStop})
	case "detect":
		// Wake-word event with text. Synthesize as if it were a real utterance.
		go s.handleWakeText(msg.Text)
	}
	return nil
}

// pushAudioEvent enqueues a turn-loop event. If the channel is full
// (e.g. the turn goroutine is stuck), control events (reset/stop) take
// precedence over frames: we drain one frame to make room.
func (s *Session) pushAudioEvent(ev audioEvent) {
	select {
	case s.audioEvents <- ev:
		return
	default:
	}
	if ev.kind != aeFrame {
		// Force-deliver control events: drain one slot then enqueue.
		select {
		case <-s.audioEvents:
		default:
		}
		select {
		case s.audioEvents <- ev:
		default:
			s.log.Warn("audio.event_drop", "session_id", s.id, "kind", ev.kind)
		}
		return
	}
	s.log.Warn("audio.drop", "session_id", s.id, "reason", "backpressure")
}

func (s *Session) handleAbort(ctx context.Context, raw []byte) error {
	var msg proto.Abort
	_ = json.Unmarshal(raw, &msg)
	s.log.Info("abort", "session_id", s.id, "reason", msg.Reason)
	s.resetTurn()
	return s.sendJSON(ctx, proto.TTS{
		Type:      "tts",
		SessionID: s.id,
		State:     "stop",
	})
}

// turnLoop accumulates opus frames per utterance, decodes on listen.stop or
// on a server-side VAD speech_end, and dispatches the pipeline. One concurrent
// turn at a time per session.
//
// Each utterance opens a fresh VAD sidecar connection (Silero, see internal/vad).
// If the sidecar is unreachable, we log and proceed without auto-stop — the
// device-side path still works in manual/PTT mode.
func (s *Session) turnLoop(ctx context.Context) {
	decoder, err := audio.NewDecoder(audio.UpstreamSampleHz)
	if err != nil {
		s.log.Error("opus.decoder", "err", err)
		return
	}
	var pcm []int16
	var vadClient *vad.Client

	closeVAD := func() {
		if vadClient != nil {
			drops := vadClient.Drops()
			if drops > 0 {
				s.log.Warn("vad.drops", "session_id", s.id, "n", drops)
			}
			_ = vadClient.Close()
			vadClient = nil
		}
	}
	defer closeVAD()

	openVAD := func() {
		if s.cfg.VADURL == "" {
			return
		}
		vc, err := vad.Open(ctx, s.cfg.VADURL, vad.Options{
			Threshold:    s.cfg.VADThreshold,
			MinSilenceMS: s.cfg.VADMinSilenceMS,
			SpeechPadMS:  s.cfg.VADSpeechPadMS,
			MinSpeechMS:  s.cfg.VADMinSpeechMS,
		}, s.log)
		if err != nil {
			s.log.Warn("vad.open", "session_id", s.id, "err", err)
			return
		}
		vadClient = vc
		go s.watchVAD(vc)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-s.audioEvents:
			if !ok {
				return
			}
			switch ev.kind {
			case aeReset:
				pcm = pcm[:0]
				closeVAD()
				openVAD()
			case aeSpeechStart:
				// Trim everything captured before the VAD detected speech,
				// keeping a small lookback so we don't clip the word onset.
				// Without this, room noise / silence accumulated since
				// listen.start gets sent to Whisper and triggers
				// training-data hallucinations on long quiet inputs.
				keep := audio.UpstreamSampleHz * speechStartKeepMS / 1000
				if len(pcm) > keep {
					trimmed := len(pcm) - keep
					copy(pcm, pcm[trimmed:])
					pcm = pcm[:keep]
					s.log.Debug("vad.trim", "session_id", s.id, "trimmed_samples", trimmed)
				}
			case aeStop:
				closeVAD()
				if len(pcm) == 0 {
					s.log.Debug("listen.stop.no_audio", "session_id", s.id)
				} else {
					s.dispatchTurn(pcm)
					pcm = pcm[:0]
				}
			case aeInjectText:
				// Drop any captured audio: a debug-injected turn pre-empts
				// whatever the device was about to send through ASR.
				closeVAD()
				pcm = pcm[:0]
				s.dispatchTextTurn(ev.text)
			case aeFrame:
				samples, err := decoder.Decode(ev.frame)
				if err != nil {
					s.log.Warn("opus.decode", "err", err)
				} else {
					pcm = append(pcm, samples...)
					if vadClient != nil {
						if err := vadClient.Feed(samples); err != nil {
							// Queue full — log once per turn, not per frame.
							s.log.Debug("vad.feed", "session_id", s.id, "err", err)
						}
					}
				}
			}
		}
	}
}

// watchVAD forwards speech_end events from the sidecar as aeStop signals on
// the audio-event channel. Speech_start is logged only. Exits when the client
// closes.
func (s *Session) watchVAD(vc *vad.Client) {
	for ev := range vc.Events() {
		switch ev.Type {
		case "speech_start":
			s.log.Debug("vad.speech_start", "session_id", s.id, "ms", ev.MS)
			s.pushAudioEvent(audioEvent{kind: aeSpeechStart})
		case "speech_end":
			s.log.Info("vad.speech_end", "session_id", s.id, "ms", ev.MS)
			s.pushAudioEvent(audioEvent{kind: aeStop})
		}
	}
}

// dispatchTurn runs the pipeline on accumulated PCM and writes the
// wire frames out in order. Returns when the turn finishes or aborts.
func (s *Session) dispatchTurn(pcm []int16) {
	if s.pipeline == nil {
		s.log.Warn("dispatch.no_pipeline", "session_id", s.id)
		return
	}
	turn := s.currentTurn()
	s.log.Info("turn.start", "session_id", s.id, "pcm_samples", len(pcm))

	dialogue := s.snapshotDialogue()
	out, resultCh := s.pipeline.Run(turn, pcm, dialogue)
	s.streamOutput(turn, out)
	// resultCh holds the final assistant text. Drain it (with ctx
	// fallback) and append the pair to our per-session history.
	var assistantText string
	select {
	case res, ok := <-resultCh:
		if ok && res.Transcript != "" && res.AssistantText != "" {
			s.appendDialogue(res.Transcript, res.AssistantText)
			assistantText = res.AssistantText
		}
	case <-turn.Done():
	}
	s.log.Info("turn.end", "session_id", s.id, "reply", assistantText)
}

// dispatchTextTurn runs the pipeline with a server-supplied transcript
// in place of ASR. Same lifecycle and channel-handling as dispatchTurn
// so the device sees a normal stt → tts → audio → emotion → tts.stop
// sequence. Runs on the turnLoop goroutine via the aeInjectText event,
// so it serializes naturally with any in-flight real turn.
func (s *Session) dispatchTextTurn(text string) {
	if s.pipeline == nil {
		s.log.Warn("dispatch.no_pipeline", "session_id", s.id)
		return
	}
	// Inject runs against a context tied to the WS lifetime, not the
	// per-turn context. Reason: the device's normal listen:start handler
	// calls resetTurn() which cancels turnCtx, and that fires during the
	// initial handshake — racing the inject with no way to win on a slow
	// link. The injected turn either completes or the WS dies; we don't
	// want it cancelled by routine state transitions.
	parent := s.runCtx
	if parent == nil {
		parent = context.Background()
	}
	turn, cancel := context.WithCancel(parent)
	defer cancel()
	s.log.Info("turn.start", "session_id", s.id, "source", "inject", "text", text)

	dialogue := s.snapshotDialogue()
	out, resultCh := s.pipeline.RunFromText(turn, text, dialogue)
	s.streamOutput(turn, out)
	var assistantText string
	select {
	case res, ok := <-resultCh:
		if ok && res.Transcript != "" && res.AssistantText != "" {
			s.appendDialogue(res.Transcript, res.AssistantText)
			assistantText = res.AssistantText
		}
	case <-turn.Done():
	}
	s.log.Info("turn.end", "session_id", s.id, "reply", assistantText)
}

// InjectText queues a debug-injected user turn. The turnLoop picks it
// up off the same channel that handles real audio, so injection serializes
// with normal listen → dispatch flow.
func (s *Session) InjectText(text string) {
	s.pushAudioEvent(audioEvent{kind: aeInjectText, text: text})
}

// ID returns the per-session UUID. Used by the /debug endpoints.
func (s *Session) ID() string { return s.id }

// DeviceID returns the MAC-format Device-Id from the WS handshake.
func (s *Session) DeviceID() string { return s.deviceID }

// ClientID returns the Client-Id from the WS handshake.
func (s *Session) ClientID() string { return s.clientID }

func (s *Session) handleWakeText(text string) {
	// We don't have PCM for a wake-word event; instead feed the wake
	// phrase straight to the LLM by faking a short transcript. For now
	// just send an STT echo so the device shows the wake word and the
	// user knows we're alive. The follow-up turn will come from the
	// listen.stop the device sends after.
	turn := s.currentTurn()
	_ = s.sendJSON(turn, proto.STT{Type: "stt", SessionID: s.id, Text: text})
}

func (s *Session) streamOutput(ctx context.Context, out *pipeline.Output) {
	pacer := pipeline.NewPacer(s.cfg.AudioFrameMS)
	ttsStarted := false
	sttSent := false
	var emotionSent bool

	flushSTTAndStart := func() error {
		if !sttSent {
			sttSent = true
		}
		if !ttsStarted {
			ttsStarted = true
			if err := s.sendJSON(ctx, proto.TTS{
				Type: "tts", SessionID: s.id, State: "start",
			}); err != nil {
				return err
			}
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			s.log.Info("turn.aborted", "session_id", s.id)
			return
		case stt, ok := <-out.STT:
			if !ok {
				out.STT = nil
				s.log.Debug("stt.closed", "session_id", s.id)
			} else {
				if err := s.sendJSON(ctx, proto.STT{
					Type: "stt", SessionID: s.id, Text: stt,
				}); err != nil {
					s.log.Error("send.stt", "err", err)
					return
				}
				if err := flushSTTAndStart(); err != nil {
					s.log.Error("send.start", "err", err)
					return
				}
			}
		case sentence, ok := <-out.SentenceStart:
			if !ok {
				out.SentenceStart = nil
				s.log.Debug("sentence.closed", "session_id", s.id)
			} else {
				if err := flushSTTAndStart(); err != nil {
					return
				}
				if err := s.sendJSON(ctx, proto.TTS{
					Type: "tts", SessionID: s.id, State: "sentence_start", Text: sentence,
				}); err != nil {
					s.log.Error("send.sentence", "err", err)
					return
				}
			}
		case pkt, ok := <-out.Audio:
			if !ok {
				out.Audio = nil
				s.log.Debug("audio.closed", "session_id", s.id, "sent", pacer.SentCount())
			} else {
				if err := pacer.WaitForSlot(ctx); err != nil {
					return
				}
				if err := s.sendBinary(ctx, pkt); err != nil {
					s.log.Error("send.audio", "err", err)
					return
				}
				pacer.Sent()
				if n := pacer.SentCount(); n == 1 || n%20 == 0 {
					s.log.Debug("audio.sent", "session_id", s.id, "n", n, "size", len(pkt))
				}
			}
		case emotion, ok := <-out.Emotion:
			if !ok {
				out.Emotion = nil
				s.log.Debug("emotion.closed", "session_id", s.id)
			} else if !emotionSent {
				emotionSent = true
				if err := s.sendJSON(ctx, proto.LLM{
					Type: "llm", SessionID: s.id, Emotion: emotion,
				}); err != nil {
					s.log.Warn("send.emotion", "err", err)
				}
			}
		case err, ok := <-out.Errors:
			if !ok {
				out.Errors = nil
				s.log.Debug("errors.closed", "session_id", s.id)
			} else {
				s.log.Error("pipeline", "session_id", s.id, "err", err)
				_ = s.sendJSON(ctx, proto.Alert{
					Type: "alert", Status: "error", Message: err.Error(), Emotion: "confused",
				})
				return
			}
		}
		// All channels drained?
		if out.STT == nil && out.SentenceStart == nil && out.Audio == nil &&
			out.Emotion == nil && out.Errors == nil {
			s.log.Debug("streamOutput.drained", "session_id", s.id)
			break
		}
	}

	if !ttsStarted {
		// Empty utterance — never sent tts.start, so skip tts.stop too.
		s.log.Info("turn.empty", "session_id", s.id)
		return
	}
	// Drain device-side jitter buffer before stop, matches xinnan-tech.
	select {
	case <-ctx.Done():
		return
	case <-time.After(pacer.TailDrainMS()):
	}
	if err := s.sendJSON(ctx, proto.TTS{
		Type: "tts", SessionID: s.id, State: "stop",
	}); err != nil {
		s.log.Error("send.stop", "err", err)
	}
	// turn.end log moved to dispatchTurn so it can include the full assistant
	// reply (resultCh fires after streamOutput returns).
}

// snapshotDialogue returns a copy of the conversation history (user +
// assistant pairs since this WS opened) for the pipeline to consume.
func (s *Session) snapshotDialogue() []llm.Message {
	s.dialogueMu.Lock()
	defer s.dialogueMu.Unlock()
	if len(s.dialogue) == 0 {
		return nil
	}
	out := make([]llm.Message, len(s.dialogue))
	copy(out, s.dialogue)
	return out
}

// appendDialogue records one user/assistant exchange and trims to keep
// the prefix bounded so TTFT stays reasonable on long conversations.
func (s *Session) appendDialogue(userText, assistantText string) {
	s.dialogueMu.Lock()
	defer s.dialogueMu.Unlock()
	s.dialogue = append(s.dialogue,
		llm.Message{Role: "user", Content: userText},
		llm.Message{Role: "assistant", Content: assistantText},
	)
	if max := maxDialogueTurns * 2; len(s.dialogue) > max {
		s.dialogue = s.dialogue[len(s.dialogue)-max:]
	}
}

func (s *Session) sendJSON(ctx context.Context, v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageText, buf)
}

func (s *Session) sendBinary(ctx context.Context, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageBinary, data)
}

func (s *Session) noteAudioActivity() {
	s.lastAudioMu.Lock()
	s.lastAudio = time.Now()
	s.lastAudioMu.Unlock()
}

func (s *Session) idleSince() time.Duration {
	s.lastAudioMu.Lock()
	t := s.lastAudio
	s.lastAudioMu.Unlock()
	if t.IsZero() {
		return 0
	}
	return time.Since(t)
}
