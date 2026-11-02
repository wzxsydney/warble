package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func event(w http.ResponseWriter, v any) { b, _ := json.Marshal(v); fmt.Fprintf(w, "data: %s\n\n", b) }
func TestSearchToolReplay(t *testing.T) {
	searches := 0
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches++
		if r.Header.Get("Authorization") != "Bearer test-private-key" {
			t.Error("missing auth")
		}
		var q struct {
			Query string `json:"query"`
			Max   int    `json:"max_results"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		if q.Query != "latest news" || q.Max != 3 {
			t.Errorf("bad query: %+v", q)
		}
		fmt.Fprint(w, `{"results":[{"title":"Source","url":"https://example.com","content":"Verified news"}]}`)
	}))
	defer search.Close()
	rounds := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rounds++
		var req openaiChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.ReasoningEffort != "none" {
			t.Error("reasoning re-enabled")
		}
		raw, _ := json.Marshal(req)
		if strings.Contains(string(raw), "test-private-key") {
			t.Error("key leaked to model")
		}
		if rounds == 1 {
			event(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Searching now", "tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]string{"name": "web_search", "arguments": "{\"query\":\"latest "}}}}}}})
			event(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]string{"arguments": "news\"}"}}}}}}})
		} else {
			last := req.Messages[len(req.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "call_1" || !strings.Contains(last.Content, "Verified news") {
				t.Errorf("bad replay: %+v", last)
			}
			event(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": "According to Source, verified news."}}}})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	c := New(model.URL)
	c.Search = &WebSearch{APIKey: "test-private-key", endpoint: search.URL}
	var spoken strings.Builder
	got, err := c.Stream(context.Background(), []Message{{Role: "user", Content: "Search news"}}, Options{Model: "qwen3.5:9b"}, func(s string) { spoken.WriteString(s) })
	if err != nil || got != "According to Source, verified news." || spoken.String() != got || rounds != 2 || searches != 1 {
		t.Fatalf("got=%q spoken=%q rounds=%d searches=%d err=%v", got, spoken.String(), rounds, searches, err)
	}
}

func TestSearchFailureAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "private server details")
	}))
	defer server.Close()
	search := &WebSearch{APIKey: "secret", endpoint: server.URL}
	got := search.search(context.Background(), `{"query":"news"}`)
	if !strings.Contains(got, "401") || strings.Contains(got, "private") || strings.Contains(got, "secret") {
		t.Fatal(got)
	}
	if !strings.Contains(search.search(context.Background(), `{"query":""}`), "error") {
		t.Fatal("empty query accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(server.URL)
	if _, err := c.Stream(ctx, nil, Options{Model: "test"}, func(string) {}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestSearchBudget(t *testing.T) {
	searches, rounds := 0, 0
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { searches++; fmt.Fprint(w, `{"results":[]}`) }))
	defer search.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rounds++
		var req openaiChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if rounds < 3 {
			var calls []any
			for i := 0; i < 2; i++ {
				calls = append(calls, map[string]any{"index": i, "id": fmt.Sprintf("call_%d_%d", rounds, i), "type": "function", "function": map[string]string{"name": "web_search", "arguments": `{"query":"news"}`}})
			}
			event(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}}}})
		} else {
			t.Error("unexpected extra round")
		}
		if rounds == 2 {
			if len(req.Tools) != 0 {
				t.Error("tools still enabled after budget exhausted")
			}
			// A model ignoring the advertised tools must not trigger more searches.
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	c := New(model.URL)
	c.Search = &WebSearch{APIKey: "key", endpoint: search.URL}
	_, err := c.Stream(context.Background(), nil, Options{Model: "test"}, func(string) {})
	if searches != 2 || rounds != 2 || err == nil {
		t.Fatalf("searches=%d rounds=%d err=%v", searches, rounds, err)
	}
}

func TestDisabledSearchStreamsImmediately(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) != 0 {
			t.Error("tools exposed without a key")
		}
		for _, s := range []string{"Hello", " world"} {
			event(w, map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": s}}}})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	var chunks []string
	got, err := New(model.URL).Stream(context.Background(), nil, Options{Model: "test"}, func(s string) { chunks = append(chunks, s) })
	if err != nil || got != "Hello world" || len(chunks) != 2 {
		t.Fatalf("got=%q chunks=%v err=%v", got, chunks, err)
	}
}
