package pipeline

import (
	"context"
	"time"
)

// Pacer reproduces xinnan-tech's AudioRateController behavior so the
// device's small jitter buffer doesn't over/underrun. See PROTOCOL.md
// "Audio pacing".
//
// Strategy:
//  1. Send the first PreBufferCount frames immediately (300 ms of
//     headroom at 60 ms/frame).
//  2. After that, maintain a virtual play position that advances by
//     frame_duration ms per frame. Before sending frame N, sleep until
//     the wall clock has caught up with the play position.
type Pacer struct {
	FrameMS        int
	PreBufferCount int

	sent           int
	startMonotonic time.Time
}

func NewPacer(frameMS int) *Pacer {
	return &Pacer{
		FrameMS:        frameMS,
		PreBufferCount: 5, // matches xinnan-tech's PRE_BUFFER_COUNT
	}
}

// Reset clears pacing state. Call at the start of each TTS turn.
func (p *Pacer) Reset() {
	p.sent = 0
	p.startMonotonic = time.Time{}
}

// WaitForSlot blocks until it's time to send the next frame. Returns
// ctx.Err() if the context is cancelled (use to implement barge-in).
func (p *Pacer) WaitForSlot(ctx context.Context) error {
	if p.sent < p.PreBufferCount {
		return nil
	}
	if p.startMonotonic.IsZero() {
		p.startMonotonic = time.Now()
	}
	// Frame index relative to start of pacing phase (after pre-buffer).
	idx := p.sent - p.PreBufferCount
	wantElapsed := time.Duration(idx*p.FrameMS) * time.Millisecond
	have := time.Since(p.startMonotonic)
	if have >= wantElapsed {
		return nil
	}
	timer := time.NewTimer(wantElapsed - have)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Sent marks one more frame as sent. Call after each successful WS write.
func (p *Pacer) Sent() {
	p.sent++
}

// SentCount returns the total frames marked sent. Used for logging.
func (p *Pacer) SentCount() int { return p.sent }

// TailDrainMS returns how long to sleep after the last frame to let the
// device's jitter buffer fully drain before sending tts.stop. Matches
// xinnan-tech: (PRE_BUFFER_COUNT + 2) * frame_duration ms.
func (p *Pacer) TailDrainMS() time.Duration {
	return time.Duration(p.PreBufferCount+2) * time.Duration(p.FrameMS) * time.Millisecond
}
