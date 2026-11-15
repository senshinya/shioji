package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestArchiveMetadataOpensSameHistoryAsTitle(t *testing.T) {
	v, ui := archiveViewFixture()
	r, ok := ui.Find("归档归属：Layout")
	if !ok {
		t.Fatal("metadata missing")
	}
	ui.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	ui.Advance(time.Second)
	if v.archive.open || v.archive.viewing != "s" || !ui.HasText("Aligned reply") {
		t.Fatal("metadata is a dead region instead of opening the row")
	}
}

func TestArchiveWorkspaceReturnRestoresItsSearch(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "old", Name: "旧版项目", Archived: true})
	ui.Frame()
	ui.Click("会话")
	ui.Key(0, n.KeyRight)
	ui.Advance(time.Second)
	ui.Click("搜索归档工作区")
	ui.Type("旧版")
	ui.Frame()
	ui.Click("查看归档工作区：旧版项目")
	ui.Advance(time.Second)
	ui.Click("返回已归档工作区")
	ui.Advance(time.Second)
	if v.archive.query != "旧版" {
		t.Fatalf("workspace return discarded search: %q", v.archive.query)
	}
	ui.Click("工作区")
	ui.Key(0, n.KeyLeft)
	ui.Advance(time.Second)
	ui.Click("搜索标题或工作区")
	ui.Type("Layout")
	ui.Frame()
	ui.Click("会话")
	ui.Key(0, n.KeyRight)
	ui.Advance(time.Second)
	if v.archive.query != "旧版" {
		t.Fatalf("tab switch discarded workspace search: %q", v.archive.query)
	}
}

func TestArchiveSearchCanClearWithoutLosingInputFocus(t *testing.T) {
	v, ui := archiveViewFixture()
	ui.Click("搜索标题或工作区")
	ui.Type("不存在")
	ui.Frame()
	if !ui.HasText("没有匹配的归档内容") {
		t.Fatal("fixture did not filter")
	}
	if err := ui.Click("清除归档搜索"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if v.archive.query != "" || !ui.HasText("1 个会话") {
		t.Fatal("clear did not restore rows")
	}
	ui.Type("Layout")
	ui.Frame()
	if v.archive.query != "Layout" {
		t.Fatal("clear lost search focus")
	}
}

func TestArchivePendingDeleteKeepsDialogAndShowsProgress(t *testing.T) {
	v, ui := archiveViewFixture()
	s := v.Model.Snapshot.Sessions[0]
	v.archive.deletion = &archiveDeleteUI{connection: "local", workspace: v.Model.Snapshot.Workspaces[0], session: s, open: true, busy: true}
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("正在删除…") {
		t.Fatal("pending deletion has no visible progress")
	}
}

func TestArchiveKeyboardReachesIndependentRowActions(t *testing.T) {
	_, ui := archiveViewFixture()
	for i := 0; i < 40 && !ui.Focused("查看归档会话：Layout"); i++ {
		ui.Key(0, n.KeyTab)
		ui.Frame()
	}
	if !ui.Focused("查看归档会话：Layout") {
		t.Fatal("row is not keyboard reachable")
	}
	ui.Key(0, n.KeyTab)
	ui.Frame()
	if !ui.Focused("恢复会话：Layout") {
		t.Fatal("restore is not reachable from row focus")
	}
	ui.Key(0, n.KeyTab)
	ui.Frame()
	if !ui.Focused("彻底删除会话：Layout") {
		t.Fatal("delete is not reachable from restore focus")
	}
}

func TestArchivePendingDeleteCannotBeDismissed(t *testing.T) {
	v, ui := archiveViewFixture()
	v.archive.deletion = &archiveDeleteUI{connection: "local", workspace: v.Model.Snapshot.Workspaces[0], session: v.Model.Snapshot.Sessions[0], open: true, busy: true}
	ui.Frame()
	ui.Advance(time.Second)
	ui.Key(0, n.KeyEscape)
	ui.Frame()
	if !v.archive.deletion.open {
		t.Fatal("Escape concealed a deletion still in progress")
	}
}

func TestArchiveLongWorkspaceHeadingKeepsRetentionInsideWindow(t *testing.T) {
	v, _ := archiveViewFixture()
	v.Model.Snapshot.Workspaces[0].Archived = true
	v.Model.Snapshot.Workspaces[0].Name = strings.Repeat("很长的工作区名", 25)
	v.archive.scope = "w"
	ui := n.NewTester(v.View, 820, 600)
	ui.Advance(time.Second)
	r, ok := ui.Find("归档保留设置")
	if !ok || r.X < 224 || r.X+r.W > 820 || r.W < 70 {
		t.Fatal("long title displaced retention control", r)
	}
}

func TestArchiveWorkspaceHeadingUsesOneTextBaseline(t *testing.T) {
	v, ui := archiveViewFixture()
	w := &v.Model.Snapshot.Workspaces[0]
	w.Name = "Archived workspace"
	w.Archived = true
	v.archive.scope = w.ID
	ui.Frame()
	ui.Advance(time.Second)
	title, ok := ui.Find(w.Name)
	if !ok {
		t.Fatal("workspace title missing")
	}
	deviceName := v.Model.DeviceName(w.DeviceID)
	device, ok := ui.Find(deviceName)
	if !ok {
		t.Fatal("device missing")
	}
	baseline := func(r n.Rect, text string, size float32) float32 {
		l := n.ShapeText(text, n.Font{Family: "system-ui", Size: size}, 0).Lines()[0]
		return r.Y + (r.H-l.Ascent-l.Descent)/2 + l.Ascent
	}
	if d := baseline(title, w.Name, 20) - baseline(device, deviceName, 12); math.Abs(float64(d)) > 1 {
		t.Fatal("workspace heading and device do not share a baseline", d, title, device)
	}
}

func TestArchiveSettingsEntryRevealsRetentionWhenScrolled(t *testing.T) {
	v, _ := archiveViewFixture()
	s := NewSettings(v)
	v.OpenSettings = func() {}
	ui := n.NewTester(s.View, 760, 600)
	ui.Advance(time.Second)
	ui.Scroll(650, 400, 0, -1800)
	ui.Advance(time.Second)
	v.archiveSettings()
	ui.Frame()
	ui.Advance(time.Second)
	r, ok := ui.Find("设置归档内容保留")
	if !ok || r.Y < 0 || r.Y+r.H > 600 {
		t.Fatal("archive gear opened a setting outside the visible area", r)
	}
}
