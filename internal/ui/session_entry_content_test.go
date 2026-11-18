package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Events can populate an inactive detail before SelectDevice's initial read
// callback reaches the UI. That tail is not a previously loaded window.
func TestSessionEntryWaitsForHistoryWhenLiveTailPrecedesRead(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, source := range []string{"live-tail", "event-with-session", "older-window"} {
			t.Run(fmt.Sprintf("%s/dark=%t", source, dark), func(t *testing.T) {
				v, ui := sessionLoadingFixture()
				m := v.Model
				other := domain.Session{ID: "cached", WorkspaceID: "w", Title: "之前的会话"}
				m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
				m.Details[other.ID] = domain.SessionDetail{Session: other, History: &domain.HistoryWindow{First: 1, Last: 1}, Items: []domain.Item{{ID: "cached-reply", Kind: "assistant", Text: "之前会话的正文", Final: true, Order: 1}}}
				ui.SetDark(dark)
				v.navigateSession(other.ID, "w")
				ui.Frame()
				ui.Advance(time.Second)
				frame := 0
				recording := os.Getenv("SHIOJI_ENTRY_CONTENT_RECORDING")
				if recording != "" {
					t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(recording, source, fmt.Sprint(dark)))
				}
				advance := func(duration time.Duration) {
					if recording == "" {
						ui.Advance(duration)
						return
					}
					for duration > 0 {
						saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
						frame++
						step := min(time.Second/60, duration)
						ui.Advance(step)
						duration -= step
					}
				}
				advance(250 * time.Millisecond)
				delete(m.HistoryReads, "s")
				d := m.Details["s"]
				d.Session = domain.Session{}
				d.History = nil
				if source == "older-window" {
					d.Session = m.Snapshot.Sessions[0]
					d.History = &domain.HistoryWindow{First: 1, Last: 1, Newer: true}
					d.Items = []domain.Item{{ID: "old-page", Kind: "assistant", Text: "较早分页的一条消息", Order: 1, Final: true}}
					m.Details["s"] = d
				} else {
					m.Details["s"] = d
					event := domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "tail", Kind: "assistant", Text: "仅收到的一条实时消息", Order: 100, Final: true}}
					if source == "event-with-session" {
						event.Session = &m.Snapshot.Sessions[0]
					}
					if err := m.ApplyEvent(event); err != nil {
						t.Fatal(err)
					}
				}
				v.navigateSession("s", "w")
				ui.Frame()
				advance(80 * time.Millisecond)
				saveNativeSkillImage(t, ui, fmt.Sprintf("tail-before-read-%t.png", dark))
				before := v.loadingHistoryOpacity()
				if source == "older-window" {
					// The opening read removes the non-latest projection.
					d.Items, d.History = nil, &domain.HistoryWindow{Invalidated: true}
					m.Details["s"] = d
				}
				read := m.BeginHistoryRead("s")
				ui.Frame()
				advance(300 * time.Millisecond)
				saveNativeSkillImage(t, ui, fmt.Sprintf("tail-read-pending-%t.png", dark))
				pending := v.loadingHistoryOpacity()
				d = m.Details["s"]
				d.Session = m.Snapshot.Sessions[0]
				d.Items = nil
				for i := 0; i < 15; i++ {
					d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("old-", i), Kind: "assistant", Text: "完整历史内容。\n\n完整历史内容。", Final: true, Order: int64(i + 1)})
				}
				d.History = &domain.HistoryWindow{First: 1, Last: 15}
				if err := m.MergeHistoryPage(d, read, true, domain.HistoryQuery{SessionID: "s"}); err != nil {
					t.Fatal(err)
				}
				m.FoldToolHistory["s"] = true
				ui.Frame()
				advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("tail-history-ready-%t.png", dark))
				if before != 0 || pending != 0 {
					t.Fatalf("partial history flashed before hydration: before=%g pending=%g", before, pending)
				}
				if v.loadingHistoryOpacity() != 1 || !v.lists["s"].AtEnd() {
					t.Fatal("hydrated history did not enter at its latest position")
				}
			})
		}
	}
}

func TestSessionReconnectKeepsVisibleOlderHistory(t *testing.T) {
	v, ui := sessionLoadingFixture(true)
	m := v.Model
	delete(m.HistoryReads, "s")
	d := m.Details["s"]
	d.History.Invalidated = false
	ui.Frame()
	if v.loadingHistoryOpacity() != 1 {
		t.Fatal("fixture did not start with cached content")
	}
	// Jumping to an earlier page in the current visit must remain visible
	// when its owning connection changes epoch and refreshes that page.
	d.History = &domain.HistoryWindow{First: 1, Last: 1, Newer: true}
	m.Details["s"] = d
	ui.Frame()
	before := v.historyRowBounds["cached"]
	m.ConnectionEpoch = "reconnected"
	ui.Frame()
	if v.loadingHistoryOpacity() != 1 || before.H == 0 || v.historyRowBounds["cached"] != before {
		t.Fatal("reconnecting hid or moved the already-visible older page")
	}
}

// Observe every paint, including the frame Tester.Frame normally settles past.
// Cached Markdown history must have its final visible geometry on entry.
func TestSessionEntryMarkdownFirstPaint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = nil
			d.Turns = nil
			for i := 0; i < 10; i++ {
				d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("reply-", i), Kind: "assistant", Text: fmt.Sprintf("历史回复 %d\n\n%s", i, strings.Repeat("正文与 `code`，保留完整内容。\n\n", 3)), Final: true, Order: int64(i + 1)})
			}
			m.Details["s"] = d
			v := New(m, nil, "")
			v.home()
			var frames []map[string]n.Rect
			record := false
			var ui *n.Tester
			ui = n.NewTester(func(c *n.Context) {
				if record && len(frames) == 1 {
					saveNativeSkillImage(t, ui, fmt.Sprintf("before-first-%t.png", dark))
				}
				n.Column(c).Fill().Children(func() { v.View(c) }).DrawOver(func(_ *n.Painter, _ n.Rect) {
					if record {
						frames = append(frames, v.historyRowBounds)
					}
				})
			}, 1200, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			v.navigateSession("s", "w")
			record = true
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("settled-%t.png", dark))
			last := frames[len(frames)-1]
			checked := 0
			for i, frame := range frames {
				for key, r := range last {
					if r.Y < 48 || r.Y+r.H > 650 {
						continue
					}
					got := frame[key]
					checked++
					if got.H == 0 || math.Abs(float64(got.Y-r.Y)) > 1 || math.Abs(float64(got.H-r.H)) > 1 {
						t.Fatalf("history is incomplete on paint %d: %s=%v, settled=%v; first=%v last=%v", i, key, got, r, frames[0], last)
					}
				}
			}
			if checked == 0 {
				t.Fatal("fixture did not expose a complete Markdown row")
			}
		})
	}
}
