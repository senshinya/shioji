package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestCompactDialogLeadingActionsAlignVisibleText(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, zoom := range []float32{1, 1.5} {
			for _, kind := range []string{"worker", "rename"} {
				t.Run(fmt.Sprintf("%s-dark-%t-zoom-%g", kind, dark, zoom), func(t *testing.T) {
					var ui *n.Tester
					var title, action string
					var checkClick func()
					switch kind {
					case "worker":
						v, _, controller := upgradeUIFixture(t)
						v.Model.Preferences.InterfaceZoom = zoom
						state := v.Model.DeviceStates["air"]
						v.beginWorkerUpdateConfirmation("air", state, true)
						ui = n.NewTester(func(c *n.Context) {
							v.workerUpgradeConfirmation(c, c.Theme(), "air", state)
						}, 850, 600)
						ui.SetZoom(zoom)
						title, action = "正在等待 MacBook Air 空闲", "取消执行服务更新"
						checkClick = func() {
							select {
							case job := <-controller.Jobs:
								if job.Method != "$worker.update.cancel" || job.ConnectionID != "air" {
									t.Fatal(job)
								}
							default:
								t.Fatal("the leading edge did not accept the cancellation click")
							}
						}
					case "rename":
						m := layoutModel()
						m.WorkerMinor = 50
						m.Preferences.InterfaceZoom = zoom
						session := m.Snapshot.Sessions[0]
						session.Title = "其他客户端的名称"
						m.Snapshot.Sessions[0] = session
						v := New(m, app.NewController(""), "")
						v.rename = &sessionRename{Session: session, Title: "修改中的名称", Connection: "local", Open: true, Error: "会话名称已更改，请重新载入。"}
						ui = n.NewTester(v.View, 850, 600)
						title, action = "重命名会话", "重新载入会话名称"
						checkClick = func() {
							if v.rename.Title != session.Title || v.rename.Error != "" {
								t.Fatal("the leading edge did not accept the reload click")
							}
						}
					}
					ui.SetSize(850, 600)
					ui.SetDark(dark)
					ui.SetScale(2)
					ui.Move(0, 0)
					ui.Advance(time.Second)
					saveNativeSkillImage(t, ui, t.Name()[strings.LastIndex(t.Name(), "/")+1:]+".png")
					heading, hasHeading := ui.Find(title)
					button, hasButton := ui.Find(action)
					if !hasHeading || !hasButton {
						t.Fatalf("missing title or leading action in %v", ui.Texts())
					}
					img := ui.Image()
					firstInk := func(box n.Rect) float64 {
						const scale = 2
						y0, y1 := int(box.Y*scale), int((box.Y+box.H)*scale)
						x0 := int(box.X * scale)
						bg := int(img.RGBAAt(x0+2, y0+2).R)
						for x := x0; x < x0+int(24*zoom*scale); x++ {
							for y := y0; y < y1; y++ {
								delta := int(img.RGBAAt(x, y).R) - bg
								if delta < -60 || delta > 60 {
									return float64(x) / scale
								}
							}
						}
						t.Fatalf("no visible text at %v", box)
						return 0
					}
					if delta := (firstInk(button) - firstInk(heading)) / float64(zoom); math.Abs(delta) > 1 {
						t.Errorf("leading action text is %.2f DIP from the title's left edge", delta)
					}
					ui.ClickAt(button.X+2, button.Y+button.H/2)
					checkClick()
				})
			}
		}
	}
}

func TestRenameDialogKeepsCancelBesideSave(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 50
	v := New(m, app.NewController(""), "")
	v.rename = &sessionRename{Session: m.Snapshot.Sessions[0], Title: "检查界面对齐", Connection: "local", Open: true}
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)

	cancel, hasCancel := ui.Find("取消重命名")
	save, hasSave := ui.Find("保存名称")
	input, hasInput := ui.Find("新的会话名称")
	gap := save.X - cancel.X - cancel.W
	if !hasCancel || !hasSave || !hasInput || math.Abs(float64(gap-8)) > 1 || math.Abs(float64(save.X+save.W-input.X-input.W)) > 1 {
		t.Fatalf("dialog actions must share the field's trailing edge with an 8 DIP gap: cancel=%v save=%v input=%v", cancel, save, input)
	}
}

func TestArchiveConfirmationKeepsCancelReachableWithLongErrorAtMaximumZoom(t *testing.T) {
	v, ui := archiveViewFixture()
	v.Model.Preferences.InterfaceZoom = 1.5
	longError := strings.Repeat("设备断开，请在设备详情中检查连接后重试。\n", 24)
	v.archive.deletion = &archiveDeleteUI{
		open:    true,
		batch:   true,
		count:   1,
		session: v.Model.Snapshot.Sessions[0],
		error:   longError,
	}
	ui.SetSize(850, 600)
	ui.Advance(time.Second)

	cancel, ok := ui.Find("取消")
	if !ok || cancel.Y < 0 || cancel.Y+cancel.H > 600 {
		t.Fatalf("confirmation footer leaves the window: cancel=%v", cancel)
	}
	before, ok := ui.Find(longError)
	if !ok {
		t.Fatal("missing scrollable error details")
	}
	ui.Scroll(425, 300, 0, 300)
	ui.Advance(time.Second)
	after, _ := ui.Find(longError)
	footer, _ := ui.Find("取消")
	if after.Y >= before.Y || footer != cancel {
		t.Fatalf("scroll moved the footer or left the error in place: error=%v → %v, cancel=%v → %v", before, after, cancel, footer)
	}
	if err := ui.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if v.archive.deletion.open {
		t.Fatal("visible cancel did not dismiss confirmation")
	}
}
