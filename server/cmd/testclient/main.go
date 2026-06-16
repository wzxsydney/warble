// Command testclient impersonates a xiaozhi device on the wire to
// validate the server end-to-end without flashing hardware.
//
// Scenarios:
//
//	-scenario full      stt + tts + audio + tts.stop
//	-scenario barge     same as full, then abort after N audio frames
//	-scenario reconnect run scenario=full, close, reopen, run again
//	-scenario idle      open + hello + wait > 120 s, expect server close
//
// Fixtures:
//
//	-fixture wire       no real audio; pipeline will see decode errors and
//	                    produce an empty turn (M1 smoke)
//	-fixture <wav>      M2 end-to-end with a real mono PCM WAV
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/rebelthor/warble/server/internal/audio"
)

func main() {
	otaURL := flag.String("ota", "http://127.0.0.1:8003/xiaozhi/ota/", "OTA endpoint")
	deviceID := flag.String("device", "aa:bb:cc:dd:ee:ff", "Device-Id header")
	clientID := flag.String("client", "warble-testclient-0001", "Client-Id header")
	fixture := flag.String("fixture", "wire", "wire | path to mono PCM WAV")
	scenario := flag.String("scenario", "full", "full | barge | reconnect | idle")
	bargeAfter := flag.Int("barge-after", 5, "send abort after this many audio frames")
	timeout := flag.Duration("timeout", 90*time.Second, "overall timeout")
	flag.Parse()

	wsURL, err := bootstrap(*otaURL, *deviceID, *clientID)
	if err != nil {
		log.Fatalf("ota: %v", err)
	}
	log.Printf("ota.ok ws_url=%s scenario=%s", wsURL, *scenario)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	runOnce := func() error {
		hdr := http.Header{}
		hdr.Set("Device-Id", *deviceID)
		hdr.Set("Client-Id", *clientID)
		hdr.Set("Protocol-Version", "1")
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
		if err != nil {
			return fmt.Errorf("ws.dial: %w", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		log.Printf("ws.connected")
		sessionID := exchangeHello(ctx, conn)

		switch *scenario {
		case "full":
			return runFull(ctx, conn, sessionID, *fixture)
		case "barge":
			return runBarge(ctx, conn, sessionID, *fixture, *bargeAfter)
		case "idle":
			return runIdle(ctx, conn, sessionID)
		default:
			return fmt.Errorf("unknown scenario: %s", *scenario)
		}
	}

	if *scenario == "reconnect" {
		*scenario = "full"
		log.Printf("--- run #1 ---")
		if err := runOnce(); err != nil {
			log.Fatalf("run #1: %v", err)
		}
		log.Printf("--- reconnecting ---")
		log.Printf("--- run #2 ---")
		if err := runOnce(); err != nil {
			log.Fatalf("run #2: %v", err)
		}
		log.Printf("reconnect scenario passed")
		return
	}

	if err := runOnce(); err != nil {
		log.Fatalf("%s: %v", *scenario, err)
	}
	log.Printf("scenario=%s passed", *scenario)
}

func runFull(ctx context.Context, conn *websocket.Conn, sessionID, fixture string) error {
	if err := sendListenStart(ctx, conn, sessionID); err != nil {
		return fmt.Errorf("listen.start: %w", err)
	}
	log.Printf("→ listen.start")

	if err := sendFixture(ctx, conn, fixture); err != nil {
		return err
	}

	if err := sendListenStop(ctx, conn, sessionID); err != nil {
		return fmt.Errorf("listen.stop: %w", err)
	}
	log.Printf("→ listen.stop")

	return expectTurn(ctx, conn, fixture != "wire", nil)
}

func runBarge(ctx context.Context, conn *websocket.Conn, sessionID, fixture string, bargeAfter int) error {
	if err := sendListenStart(ctx, conn, sessionID); err != nil {
		return err
	}
	log.Printf("→ listen.start")
	if err := sendFixture(ctx, conn, fixture); err != nil {
		return err
	}
	if err := sendListenStop(ctx, conn, sessionID); err != nil {
		return err
	}
	log.Printf("→ listen.stop")

	// Wait until we see `bargeAfter` opus audio frames, then send abort.
	onAudio := func(count int) (abortNow bool) {
		return count == bargeAfter
	}
	abortAt := time.Time{}
	err := expectTurnInterruptible(ctx, conn, true, onAudio, func() error {
		if err := writeJSON(ctx, conn, map[string]any{
			"type": "abort", "session_id": sessionID, "reason": "wake_word_detected",
		}); err != nil {
			return err
		}
		abortAt = time.Now()
		log.Printf("→ abort (after %d audio frames)", bargeAfter)
		return nil
	})
	if err != nil {
		return err
	}
	if abortAt.IsZero() {
		return fmt.Errorf("barge: abort was never sent (got tts.stop before %d audio frames)", bargeAfter)
	}
	since := time.Since(abortAt)
	log.Printf("barge: tts.stop arrived %s after abort", since)
	if since > 2*time.Second {
		return fmt.Errorf("barge: tts.stop took too long (%s)", since)
	}
	return nil
}

func runIdle(ctx context.Context, conn *websocket.Conn, sessionID string) error {
	log.Printf("idle: waiting for server to close us after IdleTimeout (~120 s)")
	start := time.Now()
	deadline, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(deadline)
		if err != nil {
			elapsed := time.Since(start)
			log.Printf("idle: server closed connection after %s (err=%v)", elapsed, err)
			if elapsed < 110*time.Second {
				return fmt.Errorf("server closed too soon: %s", elapsed)
			}
			if elapsed > 145*time.Second {
				return fmt.Errorf("server closed too late: %s", elapsed)
			}
			return nil
		}
	}
}

func sendFixture(ctx context.Context, conn *websocket.Conn, fixture string) error {
	if fixture == "wire" {
		for i := 0; i < 5; i++ {
			_ = conn.Write(ctx, websocket.MessageBinary, make([]byte, 80))
		}
		log.Printf("→ 5 garbage frames (wire mode)")
		return nil
	}
	frames, err := encodeFixture(fixture)
	if err != nil {
		return fmt.Errorf("encode fixture: %w", err)
	}
	log.Printf("→ sending %d opus frames from %s", len(frames), fixture)
	for _, pkt := range frames {
		if err := conn.Write(ctx, websocket.MessageBinary, pkt); err != nil {
			return fmt.Errorf("send audio: %w", err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	return nil
}

func exchangeHello(ctx context.Context, conn *websocket.Conn) string {
	clientHello := map[string]any{
		"type":      "hello",
		"version":   1,
		"transport": "websocket",
		"features":  map[string]any{"mcp": true},
		"audio_params": map[string]any{
			"format":         "opus",
			"sample_rate":    16000,
			"channels":       1,
			"frame_duration": 60,
		},
	}
	if err := writeJSON(ctx, conn, clientHello); err != nil {
		log.Fatalf("send hello: %v", err)
	}
	log.Printf("→ hello")
	raw, err := readText(ctx, conn)
	if err != nil {
		log.Fatalf("recv hello: %v", err)
	}
	var srvHello struct {
		Type        string `json:"type"`
		Transport   string `json:"transport"`
		SessionID   string `json:"session_id"`
		AudioParams struct {
			SampleRate    int `json:"sample_rate"`
			FrameDuration int `json:"frame_duration"`
		} `json:"audio_params"`
	}
	if err := json.Unmarshal(raw, &srvHello); err != nil {
		log.Fatalf("decode hello: %v (raw=%s)", err, raw)
	}
	if srvHello.Type != "hello" || srvHello.Transport != "websocket" || srvHello.SessionID == "" {
		log.Fatalf("bad server hello: %s", raw)
	}
	log.Printf("← hello session=%s rate=%d frame_ms=%d",
		srvHello.SessionID, srvHello.AudioParams.SampleRate, srvHello.AudioParams.FrameDuration)
	return srvHello.SessionID
}

func sendListenStart(ctx context.Context, conn *websocket.Conn, sessionID string) error {
	return writeJSON(ctx, conn, map[string]any{
		"type": "listen", "session_id": sessionID, "state": "start", "mode": "auto",
	})
}

func sendListenStop(ctx context.Context, conn *websocket.Conn, sessionID string) error {
	return writeJSON(ctx, conn, map[string]any{
		"type": "listen", "session_id": sessionID, "state": "stop",
	})
}

func encodeFixture(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pcm, rate, err := audio.ParseWAVMono16(data)
	if err != nil {
		return nil, err
	}
	if rate != audio.UpstreamSampleHz {
		pcm = audio.Resample(pcm, rate, audio.UpstreamSampleHz)
	}
	enc, err := audio.NewEncoder(audio.UpstreamSampleHz)
	if err != nil {
		return nil, err
	}
	samplesPerFrame := audio.SamplesPerFrame(audio.UpstreamSampleHz)
	buf := make([]byte, 4000)
	var frames [][]byte
	for i := 0; i+samplesPerFrame <= len(pcm); i += samplesPerFrame {
		pkt, err := enc.Encode(pcm[i:i+samplesPerFrame], buf)
		if err != nil {
			return nil, fmt.Errorf("encode frame %d: %w", len(frames), err)
		}
		cp := make([]byte, len(pkt))
		copy(cp, pkt)
		frames = append(frames, cp)
	}
	return frames, nil
}

func bootstrap(otaURL, deviceID, clientID string) (string, error) {
	body := bytes.NewBufferString(`{"version":1,"application":{"version":"0.0.0"}}`)
	req, err := http.NewRequest(http.MethodPost, otaURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Device-Id", deviceID)
	req.Header.Set("Client-Id", clientID)
	req.Header.Set("User-Agent", "warble-testclient/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status=%d body=%s", resp.StatusCode, raw)
	}
	var data struct {
		Websocket struct {
			URL string `json:"url"`
		} `json:"websocket"`
		ServerTime struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"server_time"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if data.Websocket.URL == "" {
		return "", fmt.Errorf("missing websocket.url")
	}
	if data.ServerTime.Timestamp == 0 {
		return "", fmt.Errorf("missing server_time.timestamp")
	}
	if u := strings.Replace(data.Websocket.URL, "ws://0.0.0.0", "ws://127.0.0.1", 1); u != "" {
		return u, nil
	}
	return data.Websocket.URL, nil
}

func writeJSON(ctx context.Context, c *websocket.Conn, v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, buf)
}

func readText(ctx context.Context, c *websocket.Conn) ([]byte, error) {
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ == websocket.MessageText {
			return data, nil
		}
	}
}

// expectTurn waits for the canonical reply sequence.
func expectTurn(ctx context.Context, c *websocket.Conn, expectAudio bool, onLLM func(emotion string)) error {
	return expectTurnInterruptible(ctx, c, expectAudio, nil, nil)
}

// expectTurnInterruptible is like expectTurn but allows the caller to
// inject an action (e.g. send abort) when a specific number of audio
// frames has arrived. Useful for barge-in testing.
func expectTurnInterruptible(
	ctx context.Context,
	c *websocket.Conn,
	expectAudio bool,
	onAudio func(count int) (triggerNow bool),
	trigger func() error,
) error {
	seenSTT, seenStart, seenSentence := false, false, false
	audioFrames := 0
	emotion := ""
	triggered := false
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageBinary {
			audioFrames++
			if !triggered && onAudio != nil && onAudio(audioFrames) {
				triggered = true
				if err := trigger(); err != nil {
					return fmt.Errorf("trigger: %w", err)
				}
			}
			continue
		}
		var env struct {
			Type    string `json:"type"`
			State   string `json:"state"`
			Text    string `json:"text"`
			Emotion string `json:"emotion"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			return err
		}
		switch env.Type {
		case "stt":
			log.Printf("← stt text=%q", env.Text)
			seenSTT = true
		case "tts":
			log.Printf("← tts.%s text=%q", env.State, env.Text)
			switch env.State {
			case "start":
				seenStart = true
			case "sentence_start":
				seenSentence = true
			case "stop":
				return finalize(seenSTT, seenStart, seenSentence, audioFrames, expectAudio, emotion, triggered, onAudio != nil)
			}
		case "llm":
			log.Printf("← llm emotion=%q", env.Emotion)
			emotion = env.Emotion
		case "alert":
			log.Printf("← alert status=%q message=%q emotion=%q", env.Status, env.Message, env.Emotion)
			return fmt.Errorf("server alert: %s", env.Message)
		default:
			log.Printf("← %s", env.Type)
		}
	}
}

func finalize(seenSTT, seenStart, seenSentence bool, audioFrames int, expectAudio bool, emotion string, triggered, hadTrigger bool) error {
	if !seenSTT {
		return fmt.Errorf("missing stt")
	}
	if !seenStart {
		return fmt.Errorf("missing tts.start")
	}
	if !seenSentence {
		return fmt.Errorf("missing tts.sentence_start")
	}
	if expectAudio && audioFrames == 0 {
		return fmt.Errorf("expected opus audio frames, got 0")
	}
	if hadTrigger && !triggered {
		return fmt.Errorf("expected to barge-in but got clean tts.stop first")
	}
	log.Printf("summary stt=ok start=ok sentence=ok audio_frames=%d emotion=%q triggered=%v",
		audioFrames, emotion, triggered)
	return nil
}
