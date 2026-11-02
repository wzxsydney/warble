package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type matchScore struct {
	Date      string
	Home      string
	Away      string
	HomeScore string
	AwayScore string
}

// scoreboardScores keeps only completed games and preserves the home/away mapping.
func scoreboardScores(data []byte) ([]matchScore, error) {
	var board struct {
		Events []struct {
			Date         string `json:"date"`
			Competitions []struct {
				Status struct {
					Type struct {
						Completed bool `json:"completed"`
					} `json:"type"`
				} `json:"status"`
				Competitors []struct {
					HomeAway string `json:"homeAway"`
					Score    string `json:"score"`
					Team     struct {
						DisplayName string `json:"displayName"`
					} `json:"team"`
				} `json:"competitors"`
			} `json:"competitions"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &board); err != nil {
		return nil, err
	}
	var scores []matchScore
	for _, e := range board.Events {
		for _, game := range e.Competitions {
			if !game.Status.Type.Completed {
				continue
			}
			m := matchScore{Date: e.Date}
			for _, c := range game.Competitors {
				n, err := strconv.Atoi(c.Score)
				if err != nil || n < 0 || n > 100 {
					continue
				}
				switch c.HomeAway {
				case "home":
					m.Home = c.Team.DisplayName
					m.HomeScore = c.Score
				case "away":
					m.Away = c.Team.DisplayName
					m.AwayScore = c.Score
				}
			}
			if m.Home != "" && m.Away != "" && m.HomeScore != "" && m.AwayScore != "" {
				scores = append(scores, m)
			}
		}
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].Date > scores[j].Date })
	return scores, nil
}

// footballScores uses ESPN's public scoreboard; no account or key is sent.
// Read numeric results directly so the voice model cannot reverse the winner.
func (w *WebSearch) footballScores(ctx context.Context, arguments string) (string, string) {
	var args struct {
		League string `json:"league"`
		Team   string `json:"team"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil || args.League != "eng.1" {
		return `{"error":"Only English Premier League (eng.1) scores are supported."}`, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	now := time.Now().UTC()
	var scores []matchScore
	failed := false
	for day := 0; day < 8 && len(scores) < 3; day++ {
		date := now.AddDate(0, 0, -day).Format("20060102")
		endpoint := "https://site.api.espn.com/apis/site/v2/sports/soccer/eng.1/scoreboard?dates=" + url.QueryEscape(date)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		resp, err := client.Do(req)
		if err != nil {
			failed = true
			break
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
		resp.Body.Close()
		if resp.StatusCode != 200 || readErr != nil || len(data) > 2*1024*1024 {
			failed = true
			break
		}
		found, err := scoreboardScores(data)
		if err != nil {
			failed = true
			break
		}
		for _, m := range found {
			if args.Team == "" || strings.Contains(strings.ToLower(m.Home+" "+m.Away), strings.ToLower(args.Team)) {
				scores = append(scores, m)
			}
		}
	}
	if len(scores) == 0 {
		if failed {
			return `{"error":"ESPN scoreboard unavailable. Do not invent scores."}`, ""
		}
		return `{"error":"No completed matching Premier League games found in the past eight UTC dates."}`, "[neutral] ESPN shows no completed matching Premier League games in the past eight days."
	}
	if len(scores) > 3 {
		scores = scores[:3]
	}
	var lines []string
	for _, m := range scores {
		date := m.Date
		if t, err := time.Parse("2006-01-02T15:04Z", m.Date); err == nil {
			date = t.Format("2 January")
		}
		lines = append(lines, fmt.Sprintf("On %s, %s %s, %s %s.", date, m.Home, m.HomeScore, m.Away, m.AwayScore))
	}
	reply := "[neutral] According to ESPN, the latest completed Premier League results are: " + strings.Join(lines, " ")
	if failed {
		reply += " I could not check all recent dates."
	}
	if w.Logger != nil {
		w.Logger.Info("football_scores.done", "matches", len(scores))
	}
	data, _ := json.Marshal(map[string]any{"source": "ESPN", "retrieved_at": now.Format(time.RFC3339), "matches": scores})
	return string(data), reply
}
