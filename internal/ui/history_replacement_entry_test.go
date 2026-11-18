package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestForkWaitsForHistoryWhenSourceWindowOmitsEarlierItems(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui, _ := historyFixture()
			v.Model.WorkerMinor = 45
			v.Model.Readiness.Capabilities.Fork = true
			d := v.Model.Details["s"]
			d.Items = []domain.Item{{ID: "retained-tail", Kind: "assistant", TurnID: "turn", Order: 100, Text: "分叉时只缓存的一条旧回复", Final: true}}
			d.History = &domain.HistoryWindow{First: 100, Last: 100, Older: true}
			v.Model.Details["s"] = d
			ui.SetDark(dark)
			ui.Frame()
			ui.Advance(time.Second)
			record := recordHistoryReplacementEntry(t, ui, "fork", dark)
			record(8)
			v.forkSession(d.Session, "turn")
			ui.Frame()
			ui.Advance(time.Second)
			j := historyJob(t, v, "session.fork")
			in := j.Input.(domain.HistoryChange)
			branch := d.Session
			branch.ID, branch.Title, branch.ThreadID = "branch", "新分叉", "branch-thread"
			body, err := json.Marshal(domain.HistoryOperation{ID: in.OperationID, Input: in, State: "complete", Source: d.Session, Session: branch, KeepOrder: 0})
			if err != nil {
				t.Fatal(err)
			}
			j.Apply(v.Model, body, nil)
			ui.Frame()
			record(10)
			_, partial := ui.Find("分叉时只缓存的一条旧回复")
			shown := partial && v.loadingHistoryOpacity() > 0
			seed := v.Model.Details["branch"]
			t.Logf("branch=%s source older=%t seeded=%d authority=%t first opacity=%g visible=%t", v.Model.SessionID, d.History.Older, len(seed.Items), seed.HasLoadedHistory(), v.loadingHistoryOpacity(), shown)
			latest := replacementEntryHistory(branch, d.Items[0])
			applyReplacementEntryHistory(t, v, ui, latest)
			record(36)
			if v.Model.SessionID != "branch" || !ui.HasText("完整历史的尾部编号 15") {
				t.Fatal("owning reply did not complete the branch", ui.Texts())
			}
			if shown {
				t.Error("one cached row appeared before the owning fork history returned")
			}
		})
	}
}

func TestSessionEntryWaitsForHistoryAfterAnotherClientRewritesIt(t *testing.T) {
	for _, entry := range []string{"return-from-archive", "reopen-cached-refresh", "reopen-settled-control"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%t", entry, dark), func(t *testing.T) {
				v, ui := archiveNavigationFixture()
				m := v.Model
				d := m.Details["s"]
				d.Session.ThreadID = "prior-thread"
				d.Session.Revision = 1
				m.Snapshot.Sessions[0] = d.Session
				d.Items = []domain.Item{{ID: "invalid-old", Kind: "assistant", TurnID: "changed", Order: 101, Text: "修改前缓存中的一条回复", Final: true}}
				d.Turns = []domain.Turn{{ID: "retained", Order: 1, Status: "completed"}, {ID: "changed", Order: 2, Status: "completed"}}
				d.History = &domain.HistoryWindow{First: 101, Last: 101, Older: true}
				d.HistoryLoaded = false
				m.Details["s"] = d
				ui.SetDark(dark)
				ui.Frame()
				ui.Advance(time.Second)
				if v.loadingHistoryOpacity() != 1 {
					t.Fatal("fixture lacked an already read window")
				}
				if entry == "return-from-archive" {
					enterArchiveFromMenu(t, ui)
				} else {
					if entry == "reopen-cached-refresh" {
						// State after controller reopens a latest cache and starts its refresh.
						d.HistoryLoaded = true
						d.History.Invalidated = true
						m.Details["s"] = d
					}
					v.home()
					ui.Frame()
					ui.Advance(time.Second)
				}
				record := recordHistoryReplacementEntry(t, ui, entry, dark)
				record(8)
				newSession := d.Session
				newSession.ThreadID, newSession.Revision = "changed-thread", 2
				keep := int64(1)
				if err := m.ApplyEvent(domain.Event{Type: "history.reset", Sequence: 1, SessionID: "s", Session: &newSession, HistoryTurnOrder: &keep}); err != nil {
					t.Fatal(err)
				}
				replacement := domain.Item{ID: "replacement", Kind: "user", TurnID: "replacement-turn", Order: 200, Text: "另一客户端刚重开的提问", Final: true}
				if err := m.ApplyEvent(domain.Event{Type: "submission", Sequence: 2, SessionID: "s", Session: &newSession, Item: &replacement}); err != nil {
					t.Fatal(err)
				}
				if entry == "return-from-archive" {
					if err := ui.Click("返回会话"); err != nil {
						t.Fatal(err)
					}
				} else {
					v.navigateSession("s", "w")
					ui.Frame()
				}
				record(10)
				_, partial := ui.Find(replacement.Text)
				shown := partial && v.loadingHistoryOpacity() > 0
				info := m.Details["s"]
				t.Logf("raw items=%d invalidated=%t authority=%t opacity=%g visible=%t", len(info.Items), info.History.Invalidated, info.HasLoadedHistory(), v.loadingHistoryOpacity(), shown)
				if len(info.Items) != 1 {
					t.Fatal("fixture did not replace cached window with a single input")
				}
				job := historyJob(t, v, "session.history")
				if q := job.Input.(domain.HistoryQuery); q.SessionID != "s" || q.ThreadID != newSession.ThreadID || q.AroundID != "" {
					t.Fatalf("replacement did not request the new thread's latest history: %+v", q)
				}
				body, err := json.Marshal(replacementEntryHistory(newSession, replacement))
				if err != nil {
					t.Fatal(err)
				}
				job.Apply(m, body, nil)
				ui.Frame()
				record(36)
				if !ui.HasText("完整历史的尾部编号 15") {
					t.Fatal("authoritative replacement did not complete", ui.Texts())
				}
				if shown {
					t.Error("one live replacement appeared while the rest of the history was still absent")
				}
			})
		}
	}
}

func replacementEntryHistory(s domain.Session, item domain.Item) domain.SessionDetail {
	d := domain.SessionDetail{Session: s, Sequence: 2, History: &domain.HistoryWindow{First: 1, Last: item.Order}}
	for i := 1; i <= 15; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("historical-", i), Kind: "assistant", TurnID: "retained", Text: fmt.Sprintf("完整历史的尾部编号 %d\n\n%s", i, strings.Repeat("此前内容。\n\n", 3)), Order: int64(i), Final: true})
	}
	d.Items = append(d.Items, item)
	return d
}

func applyReplacementEntryHistory(t *testing.T, v *View, ui *n.Tester, d domain.SessionDetail) {
	t.Helper()
	read := v.Model.BeginHistoryRead(d.Session.ID)
	if err := v.Model.MergeHistoryPage(d, read, true, domain.HistoryQuery{SessionID: d.Session.ID}); err != nil {
		t.Fatal(err)
	}
	v.Model.FoldToolHistory[d.Session.ID] = true
	ui.Frame()
}

func recordHistoryReplacementEntry(t *testing.T, ui *n.Tester, name string, dark bool) func(int) {
	t.Helper()
	dir := os.Getenv("SHIOJI_REPLACEMENT_ENTRY_CAPTURE")
	if dir != "" {
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name, fmt.Sprint(dark)))
	}
	frame := 0
	return func(n int) {
		for range n {
			if dir != "" {
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%03d.png", frame))
			}
			frame++
			ui.Advance(time.Second / 30)
		}
	}
}
