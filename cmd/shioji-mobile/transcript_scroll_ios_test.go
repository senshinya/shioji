package main

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func cachedMobileTranscript(t *testing.T) (*mobileApp, *ui.Tester, domain.SessionDetail) {
	t.Helper()
	m := app.NewModel()
	session := domain.Session{ID: "cached", WorkspaceID: "work", ThreadID: "thread", Status: domain.ExecutionCompleted}
	m.Snapshot = domain.Snapshot{DeviceID: "device", Sessions: []domain.Session{session,
		{ID: "other", WorkspaceID: "work", ThreadID: "other-thread"}}}
	m.SessionID = session.ID
	cached := domain.SessionDetail{Session: session, Sequence: 40, History: &domain.HistoryWindow{First: 1, Last: 40}}
	for i := 1; i <= 40; i++ {
		cached.Items = append(cached.Items, domain.Item{ID: fmt.Sprint("cached-", i), Order: int64(i),
			Kind: "assistant", Text: fmt.Sprintf("缓存消息 %02d", i)})
	}
	m.Details[session.ID] = cached
	a := &mobileApp{model: m, history: make(map[string]*ui.ListState)}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			session := a.session()
			a.renderTranscript(c, nil, session, m.Details[session.ID], true, nil, nil, nil, ui.Element{}, firstSubmissionView{})
		})
	}, 393, 700)
	return a, tester, cached
}

func TestMobileReopenPositionsCompletedCachedSessionAtLatest(t *testing.T) {
	a, tester, detail := cachedMobileTranscript(t)
	tester.Frame()
	state := a.history[detail.Session.ID]
	state.ScrollTo(4, ui.Start)
	tester.Frame()
	if state.FollowingEnd() {
		t.Fatal("fixture is still following the end")
	}
	a.selectSession("other")
	tester.Frame()
	a.selectSession(detail.Session.ID)
	tester.Frame()
	if !a.history[detail.Session.ID].AtEnd() || !tester.HasText("缓存消息 40") {
		t.Fatal("reopening a cached completed session restored the previous visit's scroll position")
	}
}

func TestMobileCachedTranscriptRefreshFollowsOnlyAtEnd(t *testing.T) {
	for _, mode := range []string{"following", "reading", "opening", "reduce-motion"} {
		t.Run(mode, func(t *testing.T) {
			a, tester, cached := cachedMobileTranscript(t)
			if mode == "reduce-motion" {
				tester.SetPreferences(ui.Preferences{ReduceMotion: true})
			}
			tester.Frame()
			sid := cached.Session.ID
			state := a.history[sid]
			const marker = "缓存消息 36"
			if !state.FollowingEnd() {
				t.Fatal("cached transcript did not open at its end")
			}
			if mode == "reading" {
				tester.Scroll(180, 300, 0, -130)
				tester.Frame()
				if state.FollowingEnd() {
					t.Fatal("upward scroll did not leave end following")
				}
			}
			before, ok := tester.Find(marker)
			if !ok {
				t.Fatal("cached marker is outside the viewport")
			}
			a.model.Connected, a.model.ConnectionEpoch = true, "connected"
			read := a.model.BeginHistoryRead(sid)
			tester.Frame()
			tester.Advance(100 * time.Millisecond)

			page := cached
			page.Items = append(slices.Clone(cached.Items), domain.Item{
				ID: "catchup", Kind: "assistant", Order: 41, Text: "补齐的新消息",
			})
			page.Sequence = 41
			page.History = &domain.HistoryWindow{First: 1, Last: 41}
			if err := a.model.MergeHistoryPage(page, read, true, domain.HistoryQuery{SessionID: sid}); err != nil {
				t.Fatal(err)
			}
			if mode == "opening" {
				a.model.FoldToolHistory[sid] = true
			}
			tester.Frame()
			first, firstVisible := tester.Find(marker)
			tester.Advance(80 * time.Millisecond)
			middle, middleVisible := tester.Find(marker)
			tester.Advance(time.Second)
			end, ok := tester.Find(marker)
			if !firstVisible || !middleVisible || !ok {
				t.Fatal("cached marker disappeared after one appended message")
			}
			near := func(x, y float32) bool { return math.Abs(float64(x-y)) <= 1 }
			switch mode {
			case "reading":
				if !near(before.Y, first.Y) || !near(before.Y, end.Y) || state.FollowingEnd() {
					t.Fatalf("refresh moved a reader from %g to %g (first %g)", before.Y, end.Y, first.Y)
				}
			case "following":
				if !near(before.Y, first.Y) || !(end.Y < middle.Y && middle.Y < first.Y) {
					t.Fatalf("refresh did not glide from the cached position: before=%g first=%g middle=%g end=%g", before.Y, first.Y, middle.Y, end.Y)
				}
			case "opening", "reduce-motion":
				if !near(first.Y, end.Y) || !(end.Y < before.Y) {
					t.Fatalf("refresh did not immediately show the updated end: before=%g first=%g end=%g", before.Y, first.Y, end.Y)
				}
			}
			if mode != "reading" && (!state.FollowingEnd() || !state.AtEnd() || !tester.HasText("补齐的新消息")) {
				t.Fatal("follower did not reach the updated end")
			}
		})
	}
}
