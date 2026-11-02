package llm

import (
	"context"
	"strings"
	"testing"
)

func TestScoreboardCompletedScoresPreserveSides(t *testing.T) {
	data := []byte(`{"events":[{"date":"2026-11-02T20:00Z","competitions":[{"status":{"type":{"completed":false}},"competitors":[{"homeAway":"home","score":"0","team":{"displayName":"Future home"}},{"homeAway":"away","score":"0","team":{"displayName":"Future away"}}]}]},{"date":"2026-11-01T16:30Z","competitions":[{"status":{"type":{"completed":true}},"competitors":[{"homeAway":"away","score":"1","team":{"displayName":"Arsenal"}},{"homeAway":"home","score":"0","team":{"displayName":"Liverpool"}}]}]}]}`)
	got, err := scoreboardScores(data)
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if got[0].Home != "Liverpool" || got[0].HomeScore != "0" || got[0].Away != "Arsenal" || got[0].AwayScore != "1" {
		t.Fatal(got)
	}
	result, spoken := (&WebSearch{}).footballScores(context.Background(), `{"league":"arbitrary"}`)
	if !strings.Contains(result, "error") || spoken != "" {
		t.Fatal("unsupported league accepted")
	}
}
