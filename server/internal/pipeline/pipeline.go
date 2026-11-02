package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/rebelthor/warble/server/internal/asr"
	"github.com/rebelthor/warble/server/internal/audio"
	"github.com/rebelthor/warble/server/internal/configloader"
	"github.com/rebelthor/warble/server/internal/llm"
	"github.com/rebelthor/warble/server/internal/tts"
)

// Output is the channel of events the session layer renders into wire
// frames. One channel per turn; closed when the turn completes or aborts.
type Output struct {
	STT           chan string // user transcript
	SentenceStart chan string // sentence text for tts:sentence_start
	Audio         chan []byte // opus packets in send order
	Emotion       chan string // device emotion tag (one per turn)
	Errors        chan error  // bail-out signal
}

func newOutput() *Output {
	return &Output{
		STT:           make(chan string, 1),
		SentenceStart: make(chan string, 16),
		Audio:         make(chan []byte, 512),
		Emotion:       make(chan string, 1),
		Errors:        make(chan error, 1),
	}
}

func (o *Output) closeAll() {
	close(o.STT)
	close(o.SentenceStart)
	close(o.Audio)
	close(o.Emotion)
	close(o.Errors)
}

type Pipeline struct {
	asr *asr.Client
	llm *llm.Client
	tts *tts.Client
	log *slog.Logger

	cfg *configloader.Loader
}

type Config struct {
	WhisperURL   string
	OllamaURL    string
	OllamaAPIKey string
	SearXNGURL   string
	PiperURL     string
	Loader       *configloader.Loader
}

func New(cfg Config, log *slog.Logger) *Pipeline {
	client := llm.New(cfg.OllamaURL)
	if cfg.OllamaAPIKey != "" || cfg.SearXNGURL != "" {
		client.Search = &llm.WebSearch{APIKey: cfg.OllamaAPIKey, SearXNGURL: cfg.SearXNGURL}
	}
	log.Info("web_search.config", "enabled", client.Search != nil)
	return &Pipeline{
		asr: asr.New(cfg.WhisperURL),
		llm: client,
		tts: tts.New(cfg.PiperURL),
		log: log,
		cfg: cfg.Loader,
	}
}

// SystemPrompt exposes the current system prompt so the session layer
// can seed its per-connection dialogue. Re-read on every call so a
// prompt edit takes effect for new sessions immediately.
func (p *Pipeline) SystemPrompt() string { return p.cfg.Snapshot().SystemPrompt }

// Warmup primes the LLM and TTS subprocesses so the first real user
// turn doesn't pay model-load latency. Runs both in parallel; returns
// after both finish (or fail — we don't fatal, just log).
//
// Reads the live snapshot so a config reload + warmup uses the current
// model + prompt + voice.
func (p *Pipeline) Warmup(ctx context.Context) {
	start := time.Now()
	snap := p.cfg.Snapshot()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s := time.Now()
		if err := p.llm.Warmup(ctx, snap.OllamaModel, snap.SystemPrompt); err != nil {
			p.log.Warn("warmup.llm", "err", err)
			return
		}
		p.log.Debug("warmup.llm.ok", "ms", time.Since(s).Milliseconds())
	}()
	go func() {
		defer wg.Done()
		s := time.Now()
		if _, _, err := p.tts.Synthesize(ctx, "salut", snap.PiperVoice); err != nil {
			p.log.Warn("warmup.tts", "err", err)
			return
		}
		p.log.Debug("warmup.tts.ok", "ms", time.Since(s).Milliseconds())
	}()
	wg.Wait()
	p.log.Info("warmup.done", "total_ms", time.Since(start).Milliseconds())
}

// TurnResult is what the caller gets back once Run completes (in addition
// to the streamed events on Output). Lets the session layer persist the
// assistant reply to its own per-connection dialogue.
type TurnResult struct {
	Transcript    string
	AssistantText string
}

// Run starts one turn. dialogue is the message list (system + prior
// turns) the session has accumulated for this connection; it is NOT
// mutated here. The assistant reply is returned via the doneCh once the
// pipeline finishes streaming so the caller can append it.
//
// Closes Output when the turn ends. resultCh emits exactly one value
// (TurnResult or error already surfaced via Output.Errors).
func (p *Pipeline) Run(ctx context.Context, pcm []int16, dialogue []llm.Message) (*Output, <-chan TurnResult) {
	out := newOutput()
	resultCh := make(chan TurnResult, 1)
	go p.runTurn(ctx, pcm, dialogue, out, resultCh)
	return out, resultCh
}

// RunFromText is Run minus the ASR step. The caller supplies the
// transcript verbatim, then everything downstream (LLM, sentence
// splitter, emotion parser, TTS, opus encode, emotion frame) behaves
// exactly as a real turn would. Used by the /debug/say endpoint and
// any future programmatic-driver hook.
func (p *Pipeline) RunFromText(ctx context.Context, transcript string, dialogue []llm.Message) (*Output, <-chan TurnResult) {
	out := newOutput()
	resultCh := make(chan TurnResult, 1)
	go func() {
		t0 := time.Now()
		defer func() {
			out.closeAll()
			close(resultCh)
		}()
		transcript = strings.TrimSpace(transcript)
		if transcript == "" {
			return
		}
		p.log.Info("inject.text", "text", transcript)
		select {
		case out.STT <- transcript:
		case <-ctx.Done():
			return
		}
		p.runTurnFromTranscript(ctx, t0, transcript, dialogue, out, resultCh)
	}()
	return out, resultCh
}

func (p *Pipeline) runTurn(ctx context.Context, pcm []int16, dialogue []llm.Message, out *Output, resultCh chan<- TurnResult) {
	t0 := time.Now()
	defer func() {
		out.closeAll()
		close(resultCh)
	}()

	asrStart := time.Now()
	transcript, err := p.asr.Transcribe(ctx, pcm, audio.UpstreamSampleHz)
	asrMS := time.Since(asrStart).Milliseconds()
	if err != nil {
		out.Errors <- fmt.Errorf("asr: %w", err)
		return
	}
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return
	}
	p.log.Info("asr.ok", "text", transcript, "asr_ms", asrMS, "since_t0_ms", time.Since(t0).Milliseconds())
	select {
	case out.STT <- transcript:
	case <-ctx.Done():
		return
	}
	p.runTurnFromTranscript(ctx, t0, transcript, dialogue, out, resultCh)
}

// runTurnFromTranscript is everything past ASR + the initial STT emit.
// Both Run (real-audio path) and RunFromText (injected path) call this
// after publishing the transcript on out.STT. Caller owns closing the
// channels via the deferred closer in its outer goroutine.
func (p *Pipeline) runTurnFromTranscript(ctx context.Context, t0 time.Time, transcript string, dialogue []llm.Message, out *Output, resultCh chan<- TurnResult) {
	snap := p.cfg.Snapshot()

	// Build the LLM message list: system prompt (if any) + caller's
	// accumulated dialogue + this user turn.
	messages := make([]llm.Message, 0, len(dialogue)+2)
	if snap.SystemPrompt != "" && !hasSystemMessage(dialogue) {
		messages = append(messages, llm.Message{Role: "system", Content: snap.SystemPrompt})
	}
	messages = append(messages, dialogue...)
	messages = append(messages, llm.Message{Role: "user", Content: transcript})

	splitter := &SentenceSplitter{}
	encoder, err := audio.NewEncoder(audio.DownstreamSampleHz)
	if err != nil {
		out.Errors <- fmt.Errorf("encoder: %w", err)
		return
	}
	sentenceCh := make(chan string, 32)
	synthDone := make(chan error, 1)
	go func() {
		var firstErr error
		for sentence := range sentenceCh {
			if firstErr != nil {
				continue // drain to unblock producer
			}
			if err := p.synthesizeAndStream(ctx, encoder, sentence, snap.PiperVoice, out); err != nil {
				firstErr = err
				p.log.Error("synth", "err", err)
			}
		}
		synthDone <- firstErr
	}()

	var firstSentenceLog sync.Once
	enqueue := func(s string) {
		firstSentenceLog.Do(func() {
			p.log.Info("llm.first_sentence", "since_t0_ms", time.Since(t0).Milliseconds(), "sentence", s)
		})
		select {
		case sentenceCh <- s:
		case <-ctx.Done():
		}
	}

	// Parser strips a leading "[emotion]" tag from the token stream
	// before any text reaches the splitter/TTS. Fires the emotion frame
	// as soon as the closing ']' is seen — typically inside the first
	// chunk — so the face changes by the time audio starts playing.
	var emotionFired bool
	parser := &emotionTagParser{
		onEmotion: func(name string) {
			emotionFired = true
			p.log.Info("llm.emotion_tag", "emotion", name, "since_t0_ms", time.Since(t0).Milliseconds())
			select {
			case out.Emotion <- name:
			case <-ctx.Done():
			default:
			}
		},
	}

	llmOpts := llm.Options{
		Model:       snap.OllamaModel,
		Temperature: snap.Temperature,
		TopP:        snap.TopP,
		NumPredict:  snap.NumPredict,
	}
	var firstTokenLog sync.Once
	full, err := p.llm.Stream(ctx, messages, llmOpts, func(tok string) {
		firstTokenLog.Do(func() {
			p.log.Debug("llm.first_token", "since_t0_ms", time.Since(t0).Milliseconds(), "tok", tok)
		})
		clean := parser.Feed(tok)
		if clean == "" {
			return
		}
		for _, s := range splitter.Feed(clean) {
			enqueue(s)
		}
	})
	if tail := parser.Flush(); tail != "" {
		for _, s := range splitter.Feed(tail) {
			enqueue(s)
		}
	}
	for _, s := range splitter.Flush() {
		enqueue(s)
	}
	close(sentenceCh)
	// Log the full reply at LLM-stream completion, well before TTS finishes
	// streaming the last audio frames. Used by `warble transcript` to render
	// the BOT line ~3 s earlier than waiting on turn.end. Only logged when
	// the stream succeeded; on error or cancel we let turn.end carry the
	// final state instead.
	if err == nil && ctx.Err() == nil {
		p.log.Info("llm.done", "since_t0_ms", time.Since(t0).Milliseconds(), "reply", full)
	}
	if synthErr := <-synthDone; synthErr != nil && ctx.Err() == nil {
		out.Errors <- synthErr
	}
	if err != nil && ctx.Err() == nil {
		out.Errors <- fmt.Errorf("llm: %w", err)
		return
	}

	// Fallback: if the LLM didn't lead with a valid tag, set the face to
	// neutral so it doesn't keep a stale expression from the previous turn.
	if !emotionFired {
		p.log.Debug("llm.emotion_fallback", "since_t0_ms", time.Since(t0).Milliseconds())
		select {
		case out.Emotion <- "neutral":
		case <-ctx.Done():
			return
		default:
		}
	}

	// Return result so the session can persist the assistant reply.
	resultCh <- TurnResult{Transcript: transcript, AssistantText: full}
}

// synthesizeAndStream renders one sentence through Piper, opus-encodes
// the 24 kHz PCM into 60 ms frames and pushes them to out.Audio.
func (p *Pipeline) synthesizeAndStream(ctx context.Context, encoder *audio.Encoder, sentence, voice string, out *Output) error {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return nil
	}
	select {
	case out.SentenceStart <- sentence:
	case <-ctx.Done():
		return ctx.Err()
	}
	synthStart := time.Now()
	pcmTTS, rate, err := p.tts.Synthesize(ctx, sentence, voice)
	if err != nil {
		return fmt.Errorf("piper: %w", err)
	}
	piperMS := time.Since(synthStart).Milliseconds()
	if rate != audio.DownstreamSampleHz {
		pcmTTS = audio.Resample(pcmTTS, rate, audio.DownstreamSampleHz)
	}
	samplesPerFrame := audio.SamplesPerFrame(audio.DownstreamSampleHz)
	buf := make([]byte, 4000)
	frameCount := 0
	for i := 0; i+samplesPerFrame <= len(pcmTTS); i += samplesPerFrame {
		pkt, err := encoder.Encode(pcmTTS[i:i+samplesPerFrame], buf)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		cp := make([]byte, len(pkt))
		copy(cp, pkt)
		select {
		case out.Audio <- cp:
		case <-ctx.Done():
			return ctx.Err()
		}
		frameCount++
	}
	p.log.Debug("synth.done", "sentence", sentence, "piper_ms", piperMS, "opus_frames", frameCount)
	return nil
}

func hasSystemMessage(d []llm.Message) bool {
	for _, m := range d {
		if m.Role == "system" {
			return true
		}
	}
	return false
}

// validEmotions are the emotion strings the StackChan firmware actually
// renders (stackchan_display.cc:329-365). "sleepy" is omitted because it
// triggers a "Zzz…" speech bubble and halts idle motion — not desirable
// for normal Q&A. Anything not in this set falls through to "neutral" on
// the device, so we treat unrecognized tags as "no tag found".
var validEmotions = map[string]struct{}{
	"neutral":  {},
	"happy":    {},
	"laughing": {},
	"angry":    {},
	"sad":      {},
	"crying":   {},
	"doubtful": {},
}

// emotionTagParser pulls a leading "[name]" tag off a streaming LLM
// reply. Feed forwards everything that is NOT part of the tag (and NOT
// leading whitespace before the tag) to the caller. onEmotion fires
// exactly once, the moment the closing ']' is seen, so the face frame
// can race ahead of the first synthesized sentence.
type emotionTagParser struct {
	done         bool
	trimNextLead bool // strip leading whitespace from the first passthrough chunk after a tag
	buf          strings.Builder
	onEmotion    func(string)
}

// maxTagBufBytes caps how much we'll buffer waiting for the closing ']'.
// Tag names are <= 8 chars + brackets; 64 is a generous safety margin so
// a malformed reply doesn't stall TTS.
const maxTagBufBytes = 64

func (p *emotionTagParser) Feed(tok string) string {
	if p.done {
		if p.trimNextLead {
			tok = strings.TrimLeft(tok, " \t\r\n")
			if tok != "" {
				p.trimNextLead = false
			}
		}
		return tok
	}
	p.buf.WriteString(tok)
	s := p.buf.String()
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed == "" {
		return ""
	}
	if trimmed[0] != '[' {
		p.done = true
		p.buf.Reset()
		return s
	}
	idx := strings.IndexByte(trimmed, ']')
	if idx < 0 {
		if p.buf.Len() > maxTagBufBytes {
			p.done = true
			p.buf.Reset()
			return s
		}
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(trimmed[1:idx]))
	rest := trimmed[idx+1:]
	p.done = true
	p.buf.Reset()
	if _, ok := validEmotions[name]; !ok {
		// Unknown tag: pass the literal text through (including the
		// brackets) so we don't silently drop content. No emotion fires.
		return s
	}
	if p.onEmotion != nil {
		p.onEmotion(name)
	}
	stripped := strings.TrimLeft(rest, " \t\r\n")
	if stripped == "" {
		// Nothing left in this chunk after the tag — make sure whatever
		// comes next has its own leading whitespace stripped too.
		p.trimNextLead = true
	}
	return stripped
}

func (p *emotionTagParser) Flush() string {
	if p.done {
		return ""
	}
	p.done = true
	out := p.buf.String()
	p.buf.Reset()
	return out
}
