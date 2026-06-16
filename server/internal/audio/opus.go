// Package audio handles opus encode/decode and PCM resampling between
// the device's wire formats and the sample rates Whisper/Piper expect.
package audio

import (
	"fmt"

	"gopkg.in/hraban/opus.v2"
)

// Device sends 16 kHz mono opus, 60 ms frames. Server sends 24 kHz mono
// opus, 60 ms frames. Match what the firmware advertises in its hello.
// See PROTOCOL.md "Binary frames".
const (
	UpstreamSampleHz   = 16000
	DownstreamSampleHz = 24000
	FrameMS            = 60
	Channels           = 1
)

// SamplesPerFrame returns int16 samples per opus frame at the given rate.
func SamplesPerFrame(rateHz int) int {
	return rateHz * FrameMS / 1000
}

// Decoder wraps libopus for upstream device → server PCM decoding.
type Decoder struct {
	dec     *opus.Decoder
	scratch []int16 // reused per Decode to avoid alloc
}

func NewDecoder(sampleHz int) (*Decoder, error) {
	d, err := opus.NewDecoder(sampleHz, Channels)
	if err != nil {
		return nil, fmt.Errorf("opus decoder: %w", err)
	}
	return &Decoder{
		dec:     d,
		scratch: make([]int16, SamplesPerFrame(sampleHz)*4), // headroom for variable frame sizes
	}, nil
}

// Decode one opus packet into PCM int16 samples. The returned slice is
// reused on subsequent calls — copy if you need to retain it.
func (d *Decoder) Decode(opusPacket []byte) ([]int16, error) {
	n, err := d.dec.Decode(opusPacket, d.scratch)
	if err != nil {
		return nil, fmt.Errorf("opus decode: %w", err)
	}
	return d.scratch[:n], nil
}

// Encoder wraps libopus for server → device PCM encoding.
type Encoder struct {
	enc *opus.Encoder
}

func NewEncoder(sampleHz int) (*Encoder, error) {
	e, err := opus.NewEncoder(sampleHz, Channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	// VoIP-friendly defaults: aggressive low-bitrate works fine for speech
	// on the device's small jitter buffer.
	_ = e.SetBitrate(24000)
	_ = e.SetComplexity(8)
	return &Encoder{enc: e}, nil
}

// Encode one frame's worth of PCM int16 samples into an opus packet.
// pcm length must equal SamplesPerFrame(sampleHz). out is the buffer;
// caller provides one to avoid allocations.
func (e *Encoder) Encode(pcm []int16, out []byte) ([]byte, error) {
	n, err := e.enc.Encode(pcm, out)
	if err != nil {
		return nil, fmt.Errorf("opus encode: %w", err)
	}
	return out[:n], nil
}
