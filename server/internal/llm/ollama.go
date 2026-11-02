// Package llm streams chat completions from Ollama via the OpenAI-
// compatible /v1/chat/completions endpoint.
//
// We use /v1/chat/completions, NOT /api/chat. Same Ollama process, same
// model, same parameters — but /api/chat has a ~2.5 s pre-token delay
// while /v1 starts emitting in ~280 ms. Measured directly with curl on
// 2026-05-11. xinnan-tech also uses /v1 (via the openai-python client),
// which is why their first_audio latency is ~3 s faster.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	URL    string // e.g. http://127.0.0.1:11434
	Search *WebSearch
	HTTP   *http.Client
}

func New(url string) *Client {
	return &Client{
		URL: strings.TrimRight(url, "/"),
		HTTP: &http.Client{
			// No client-side timeout: streaming responses can run minutes.
			// Cancellation is via ctx.
			Timeout: 0,
		},
	}
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Options carries per-call model + sampling params so callers can pass a
// snapshot from the runtime config without the client holding mutable
// state. Zero-valued fields fall back to Ollama's defaults except where
// noted below.
type Options struct {
	Model       string  // required
	Temperature float64 // 0 → omitted (Ollama default)
	TopP        float64 // 0 → omitted
	NumPredict  int     // 0 → omitted
}

// openaiChatRequest matches the /v1/chat/completions schema. Note this
// uses top-level sampling fields (not nested "options"), keep_alive
// (Ollama extension, optional), and stream.
type openaiChatRequest struct {
	Tools       []Tool    `json:"tools,omitempty"`
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature,omitempty"`
	TopP        float64   `json:"top_p,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	// RepeatPenalty is an Ollama extension accepted at the top level of
	// /v1/chat/completions. The default of 1.1 can mangle inflected word forms
	// in some languages/models; set to 1.0 to disable.
	RepeatPenalty float64 `json:"repeat_penalty,omitempty"`
	// KeepAlive is an Ollama extension to the OpenAI shape — it pins the
	// model in memory after the request. "24h" effectively means "forever"
	// for a single-device kiosk; the alternative is Ollama's 5 min default
	// which we'd pay every time the user is idle for >5 min.
	KeepAlive       string `json:"keep_alive,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

const keepAliveForever = "24h"

// openaiChunk is one SSE frame from /v1/chat/completions.
type openaiChunk struct {
	Choices []struct {
		Delta struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// streamOnce consumes one completion, accumulating fragmented tool calls.
func (c *Client) streamOnce(ctx context.Context, messages []Message, opts Options, tools []Tool, onToken func(string)) (Message, error) {
	var answer Message
	answer.Role = "assistant"
	req := openaiChatRequest{
		Tools:           tools,
		Model:           opts.Model,
		Messages:        messages,
		Stream:          true,
		Temperature:     opts.Temperature,
		TopP:            opts.TopP,
		MaxTokens:       opts.NumPredict,
		RepeatPenalty:   1.0,
		KeepAlive:       keepAliveForever,
		ReasoningEffort: "none",
	}
	body, err := json.Marshal(req)
	if err != nil {
		return answer, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return answer, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return answer, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		return answer, fmt.Errorf("ollama status=%d body=%s", resp.StatusCode, buf)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var full strings.Builder
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// SSE: "data: {json}"; ignore comment lines and the final [DONE].
		const prefix = "data: "
		if !bytes.HasPrefix(line, []byte(prefix)) {
			continue
		}
		payload := line[len(prefix):]
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		var chunk openaiChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			answer.Content = full.String()
			return answer, fmt.Errorf("parse chunk: %w (line=%s)", err, line)
		}
		for _, ch := range chunk.Choices {
			for _, delta := range ch.Delta.ToolCalls {
				if delta.Index < 0 || delta.Index >= 4 {
					return answer, fmt.Errorf("invalid tool call index")
				}
				for len(answer.ToolCalls) <= delta.Index {
					answer.ToolCalls = append(answer.ToolCalls, ToolCall{})
				}
				call := &answer.ToolCalls[delta.Index]
				call.ID += delta.ID
				call.Type = "function"
				call.Function.Name += delta.Function.Name
				call.Function.Arguments += delta.Function.Arguments
				if len(call.Function.Arguments) > 16*1024 || len(call.Function.Name) > 128 || len(call.ID) > 256 {
					return answer, fmt.Errorf("tool call exceeds size limit")
				}
			}
			if ch.Delta.Content != "" {
				full.WriteString(ch.Delta.Content)
				if len(tools) == 0 {
					onToken(ch.Delta.Content)
				}
			}
		}
		if ctx.Err() != nil {
			answer.Content = full.String()
			return answer, ctx.Err()
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		answer.Content = full.String()
		return answer, fmt.Errorf("scan: %w", err)
	}
	answer.Content = full.String()
	if ctx.Err() != nil {
		return answer, ctx.Err()
	}
	if len(tools) > 0 && len(answer.ToolCalls) == 0 {
		onToken(answer.Content)
	}
	return answer, nil
}

// openaiNonStreaming is the response shape when stream=false.
type openaiNonStreaming struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// Warmup loads the model AND primes Ollama's prompt KV-cache with the
// production system prompt. Without the same system prompt, the first
// real turn pays ~2 s of prompt-prefix reprocessing even though the
// model itself is loaded. Verified empirically on 2026-05-11 — see
// AB_RESULTS.md "First-turn cache miss" finding.
//
// Retries on connection errors and 5xx until ctx expires, so a `warble`
// start race against the ollama brew service still ends with a loaded
// model. ctx should carry a wall-clock budget (main passes 120 s).
//
// Pass the same system prompt the session will use for real turns.
func (c *Client) Warmup(ctx context.Context, model, systemPrompt string) error {
	msgs := []Message{}
	if systemPrompt != "" {
		msgs = append(msgs, Message{Role: "system", Content: systemPrompt})
	}
	msgs = append(msgs, Message{Role: "user", Content: "salut"})
	req := openaiChatRequest{
		Model:     model,
		Messages:  msgs,
		Stream:    false,
		MaxTokens: 1,
		KeepAlive: keepAliveForever,
	}
	body, _ := json.Marshal(req)

	hot := &http.Client{Timeout: 120 * time.Second}
	var lastErr error
	for {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := hot.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("warmup: %w", err)
		} else {
			if resp.StatusCode == http.StatusOK {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				return nil
			}
			buf, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("warmup status=%d body=%s", resp.StatusCode, buf)
			if resp.StatusCode < 500 {
				return lastErr
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last: %v)", ctx.Err(), lastErr)
		case <-time.After(2 * time.Second):
		}
	}
}

// Generate is a non-streaming convenience wrapper. Currently unused —
// kept because emotion tagging may need it again if we move off the
// emoji-scan heuristic.
func (c *Client) Generate(ctx context.Context, messages []Message, opts Options) (string, error) {
	req := openaiChatRequest{
		Model:       opts.Model,
		Messages:    messages,
		Stream:      false,
		Temperature: opts.Temperature,
		TopP:        opts.TopP,
		MaxTokens:   opts.NumPredict,
		KeepAlive:   keepAliveForever,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	short := &http.Client{Timeout: 15 * time.Second}
	resp, err := short.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama status=%d body=%s", resp.StatusCode, buf)
	}

	var data openaiNonStreaming
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if len(data.Choices) == 0 {
		return "", nil
	}
	return data.Choices[0].Message.Content, nil
}
