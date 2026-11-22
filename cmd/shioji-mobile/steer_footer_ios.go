package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type steerFooterHandoff struct {
	scope               replyHistoryScope
	runningTurn, after  string
	insertion, released string
	started             time.Time
}

func (s *steerFooterHandoff) project(c *ui.Context, scope replyHistoryScope, rows []mobileclient.TranscriptRow, items []domain.Item, uncertain map[string]bool, enabled bool) []mobileclient.TranscriptRow {
	if !enabled || s.scope != scope {
		*s = steerFooterHandoff{scope: scope}
		return rows
	}
	if s.insertion != "" && c.Now().Sub(s.started) >= mobileReplyFade {
		s.released, s.insertion = s.insertion, ""
		return rows
	}
	if s.insertion == "" && s.runningTurn != "" {
		turn, after := s.runningTurn, s.after
		s.runningTurn, s.after = "", ""
		for _, row := range rows {
			if row.Footer == turn && row.Receipt.AfterInput == after && row.Receipt.BeforeInput != "" && row.Receipt.BeforeInput != s.released {
				s.insertion, s.started = row.Receipt.BeforeInput, c.Now()
				break
			}
		}
		if s.insertion == "" && len(rows) != 0 {
			live := rows[len(rows)-1]
			if live.Footer == turn && live.Receipt.BeforeInput == "" && live.Receipt.AfterInput == after && len(live.Receipt.Reply) != 0 {
				// A local dispatch can be journaled before the harness accepts
				// it. Keep it out of the transition until that result is known.
				for i, row := range slices.Backward(rows[:len(rows)-1]) {
					if row.Footer == "" && items[row.First].TurnID == turn {
						item := items[row.First]
						if item.Kind == "user" && item.Status == "dispatching" && !uncertain[strings.TrimPrefix(item.ID, "input:")] {
							return append(rows[:i:i], live)
						}
						break
					}
				}
			}
		}
	}
	if s.insertion != "" {
		// The old progress and its summary share a key. Finish that change
		// before letting the accepted input and the next live footer enter.
		cut := slices.IndexFunc(rows, func(row mobileclient.TranscriptRow) bool {
			return row.Footer == "" && items[row.First].ID == s.insertion
		})
		if cut >= 0 {
			c.AnimationFrame()
			return rows[:cut]
		}
		s.insertion = ""
	}
	return rows
}

func (s *steerFooterHandoff) observeRunning(row mobileclient.TranscriptRow) {
	s.runningTurn, s.after = row.Footer, row.Receipt.AfterInput
}
