package ui

import (
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func hoverTextCount(t *testing.T, ui *n.Tester, label, text string) int {
	t.Helper()
	r, ok := ui.Find(label)
	if !ok {
		t.Fatal("missing hover target", label, ui.Texts())
	}
	ui.Move(r.X+r.W/2, r.Y+r.H/2)
	ui.Advance(time.Second)
	count := 0
	for _, got := range ui.Texts() {
		if got == text {
			count++
		}
	}
	return count
}

// A label can fit after resizing without changing its identity or pointer.
// Regressions here either repeat readable values or hide a clipped value.
func TestChoiceTooltipOnlyWhileValueIsClipped(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, value := range []string{"浏览器授权", "English model", "潮路 Shioji " + strings.Repeat("很长的设备名称 ", 6)} {
			t.Run(value+map[bool]string{false: "/light", true: "/dark"}[dark], func(t *testing.T) {
				width := float32(70)
				ui := n.NewTester(func(c *n.Context) {
					applyAppearance(c, app.ClientPreferences{Appearance: "system"})
					n.Column(c).Fill().Padding(60).Children(func() {
						configurationChoiceField(c, "选择配置", value, false).Width(width)
					})
				}, 1000, 300)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if got := hoverTextCount(t, ui, value, value); got != 2 {
					t.Fatal("clipped value lost its full tooltip", got, ui.Texts())
				}
				width = 850
				ui.Frame()
				ui.Advance(time.Second)
				if got := hoverTextCount(t, ui, value, value); got != 1 {
					t.Fatal("readable value still has a duplicate tooltip after resizing", got, ui.Texts())
				}
			})
		}
	}
}

func TestActivityTooltipOnlyWhenHeadingIsClipped(t *testing.T) {
	for _, long := range []bool{false, true} {
		v := New(layoutModel(), nil, "")
		name := "读取文件"
		if long {
			name += strings.Repeat(" /workspace/source/very-long-component.go", 12)
		}
		ui := n.NewTester(func(c *n.Context) {
			theme := applyAppearance(c, app.ClientPreferences{Appearance: "light"})
			n.Column(c).Fill().Padding(60).Children(func() {
				v.activityItem(c, theme, domain.Item{ID: "activity", Kind: "tool", Name: name, Status: "completed"})
			})
		}, 500, 300)
		ui.Advance(time.Second)
		want := 0
		for _, text := range ui.Texts() {
			if text == name {
				want++
			}
		}
		if long {
			want++
		}
		if got := hoverTextCount(t, ui, name, name); got != want {
			t.Fatal("activity tooltip does not follow two-line clipping", long, got, ui.Texts())
		}
	}
}

func TestBackgroundTooltipOnlyExplainsOtherwiseHiddenReason(t *testing.T) {
	for _, state := range []string{"enabled", "unavailable"} {
		m := deviceUIModel()
		s := m.DeviceStates["local"]
		message := "此设备无法注册登录服务"
		s.WorkerService = &app.WorkerService{Background: &domain.BackgroundService{State: state, Message: message}}
		v := New(m, app.NewController(""), "")
		ui := n.NewTester(func(c *n.Context) {
			theme := applyAppearance(c, app.ClientPreferences{Appearance: "light"})
			n.Column(c).Fill().Padding(60).Children(func() { v.backgroundSwitch(c, theme, "local", s) })
		}, 900, 300)
		ui.Advance(time.Second)
		before := 0
		for _, text := range ui.Texts() {
			if text == message {
				before++
			}
		}
		got := hoverTextCount(t, ui, "登录后启动服务开关", message)
		if state == "enabled" && got != before {
			t.Fatal("background tooltip repeats visible details", ui.Texts())
		}
		if state == "unavailable" && got != before+1 {
			t.Fatal("disabled background service lost its hidden reason", ui.Texts())
		}
	}
}

func TestUserBareURLDoesNotRepeatItselfOnHover(t *testing.T) {
	v := New(layoutModel(), nil, "")
	url := "http://localhost:3000"
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(60).Children(func() { v.userMessageLinks(c, url) })
	}, 500, 240)
	ui.Advance(time.Second)
	if got := hoverTextCount(t, ui, url, url); got != 1 {
		t.Fatal("bare URL repeats its visible address", ui.Texts())
	}
}

func TestClippedFileNameTooltipRemainsAvailableFromKeyboard(t *testing.T) {
	v := New(layoutModel(), nil, "")
	name := strings.Repeat("长文件名称-", 15) + ".txt"
	ui := n.NewTester(func(c *n.Context) {
		theme := applyAppearance(c, app.ClientPreferences{Appearance: "light"})
		n.Column(c).Fill().Padding(60).Children(func() {
			v.outputFile(c, theme, domain.Attachment{ID: "file", Name: name})
		})
	}, 500, 300)
	ui.Advance(time.Second)
	ui.Key(0, n.KeyTab)
	if !ui.Focused("预览文件：" + name) {
		t.Fatal("file action did not receive keyboard focus")
	}
	count := 0
	for _, text := range ui.Texts() {
		if text == name {
			count++
		}
	}
	if count != 2 {
		t.Fatal("keyboard focus cannot reveal clipped file name", ui.Texts())
	}
}
