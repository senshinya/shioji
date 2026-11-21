package ui

import (
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/portforward"
	"shioji/internal/protocol"
)

func auditCapture(t *testing.T, ui *n.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("SHIOJI_REVIEW_CAPTURE"); dir != "" {
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
		saveNativeSkillImage(t, ui, name+".png")
	}
}

func TestReviewSettingsSaveFailureIsVisibleAtDefaultSize(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(parent, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	v := New(layoutModel(), nil, filepath.Join(parent, "state.json"))
	defer v.Saver.Close()
	settings := NewSettings(v)
	ui := n.NewTester(settings.View, 968, 756)
	ui.SetDark(false)
	if err := ui.Click("外观跟随系统"); err != nil {
		t.Fatal(err)
	}
	if err := v.Saver.FlushLatest(); err == nil {
		t.Fatal("expected a real state write failure")
	}
	ui.Advance(time.Second)
	auditCapture(t, ui, "settings-save")
	r, shown := ui.Find("弹出消息")
	if !shown || r.W == 0 || r.Y+r.H > 756 || !ui.HasText("无法保存设置") {
		t.Fatalf("save failure outside the shared message in the settings window: %v", r)
	}
	if err := v.Flush(); err == nil {
		t.Fatal("expected the same save failure while closing settings")
	} else {
		settings.error = "无法保存设置：" + err.Error()
	}
	ui.Frame()
	count := 0
	for _, text := range ui.Texts() {
		if text == "无法保存设置" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one failure after trying to close settings, got %d", count)
	}
	if err := ui.Click("关闭消息"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText("无法保存设置") {
		t.Fatal("dismissed message reopened without another failure")
	}
}

func TestReviewSettingsSearchRevealsOffscreenControl(t *testing.T) {
	s := NewSettings(New(layoutModel(), nil, ""))
	ui := n.NewTester(s.View, 968, 756)
	ui.SetDark(false)
	if err := ui.Click("搜索设置"); err != nil {
		t.Fatal(err)
	}
	ui.Type("减少动态效果")
	if err := ui.Click("外观 · 减少动态效果"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	auditCapture(t, ui, "settings-search")
	r, shown := ui.Find("减少动态效果")
	if !shown || r.W == 0 || r.Y+r.H > 756 {
		t.Fatalf("matched setting is offscreen: %v", r)
	}
}

func TestReviewWarningInkIsLegibleOnSidebar(t *testing.T) {
	m := layoutModel()
	detail := m.Details["s"]
	detail.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", Kind: "approval", State: "pending", Title: "需要确认"}}
	m.Details["s"] = detail
	v := New(m, nil, "")
	v.collapsedWorkspaces["w"] = true
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	auditCapture(t, ui, "attention-contrast")
	pixels := ui.Image()
	for _, region := range []struct {
		label string
		maxX  float32
		min   float64
	}{
		{"工作区待处理：w", 0, 4.5},
		{"待我处理", 40, 3},
	} {
		r, ok := ui.Find(region.label)
		if !ok {
			t.Fatal(region.label, ui.Texts())
		}
		best := 0.0
		end := r.X + r.W
		if region.maxX != 0 {
			end = min(end, region.maxX)
		}
		for y := int(r.Y); y < int(r.Y+r.H); y++ {
			for x := int(r.X); x < int(end); x++ {
				pixel := pixels.RGBAAt(x, y)
				if pixel.R > pixel.B+20 && pixel.G > pixel.B {
					best = max(best, selectionContrast(pixel, color.RGBA{244, 244, 244, 255}))
				}
			}
		}
		if best < region.min {
			t.Errorf("%s contrast %.2f, need %.1f", region.label, best, region.min)
		}
	}
}

func TestReviewSearchArrowsAndReturnOpenSelectedResult(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = ""
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Key(n.Cmd, n.KeyK)
	ui.Type("candidate")
	job := welcomeJob(t, v.Controller, "session.search")
	job.Apply(m, []byte(`[{"sessionId":"s","workspaceId":"w","title":"First candidate"},{"sessionId":"other","workspaceId":"w","title":"Second candidate"}]`), nil)
	ui.Advance(time.Second)
	ui.Key(0, n.KeyDown)
	ui.Key(0, n.KeyDown)
	ui.Advance(250 * time.Millisecond)
	auditCapture(t, ui, "search-keyboard")
	if !ui.Focused("搜索会话") {
		t.Fatal("arrow navigation took focus out of the search field")
	}
	ui.Key(0, n.KeyEnter)
	if v.searchOpen || m.SessionID != "other" {
		t.Fatalf("return did not open the selected result: open=%v session=%q", v.searchOpen, m.SessionID)
	}
}

func TestReviewEmptyAttentionShortcutShowsFeedbackAndKeepsDraft(t *testing.T) {
	m := layoutModel()
	m.Drafts["session:s"] = "保留当前草稿"
	ui := n.NewTester(New(m, nil, "").View, 1240, 820)
	ui.SetDark(false)
	ui.Key(n.Cmd|n.Shift, n.KeyJ)
	ui.Advance(350 * time.Millisecond)
	auditCapture(t, ui, "attention-empty")
	if !ui.HasText("无待处理会话") || m.SessionID != "s" || m.Drafts["session:s"] != "保留当前草稿" {
		t.Fatal("empty shortcut did not report the result while retaining the session and draft")
	}
	if _, ok := ui.Find("弹出消息"); !ok {
		t.Fatal("empty shortcut did not use the shared feedback overlay")
	}
	ui.Advance(7 * time.Second)
	if err := ui.Click("最近消息"); err != nil {
		t.Fatal(err)
	}
	if !ui.HasText("无待处理会话") {
		t.Fatal("empty shortcut feedback is missing from recent messages")
	}
}

func TestReviewHomeOffersActionsWhenAllWorkspacesAreArchived(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.Snapshot.Workspaces[0].Archived = true
	m.Drafts["home"] = "保留首页草稿"
	ui := n.NewTester(New(m, app.NewController(""), "").View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	auditCapture(t, ui, "home-archived")
	if err := ui.Click("查看已归档"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("已归档") || m.Drafts["home"] != "保留首页草稿" {
		t.Fatal("archive entry did not open the archive while preserving the home draft")
	}
}

func TestReviewCompareFailureUsesOriginalIndicatorAndRetriesFailedPair(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	v := New(m, app.NewController(""), "")
	state := v.changesFor(m.SessionID)
	state.Comparison = domain.DefaultDiffComparison()
	pair := state.Comparison
	pair.Base = domain.DiffEndpoint{Kind: "ref", Name: "release"}
	v.requestComparison(state, pair)
	failed := welcomeJob(t, v.Controller, "git.compare")
	failed.Apply(m, nil, errors.New("无法读取比较基准"))
	ui := n.NewTester(func(c *n.Context) {
		applyAppearance(c, m.Preferences)
		v.inspector(c, c.Theme(), false, 360, 360)
	}, 360, 560)
	ui.SetDark(false)
	ui.Advance(time.Second)
	auditCapture(t, ui, "diff-failure")
	button, ok := ui.Find("重试比较")
	if !ok || button.W != 16 || button.H != 20 || !ui.HasText("!") || !ui.HasText("无法读取比较基准") {
		t.Fatalf("comparison recovery does not use the original indicator and error text: %v", button)
	}
	if err := ui.Click("重试比较"); err != nil {
		t.Fatal(err)
	}
	retry := welcomeJob(t, v.Controller, "git.compare")
	if retry.Input.(protocol.GitQuery).Comparison == nil || *retry.Input.(protocol.GitQuery).Comparison != pair {
		t.Fatalf("retry changed comparison: %+v", retry.Input)
	}
}

func TestReviewFaultedPortForwardCanBeRetriedFromTheRow(t *testing.T) {
	m := deviceUIModel()
	m.PortForwards = []portforward.Mapping{{ID: "mapping", DeviceID: "B", Name: "MacBook Air", Target: portforward.Target{Host: "localhost", Port: 8080}, LocalPort: 8080, State: "unavailable", Error: "connection refused"}}
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	if err := ui.Click("端口转发"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	auditCapture(t, ui, "port-failure")
	if err := ui.Click("重试转发：MacBook Air · 8080"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "$forward.retry")
	if job.Input.(app.ForwardInput).ID != "mapping" {
		t.Fatal("retry selected another mapping")
	}
}

func TestReviewWideInspectorKeepsConversationTitleAndDrafts(t *testing.T) {
	m := parallelModel()
	m.WorkerMinor = 33
	m.SecondarySessionID = "other"
	m.Inspectors["s"], m.Inspectors["other"] = true, true
	m.InspectorWidth = 460
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	auditCapture(t, ui, "parallel-review")
	title, ok := ui.Find("会话名称")
	if !ok || title.W < 180 {
		t.Fatalf("wide inspector collapsed the active heading: %v", title)
	}
	if err := ui.Click("Another session"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if _, visible := ui.Find("右侧会话输入"); !visible || m.Drafts["session:s"] != "left draft" || m.Drafts["session:other"] != "right draft" {
		t.Fatal("switching panes with the inspector open lost the conversation or its draft")
	}
}
