package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestColdGitHydrationKeepsComposerLayout(t *testing.T) {
	for _, home := range []bool{false, true} {
		t.Run(map[bool]string{true: "home", false: "saved-session"}[home], func(t *testing.T) {
			m := layoutModel()
			m.ConnectionEpoch = "fresh"
			if home {
				m.SessionID = ""
				m.GitRepositories["w"] = "/repo"
				m.GitDrafts["w"] = domain.GitChoice{Branch: "main"}
			} else {
				m.Snapshot.Sessions[0].Context.Repository = "/repo"
				m.Snapshot.Sessions[0].GitBranch = "main"
				m.Details = map[string]domain.SessionDetail{}
			}
			v := New(m, app.NewController(""), "")
			ui := n.NewTester(v.View, 1240, 820)
			before, _ := ui.Find("消息编辑器")
			key, _ := v.gitQuery()
			m.SetGitStatus(key, domain.GitStatus{Applicable: true, Repository: "/repo", Branch: "main", Branches: []string{"main"}, DefaultBranch: "main"})
			ui.Frame()
			ui.Advance(300 * time.Millisecond)
			after, _ := ui.Find("消息编辑器")
			if before != after {
				t.Fatal("first Git response changes already known layout", before, after)
			}
		})
	}
}

func TestPreviouslyUnknownBranchExpandsWithoutAHeightJump(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	before, _ := ui.Find("消息编辑器")
	m.GitStatuses["session:s"] = domain.GitStatus{Applicable: true, Branch: "main"}
	ui.Frame()
	first, _ := ui.Find("消息编辑器")
	ui.Advance(80 * time.Millisecond)
	mid, _ := ui.Find("消息编辑器")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("消息编辑器")
	if first.H != before.H || mid.H <= before.H || mid.H >= end.H {
		t.Fatal("new Git row snaps instead of expanding", before, first, mid, end)
	}
}

func TestEmptyAttentionIsDisabledAndHasImmediateHoverHint(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	r, _ := ui.Find("待我处理")
	ui.Click("待我处理")
	ui.Advance(300 * time.Millisecond)
	if v.Model.Error != "" {
		t.Fatal("empty attention writes a notice below the composer", v.Model.Error)
	}
	ui.Move(r.X+r.W/2, r.Y+r.H/2)
	ui.Frame()
	if !ui.HasText("无待处理会话") {
		t.Fatal("empty attention hover waits for a tooltip delay", ui.Texts())
	}
}
