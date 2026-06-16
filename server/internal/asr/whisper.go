// Package asr posts PCM audio to whisper-server's OpenAI-compatible
// /inference endpoint and returns the transcript.
package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/rebelthor/warble/server/internal/audio"
)

type Client struct {
	URL  string // e.g. http://127.0.0.1:8082/inference
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

type inferenceResponse struct {
	Text string `json:"text"`
}

// Transcribe sends PCM as a WAV file via multipart and returns text.
func (c *Client) Transcribe(ctx context.Context, pcm []int16, sampleHz int) (string, error) {
	var wav bytes.Buffer
	if err := audio.WriteWAVMono16(&wav, pcm, sampleHz); err != nil {
		return "", fmt.Errorf("encode wav: %w", err)
	}
	wavBytes := wav.Bytes()
	// Persist the audio to the debug ring buffer before sending to whisper,
	// so a hung/crashed whisper-server still leaves the WAV behind for replay.
	savedName := saveAudio(wavBytes)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("form file: %w", err)
	}
	if _, err := fw.Write(wavBytes); err != nil {
		return "", fmt.Errorf("copy wav: %w", err)
	}
	// whisper-server ignores model when -m is set on the command line,
	// but the OpenAI-compatible clients all send it, so include for parity.
	_ = mw.WriteField("model", "whisper-1")
	_ = mw.WriteField("response_format", "json")
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("close mw: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper status=%d body=%s", resp.StatusCode, buf)
	}

	var parsed inferenceResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	text := strings.TrimSpace(parsed.Text)
	saveTranscript(savedName, text)
	return text, nil
}
