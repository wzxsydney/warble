// Package configloader reads the hot-reloadable portion of warble's
// configuration from disk and serves it via an atomic snapshot.
//
// Two files are watched:
//
//   - prompt.txt  — the system prompt, plain text, edited freely.
//   - runtime.json — voice, model, sampling options.
//
// A background goroutine stats both files at a fixed interval; when an
// mtime advances, the file is re-read and the snapshot pointer is
// swapped. Readers (the pipeline) call Snapshot() per turn and get the
// current values without locks.
//
// Cold-start settings (ports, sidecar URLs, audio frame geometry) stay
// in internal/config and require a process restart — see CLAUDE.md.
package configloader

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot is the immutable view of runtime config the pipeline reads
// per turn. Replaced atomically by the polling goroutine.
type Snapshot struct {
	SystemPrompt string
	PiperVoice   string
	OllamaModel  string
	Temperature  float64
	TopP         float64
	NumPredict   int
}

// runtimeJSON is the on-disk shape of runtime.json. Kept separate from
// Snapshot so the file format can evolve without leaking JSON tags into
// the rest of the code.
type runtimeJSON struct {
	PiperVoice  string  `json:"piper_voice"`
	OllamaModel string  `json:"ollama_model"`
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	NumPredict  int     `json:"num_predict"`
}

// Loader holds the current snapshot and the polling state.
type Loader struct {
	dir         string
	promptPath  string
	runtimePath string

	cur atomic.Pointer[Snapshot]

	mu           sync.Mutex // guards mtimes + onChange
	promptMTime  time.Time
	runtimeMTime time.Time
	onChange     func(old, new *Snapshot)

	log *slog.Logger
}

// New loads both files once, fails if either is missing or invalid, and
// returns a ready-to-use Loader. Polling does not start until Start is
// called.
func New(dir string, log *slog.Logger) (*Loader, error) {
	l := &Loader{
		dir:         dir,
		promptPath:  filepath.Join(dir, "prompt.txt"),
		runtimePath: filepath.Join(dir, "runtime.json"),
		log:         log,
	}
	snap, pMT, rMT, err := l.readAll()
	if err != nil {
		return nil, err
	}
	l.cur.Store(snap)
	l.promptMTime = pMT
	l.runtimeMTime = rMT
	log.Info("config.loaded",
		"dir", dir,
		"voice", snap.PiperVoice,
		"model", snap.OllamaModel,
		"prompt_chars", len(snap.SystemPrompt))
	return l, nil
}

// Snapshot returns the current config. Cheap; safe for concurrent calls.
func (l *Loader) Snapshot() *Snapshot { return l.cur.Load() }

// OnChange registers a callback fired after each successful reload. The
// callback runs on the polling goroutine, so it should be fast or hand
// off to its own goroutine. Set to nil to clear.
func (l *Loader) OnChange(fn func(old, new *Snapshot)) {
	l.mu.Lock()
	l.onChange = fn
	l.mu.Unlock()
}

// Start launches a polling goroutine that stats both files every
// interval and reloads when an mtime advances. Stops when ctx is done.
func (l *Loader) Start(stop <-chan struct{}, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				l.tick()
			}
		}
	}()
}

func (l *Loader) tick() {
	pMT, rMT, err := l.stat()
	if err != nil {
		l.log.Warn("config.stat", "err", err)
		return
	}
	l.mu.Lock()
	changed := pMT.After(l.promptMTime) || rMT.After(l.runtimeMTime)
	l.mu.Unlock()
	if !changed {
		return
	}
	snap, newPMT, newRMT, err := l.readAll()
	if err != nil {
		l.log.Warn("config.reload", "err", err, "note", "keeping previous snapshot")
		return
	}
	old := l.cur.Swap(snap)
	l.mu.Lock()
	l.promptMTime = newPMT
	l.runtimeMTime = newRMT
	cb := l.onChange
	l.mu.Unlock()
	l.log.Info("config.reloaded",
		"voice", snap.PiperVoice,
		"model", snap.OllamaModel,
		"prompt_chars", len(snap.SystemPrompt),
		"prompt_changed", old.SystemPrompt != snap.SystemPrompt,
		"model_changed", old.OllamaModel != snap.OllamaModel,
		"voice_changed", old.PiperVoice != snap.PiperVoice)
	if cb != nil {
		cb(old, snap)
	}
}

func (l *Loader) stat() (time.Time, time.Time, error) {
	ps, err := os.Stat(l.promptPath)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("stat prompt: %w", err)
	}
	rs, err := os.Stat(l.runtimePath)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("stat runtime: %w", err)
	}
	return ps.ModTime(), rs.ModTime(), nil
}

func (l *Loader) readAll() (*Snapshot, time.Time, time.Time, error) {
	promptBytes, err := os.ReadFile(l.promptPath)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("read prompt: %w", err)
	}
	prompt := trimTrailingNewlines(string(promptBytes))
	if prompt == "" {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("prompt.txt is empty")
	}

	rtBytes, err := os.ReadFile(l.runtimePath)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("read runtime: %w", err)
	}
	var rt runtimeJSON
	if err := json.Unmarshal(rtBytes, &rt); err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("parse runtime.json: %w", err)
	}
	if rt.PiperVoice == "" {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("runtime.json: piper_voice is required")
	}
	if rt.OllamaModel == "" {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("runtime.json: ollama_model is required")
	}

	snap := &Snapshot{
		SystemPrompt: prompt,
		PiperVoice:   rt.PiperVoice,
		OllamaModel:  rt.OllamaModel,
		Temperature:  rt.Temperature,
		TopP:         rt.TopP,
		NumPredict:   rt.NumPredict,
	}
	ps, _ := os.Stat(l.promptPath)
	rs, _ := os.Stat(l.runtimePath)
	return snap, ps.ModTime(), rs.ModTime(), nil
}

func trimTrailingNewlines(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
