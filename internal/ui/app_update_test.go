package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	n "github.com/egoist/mygo/ui"
)

type updateSessionFixture struct {
	view      updater.Snapshot
	action    updater.Action
	automatic bool
	responses int
}

// Capture the production native renderer through state handoffs; publishing
// and installing a real release remain separate signed-distribution checks.
func TestAppUpdateStateRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_UPDATE_FRAMES")
	if dir == "" {
		t.Skip("native recording")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s := &updateSessionFixture{}
	w := newAppUpdate(layoutModel(), s)
	ui := n.NewTester(w.View, 620, 440)
	ui.SetDark(false)
	frame := 0
	capture := func(seconds int) {
		t.Helper()
		for range seconds * 12 {
			ui.Advance(time.Second / 12)
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("frame-%04d.png", frame)))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, ui.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
			frame++
		}
	}
	s.view = updater.Snapshot{Prompt: 1, Title: "正在检查更新", Message: "正在检查 Shioji 的最新版本。", Bar: true, Progress: -1, Buttons: []updater.Button{{Action: updater.ActionCancel, Label: "取消", Cancel: true}}}
	capture(3)
	s.view = updater.Snapshot{Prompt: 2, Release: true, Title: "Shioji 有新版本可用", Message: "Shioji 0.0.2 已发布，当前版本为 0.0.1。", NotesMarkdown: "## 更新内容\n\n- 新增 App 检查更新\n- 下载完成后可选择稍后重启\n\n正在执行的任务将继续运行。", Checkbox: true, Buttons: []updater.Button{{Action: updater.ActionSkip, Label: "跳过此版本", Aside: true}, {Action: updater.ActionLater, Label: "稍后提醒"}, {Action: updater.ActionInstall, Label: "下载更新", Default: true}}}
	ui.Frame()
	capture(4)
	if err := ui.Click("App 更新：下载更新"); err != nil {
		t.Fatal(err)
	}
	if s.action != updater.ActionInstall {
		t.Fatal("download was not requested")
	}
	for i := 0; i < 5; i++ {
		s.view = updater.Snapshot{Prompt: 3, Title: "正在下载更新", Message: fmt.Sprintf("%d MB / 100 MB", i*25), Bar: true, Progress: float64(i) / 4, Buttons: []updater.Button{{Action: updater.ActionCancel, Label: "取消", Cancel: true}}}
		ui.Frame()
		capture(1)
	}
	s.view = updater.Snapshot{Prompt: 4, Title: "正在安装更新", Message: "正在验证并准备更新包。", Bar: true, Progress: -1, Buttons: []updater.Button{{Action: updater.ActionCancel, Label: "取消", Cancel: true, Disabled: true}}}
	ui.Frame()
	capture(3)
	s.view = updater.Snapshot{Prompt: 5, Title: "更新已准备就绪", Message: "重新启动 Shioji 以使用新版本。", Buttons: []updater.Button{{Action: updater.ActionLater, Label: "稍后", Cancel: true}, {Action: updater.ActionRelaunch, Label: "安装并重启", Default: true}}}
	ui.Frame()
	capture(4)
	ui.Click("App 更新：稍后")
	if s.action != updater.ActionLater {
		t.Fatal("relaunch was forced")
	}
	s.view = updater.Snapshot{Prompt: 6, Title: "无法安装更新", Message: "更新包验证失败。", Detail: "更新包的开发者签名校验失败", Buttons: []updater.Button{{Action: updater.ActionOK, Label: "好", Default: true}}}
	ui.Frame()
	capture(3)
}

func (s *updateSessionFixture) NewWindow(mygo.Content) *mygo.Window          { return nil }
func (s *updateSessionFixture) Current() (updater.Snapshot, <-chan struct{}) { return s.view, nil }
func (s *updateSessionFixture) Done() <-chan struct{}                        { return nil }
func (s *updateSessionFixture) Respond(_ int, a updater.Action, automatic bool) {
	s.action, s.automatic = a, automatic
	s.responses++
}

func TestAppUpdatePromptHandoffAndKeyboard(t *testing.T) {
	session := &updateSessionFixture{view: updater.Snapshot{Prompt: 1, Title: "可下载更新", Buttons: []updater.Button{{Action: updater.ActionInstall, Label: "下载更新", Default: true}}}}
	w := newAppUpdate(layoutModel(), session)
	ui := n.NewTester(w.View, 520, 220)
	ui.Key(0, n.KeyEnter)
	ui.Key(0, n.KeyEnter)
	if session.responses != 1 || session.action != updater.ActionInstall {
		t.Fatal("one prompt accepted multiple actions", session.responses)
	}
	session.view = updater.Snapshot{Prompt: 2, Title: "正在下载更新", Bar: true, Progress: .5, Buttons: []updater.Button{{Action: updater.ActionCancel, Label: "取消", Cancel: true}}}
	ui.Frame()
	ui.Key(0, n.KeyEscape)
	if session.responses != 2 || session.action != updater.ActionCancel {
		t.Fatal("cancel was lost during download handoff")
	}
	session.view = updater.Snapshot{Prompt: 3, Title: "正在安装更新", Buttons: []updater.Button{{Action: updater.ActionCancel, Label: "取消", Cancel: true, Disabled: true}}}
	ui.Frame()
	ui.Key(0, n.KeyEscape)
	if session.responses != 2 {
		t.Fatal("disabled action fired")
	}
}
func (s *updateSessionFixture) Fit(int, int) {}
func (s *updateSessionFixture) Icon() []byte { return nil }
func (s *updateSessionFixture) User() bool   { return true }

// Texts is the remaining session metadata; its public alias is supplied by the plugin.
func (s *updateSessionFixture) Texts() updater.WindowTexts {
	return updater.WindowTexts{AutomaticDownloads: "自动下载更新"}
}

func TestAppUpdateUsesSharedNativeControls(t *testing.T) {
	session := &updateSessionFixture{view: updater.Snapshot{Prompt: 1, Release: true, Title: "Shioji 有新版本可用", Message: "Shioji 0.0.2 已发布，当前版本为 0.0.1。", NotesMarkdown: "## 更新内容\n\n- 新增 App 检查更新\n- 下载完成后可选择稍后重启", Checkbox: true, Buttons: []updater.Button{{Action: updater.ActionSkip, Label: "跳过此版本", Aside: true}, {Action: updater.ActionLater, Label: "稍后提醒"}, {Action: updater.ActionInstall, Label: "下载更新", Default: true}}}}
	w := newAppUpdate(layoutModel(), session)
	ui := n.NewTester(w.View, 620, 440)
	ui.SetDark(false)
	ui.Advance(time.Second)
	if !ui.HasText("更新内容") {
		t.Fatal("release notes did not use native Markdown", ui.Texts())
	}
	r, ok := ui.Find("App 更新：下载更新")
	if !ok || r.H != configurationControlHeight {
		t.Fatal("update action does not share configuration control geometry", r)
	}
	saveNativeSkillImage(t, ui, "native-update-release-light.png")
	if err := ui.Click("下载更新"); err != nil {
		t.Fatal(err)
	}
	if session.action != updater.ActionInstall {
		t.Fatal("update action lost its session")
	}
}
