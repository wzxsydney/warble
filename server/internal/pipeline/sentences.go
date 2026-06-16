// Package pipeline orchestrates ASR → LLM → TTS for one user turn.
package pipeline

import (
	"strings"
	"unicode"
)

// SentenceSplitter accumulates tokens from a streaming LLM and emits
// complete sentences. A sentence ends at ., !, ?, : or newline. Avoids
// splitting on . inside numbers (3.14) or after very short fragments.
type SentenceSplitter struct {
	buf strings.Builder
}

// Feed appends a token. Returns zero or more complete sentences ready to
// hand to TTS.
func (s *SentenceSplitter) Feed(token string) []string {
	if token == "" {
		return nil
	}
	s.buf.WriteString(token)
	return s.extract(false)
}

// Flush returns whatever is left in the buffer as a final sentence. Call
// once the stream ends.
func (s *SentenceSplitter) Flush() []string {
	if s.buf.Len() == 0 {
		return nil
	}
	sentences := s.extract(true)
	remaining := strings.TrimSpace(s.buf.String())
	if remaining != "" {
		sentences = append(sentences, remaining)
		s.buf.Reset()
	}
	return sentences
}

func (s *SentenceSplitter) extract(flushing bool) []string {
	// Split early so TTS can start synthesizing the first chunk while the
	// LLM is still emitting the rest. xinnan-tech does the same.
	const (
		minFirstChunkLen = 4 // accept very short opening pieces (e.g. "Hi!" / "Salut!")
		minCommaChunkLen = 8 // xinnan-tech splits on the first comma even for "Sunt StackChan," (14 chars)
	)

	text := s.buf.String()
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		isStrong := c == '.' || c == '!' || c == '?' || c == ':' || c == '\n'
		isWeak := c == ','
		if !isStrong && !isWeak {
			continue
		}
		// Skip decimal points (digit . digit)
		if c == '.' && i > 0 && i < len(text)-1 &&
			unicode.IsDigit(rune(text[i-1])) && unicode.IsDigit(rune(text[i+1])) {
			continue
		}
		end := i + 1
		// Eat repeated punctuation: "..." → one sentence
		if isStrong {
			for end < len(text) && (text[end] == '.' || text[end] == '!' || text[end] == '?') {
				end++
			}
		}
		piece := strings.TrimSpace(text[start:end])
		if piece == "" {
			start = end
			continue
		}
		if !flushing {
			minLen := minFirstChunkLen
			if isWeak {
				minLen = minCommaChunkLen
			}
			if len(piece) < minLen {
				continue
			}
		}
		out = append(out, piece)
		start = end
	}
	if start > 0 {
		remaining := text[start:]
		s.buf.Reset()
		s.buf.WriteString(remaining)
	}
	return out
}
