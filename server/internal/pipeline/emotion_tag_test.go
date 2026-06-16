package pipeline

import (
	"strings"
	"testing"
)

func runParser(tokens []string) (emotion string, fired bool, passthrough string) {
	var p emotionTagParser
	var out strings.Builder
	p.onEmotion = func(name string) {
		emotion = name
		fired = true
	}
	for _, tok := range tokens {
		out.WriteString(p.Feed(tok))
	}
	out.WriteString(p.Flush())
	return emotion, fired, out.String()
}

func TestEmotionTagParser(t *testing.T) {
	cases := []struct {
		name            string
		tokens          []string
		wantEmotion     string
		wantFired       bool
		wantPassthrough string
	}{
		{
			name:            "tag in single token",
			tokens:          []string{"[happy] Salut!"},
			wantEmotion:     "happy",
			wantFired:       true,
			wantPassthrough: "Salut!",
		},
		{
			name:            "tag split across tokens",
			tokens:          []string{"[", "happy", "]", " Salut!"},
			wantEmotion:     "happy",
			wantFired:       true,
			wantPassthrough: "Salut!",
		},
		{
			name:            "tag with leading whitespace tokens",
			tokens:          []string{" ", "\n", "[sad", "] vai"},
			wantEmotion:     "sad",
			wantFired:       true,
			wantPassthrough: "vai",
		},
		{
			name:            "no tag, plain text",
			tokens:          []string{"Salut! ", "Cum ești?"},
			wantFired:       false,
			wantPassthrough: "Salut! Cum ești?",
		},
		{
			name:            "unknown tag passed through verbatim",
			tokens:          []string{"[mystery] hello"},
			wantFired:       false,
			wantPassthrough: "[mystery] hello",
		},
		{
			name:            "sleepy is intentionally rejected (no fire, literal text)",
			tokens:          []string{"[sleepy] Zzz"},
			wantFired:       false,
			wantPassthrough: "[sleepy] Zzz",
		},
		{
			name:            "uppercase tag is normalized",
			tokens:          []string{"[Happy] X"},
			wantEmotion:     "happy",
			wantFired:       true,
			wantPassthrough: "X",
		},
		{
			name:            "unclosed bracket overflows safety cap",
			tokens:          []string{"[", strings.Repeat("x", 80)},
			wantFired:       false,
			wantPassthrough: "[" + strings.Repeat("x", 80),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emo, fired, pass := runParser(tc.tokens)
			if fired != tc.wantFired {
				t.Errorf("fired=%v want=%v", fired, tc.wantFired)
			}
			if emo != tc.wantEmotion {
				t.Errorf("emotion=%q want=%q", emo, tc.wantEmotion)
			}
			if pass != tc.wantPassthrough {
				t.Errorf("passthrough=%q want=%q", pass, tc.wantPassthrough)
			}
		})
	}
}
