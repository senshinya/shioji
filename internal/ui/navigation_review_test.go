package ui

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestGlobalSearchFocusesAndRestoresTheComposer(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 850, 600)
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Key(n.Cmd, n.KeyK)
	ui.Advance(time.Second)
	if !ui.Focused("搜索会话") {
		t.Fatal("Cmd+K does not focus the search input")
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(time.Second)
	if v.searchOpen || !ui.Focused("消息输入") {
		t.Fatalf("Escape did not restore the composer: open=%v search focused=%v", v.searchOpen, ui.Focused("搜索会话"))
	}
}

func TestGlobalSearchRejectsPreviousResultsAndRetriesInline(t *testing.T) {
	m := layoutModel()
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 850, 600)
	if err := ui.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("搜索会话"); err != nil {
		t.Fatal(err)
	}
	ui.Type("alpha")
	alpha := welcomeJob(t, v.Controller, "session.search")
	matches := json.RawMessage(`[{"sessionId":"s","workspaceId":"w","title":"Old Alpha"}]`)
	alpha.Apply(m, matches, nil)
	ui.Frame()
	if !ui.HasText("Old Alpha") {
		t.Fatal("missing current result")
	}
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("beta")
	beta := welcomeJob(t, v.Controller, "session.search")
	if ui.HasText("Old Alpha") || !ui.HasText("正在搜索") {
		t.Fatal("waiting query exposes the previous result")
	}
	alpha.Apply(m, matches, nil)
	beta.Apply(m, nil, errors.New("设备离线"))
	ui.Frame()
	if ui.HasText("Old Alpha") || m.Error != "" {
		t.Fatal("failed query leaked a stale result or a global error")
	}
	if err := ui.Click("重试搜索"); err != nil {
		t.Fatal(err)
	}
	retry := welcomeJob(t, v.Controller, "session.search")
	beta.Apply(m, matches, nil)
	retry.Apply(m, json.RawMessage(`[]`), nil)
	ui.Frame()
	if ui.HasText("Old Alpha") || !ui.HasText("没有匹配的会话") {
		t.Fatal("late result displaced the retry")
	}
}

func TestGlobalSearchShowsPartialResultsAndReportsFailedDevicesInline(t *testing.T) {
	m := layoutModel()
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 850, 600)
	ui.Key(n.Cmd, n.KeyK)
	ui.Type("readme")
	job := welcomeJob(t, v.Controller, "session.search")
	result := domain.SearchResult{SessionID: "s", WorkspaceID: "w", Title: "在线结果"}
	job.SearchProgress(m, app.SearchProgress{Results: []domain.SearchResult{result}, Pending: 1})
	ui.Frame()
	if !ui.HasText("在线结果") || !ui.HasText("正在搜索其他设备") {
		t.Fatal("search hid partial results while waiting")
	}
	job.SearchProgress(m, app.SearchProgress{Results: []domain.SearchResult{result}, Failed: 1})
	ui.Frame()
	if !ui.HasText("在线结果") || !ui.HasText("部分设备搜索失败") || !ui.HasText("重试搜索") {
		t.Fatal("partial failure hid successful matches or gave no retry")
	}
}

func TestWelcomeSSHFieldsShareTheNarrowContentEdge(t *testing.T) {
	m := welcomeNetworkModel()
	m.Preferences.InterfaceZoom = 1.5
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 850, 600)
	for _, label := range []string{"通过 SSH 连接", "连接选项"} {
		if err := ui.Click(label); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
	}
	content, hasContent := ui.Find("欢迎内容")
	field, hasField := ui.Find("远程设备名称")
	if !hasContent || !hasField || field.W < 150 || math.Abs(float64(content.X+content.W-field.X-field.W)) > 1 {
		t.Fatalf("expanded SSH field misses the content edge: content=%v field=%v", content, field)
	}
	submit, hasSubmit := ui.Find("连接并准备服务")
	if !hasSubmit || math.Abs(float64(submit.H-48)) > .5 || submit.Y+submit.H > 600 {
		t.Fatalf("short welcome window clips the SSH submit control: %v", submit)
	}
}

func TestCollapsedWorkspaceKeepsItsPendingAttention(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", Title: "等待确认", State: "pending", Kind: "approval"}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 850, 600)
	if err := ui.Click("Example"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !v.collapsedWorkspaces["w"] || !ui.HasText("工作区待处理：w") || ui.HasText("会话等待状态：s") {
		t.Fatal("collapsed workspace has no group-level pending indicator")
	}
}

func TestArchiveSessionWorkspaceHeaderIsLocalized(t *testing.T) {
	_, ui := archiveViewFixture()
	if ui.HasText("Workspace") {
		t.Fatal("archive table still uses an English workspace header")
	}
}
