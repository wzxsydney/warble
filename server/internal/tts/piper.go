// Package tts posts text to the piper-shim HTTP server and returns
// 22050 Hz mono int16 PCM ready to be resampled and opus-encoded.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rebelthor/warble/server/internal/audio"
)

type Client struct {
	URL  string // e.g. http://127.0.0.1:8001/v1/audio/speech
	HTTP *http.Client
}

func New(url string) *Client {
	return &Client{
		URL: url,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type speakRequest struct {
	Input string `json:"input"`
	Voice string `json:"voice,omitempty"`
}

// Synthesize returns PCM samples + native sample rate (from the WAV header).
// Piper voices are typically 22050 Hz for *-high, 16000 Hz for *-medium.
// Voice is passed per call so the live config can swap it without
// reconstructing the client.
func (c *Client) Synthesize(ctx context.Context, text, voice string) ([]int16, int, error) {
	if text == "" {
		return nil, 0, nil
	}
	body, _ := json.Marshal(speakRequest{Input: text, Voice: voice})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("piper: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("piper status=%d body=%s", resp.StatusCode, buf)
	}
	wav, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read piper wav: %w", err)
	}
	pcm, rate, err := audio.ParseWAVMono16(wav)
	if err != nil {
		return nil, 0, fmt.Errorf("parse piper wav: %w", err)
	}
	return pcm, rate, nil
}
