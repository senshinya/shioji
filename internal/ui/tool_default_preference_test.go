package ui

import (
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestLatestToolDefaultFoldedOnArrivalAndReentry(t *testing.T) {
	for _, reentry := range []bool{false, true} {
		t.Run(fmt.Sprint("reentry=", reentry), func(t *testing.T) {
			v := toolTailFixture(2)
			v.Model.Connected = true
			v.Model.Preferences.CollapseLatestTools = true
			v.Model.FoldToolHistory["s"] = reentry
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			if ui.HasText("执行工具") || ui.HasText("go test ./internal/ui -run Check0") || !ui.HasText("2 次工具调用") || !ui.HasText("正在执行命令") {
				t.Fatal("latest tools did not start as a single status row", ui.Texts())
			}
			if err := ui.Click("2 次工具调用"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText("执行工具") || !ui.HasText("go test ./internal/ui -run Check0") {
				t.Fatal("default folding blocks manual expansion")
			}
			d := v.Model.Details["s"]
			d.Items = append(d.Items, domain.Item{ID: "more", TurnID: "t", Kind: "tool", Presentation: "command", Name: "next command", Status: "running"})
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			if !ui.HasText("next command") {
				t.Fatal("new call reset explicit expansion")
			}
			d.Items = append(d.Items, domain.Item{ID: "steer", TurnID: "t", Kind: "user", Text: "继续检查", Final: true}, domain.Item{ID: "new-group", TurnID: "t", Kind: "tool", Presentation: "command", Name: "new group command", Status: "running"})
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			if ui.HasText("next command") || ui.HasText("new group command") || !ui.HasText("1 次工具调用") {
				t.Fatal("new group ignored default or prose did not close old group", ui.Texts())
			}
		})
	}
}

func TestToolDefaultPreferenceSettingAndSearch(t *testing.T) {
	v := toolTailFixture(2)
	s := NewSettings(v)
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	if err := ui.Click("自动展开最新工具调用开关"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !v.Model.Preferences.CollapseLatestTools {
		t.Fatal("setting did not disable auto-expansion")
	}
	ui.Click("自动展开最新工具调用开关")
	ui.Advance(time.Second)
	if v.Model.Preferences.CollapseLatestTools {
		t.Fatal("setting cannot re-enable auto-expansion")
	}
	ui.Click("搜索设置")
	ui.Type("工具调用")
	ui.Advance(time.Second)
	if err := ui.Click("外观 · 自动展开最新工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if s.page != "appearance" || s.query != "" || !ui.HasText("自动展开最新工具调用") {
		t.Fatal("search did not reveal the preference")
	}
	saveNativeSkillImage(t, ui, "tool-default-preference-light.png")
}
