package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestHooksNativeAppearanceAndRowAlignment(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := hookUIFixture(t, true)
			ui.SetDark(dark)
			applyHookCatalog(t, v, ui, true)
			// Include mixed-script events, long definitions and different trust labels.
			u := v.hookManagementFor("local", "custom", "w", "")
			u.catalog.Entries = append(u.catalog.Entries, domain.HookEntry{ID: "chinese", DefinitionRevision: "d", Event: "提交前检查", Source: "项目", Scope: domain.HookProject, Kind: "command", Command: "./scripts/check.sh --workspace /very/long/project/path/with/many/segments", Enabled: true, Trust: domain.HookModified, CanTrust: true, CanEnable: true})
			ui.Frame()
			ui.Advance(time.Second)
			switchOne, ok := ui.Find("启停 Hook：PreToolUse")
			switchTwo, _ := ui.Find("启停 Hook：提交前检查")
			arrow, _ := ui.Find("审核 Hook：PreToolUse")
			search, _ := ui.Find("搜索 Hooks")
			refresh, _ := ui.Find("刷新 Hooks")
			if !ok || switchOne.H != 28 || math.Abs(float64(switchOne.X-switchTwo.X)) > 1 || math.Abs(float64(switchOne.Y-arrow.Y)) > 1 {
				t.Fatal("first-line control slots misaligned", switchOne, switchTwo, arrow)
			}
			event, _ := ui.Find("PreToolUse")
			font := n.Font{Family: "system-ui", Size: 13, Weight: 400}
			m := inlineFontMetrics("PreToolUse", font)
			inkCenter := event.Y + (event.H+m.ascent-m.descent)/2 - m.center
			if math.Abs(float64(switchOne.Y+switchOne.H/2-inkCenter)) > 1 {
				t.Fatal("switch misses first-line glyph centre", event, switchOne, inkCenter)
			}
			if search.H != 28 || refresh.H != 28 {
				t.Fatal("toolbar controls changed height", search, refresh)
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-workspace-%v.png", dark))
			if err := ui.Click("查看 Hook：PreToolUse"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-detail-%v.png", dark))
			ui.Click("详细信息")
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-metadata-%v.png", dark))
			trust, _ := ui.Find("信任此版本")
			definition, _ := ui.Find("go test ./...")
			if math.Abs(float64(definition.X+definition.W-refresh.X-refresh.W)) > 1 {
				t.Fatal("definition and toolbar have different right edges", definition, refresh)
			}
			if trust.Y+trust.H > 820 || trust.Y < definition.Y+definition.H+16 {
				t.Fatal("detail footer crowds content", trust, definition)
			}
			ui.Click("返回 Hooks 列表")
			ui.Advance(time.Second)
			ui.Click("在设备设置中管理 Hooks")
			ui.Advance(time.Second)
			applyHookCatalog(t, v, ui, false)
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-global-%v.png", dark))
		})
	}
}

// Opt-in capture of production MyGo rendering, with isolated catalog responses.
func TestHooksNativeInteractionRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_HOOK_RECORDING")
	if dir == "" {
		t.Skip("capture requested only for visual verification")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, ui := hookUIFixture(t, true)
	applyHookCatalog(t, v, ui, true)
	frame := 0
	capture := func(count int) {
		for i := 0; i < count; i++ {
			ui.Advance(100 * time.Millisecond)
			file, err := os.Create(filepath.Join(dir, fmt.Sprintf("frame-%03d.png", frame)))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, ui.Image())
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			frame++
		}
	}
	capture(6)
	ui.Click("查看 Hook：PreToolUse")
	capture(5)
	ui.Click("信任此版本")
	capture(4)
	j := welcomeJob(t, v.Controller, "runtime.hooks.change")
	in := j.Input.(domain.HookRequest)
	receipt, _ := json.Marshal(domain.HookOperation{Input: in, State: domain.OperationConfirmed, Saved: true})
	j.Apply(v.Model, receipt, nil)
	next := hookUICatalog(true)
	next.Entries[1].Trust = domain.HookTrusted
	next.Entries[1].CanTrust = false
	body, _ := json.Marshal(next)
	welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, body, nil)
	capture(6)
	ui.Click("返回 Hooks 列表")
	capture(5)
	ui.Click("在设备设置中管理 Hooks")
	capture(5)
	applyHookCatalog(t, v, ui, false)
	capture(5)
	ui.Click("返回工作区 Hooks")
	capture(5)
}
