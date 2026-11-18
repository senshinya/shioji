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
	"shioji/internal/protocol"
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
			arrow, _ := ui.Find("查看 Hook 详情：PreToolUse")
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
			back, backOK := ui.Find("返回 Hooks 列表")
			workspaceBack, workspaceBackOK := ui.Find("返回")
			heading, _ := ui.Find("PreToolUse")
			if !backOK || !workspaceBackOK || back.H != workspaceBack.H {
				t.Fatal("Hook return control does not share the settings return geometry", back, workspaceBack)
			}
			frame := ui.Image()
			background := frame.RGBAAt(int(heading.X), int(back.Y)+2)
			arrowBox := func(button n.Rect) n.Rect {
				return n.Rect{X: heading.X, Y: button.Y, W: 12, H: button.H}
			}
			hookArrow := sidebarInkBounds(t, frame, arrowBox(back), 1, background)
			workspaceArrow := sidebarInkBounds(t, frame, arrowBox(workspaceBack), 1, background)
			if math.Abs(float64(hookArrow.Min.X-workspaceArrow.Min.X)) > 1 || math.Abs(float64(hookArrow.Dx()-workspaceArrow.Dx())) > 1 {
				t.Fatal("Hook return arrow does not share the settings return reading edge", hookArrow, workspaceArrow)
			}
			if gap := heading.Y - back.Y - back.H; math.Abs(float64(gap-12)) > 1 {
				t.Fatal("Hook return toolbar must use the settings subpage gap", gap)
			}
			disclosure, disclosureOK := ui.Find("详细信息")
			if !disclosureOK || math.Abs(float64(disclosure.X-heading.X)) > 1 {
				t.Fatal("Hook disclosure label is inset from reading edge", disclosure, heading)
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-detail-%v.png", dark))
			// The top of the hit area is outside the text and chevron ink.
			x, y := int(disclosure.X+disclosure.W/2), int(disclosure.Y+4)
			resting := ui.Image().RGBAAt(x, y)
			ui.Move(disclosure.X+disclosure.W/2, disclosure.Y+disclosure.H/2)
			ui.Advance(time.Second)
			if hover := ui.Image().RGBAAt(x, y); hover != resting {
				t.Fatal("Hook disclosure paints a hover background", resting, hover)
			}
			if err := ui.Click("详细信息"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			expanded, _ := ui.Find("详细信息")
			if !ui.HasText("执行方式") || expanded != disclosure || disclosure.H != 28 {
				t.Fatal("expanding Hook metadata moved or resized the disclosure", disclosure, expanded)
			}
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
			searchEdge, _ := ui.Find("搜索 Hooks")
			eventEdge, _ := ui.Find("Stop")
			if math.Abs(float64(searchEdge.X-eventEdge.X)) > 1 {
				t.Fatal("global search outline lost reading edge", searchEdge, eventEdge)
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-global-%v.png", dark))
		})
	}
}

func TestHooksNativeNarrowZoomKeepsSearchAndEventReadable(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := hookUIFixture(t, true)
			v.Model.DeviceStates["local"].WorkerMinor = protocol.HookBatchMinor
			const zoom = 1.5
			v.Model.Preferences.InterfaceZoom = zoom
			ui.SetSize(850, 600)
			ui.SetDark(dark)
			catalog := hookUICatalog(true)
			project := &catalog.Entries[1]
			project.Event = "UserPromptSubmit"
			project.Command = "node ./scripts/validate.mjs --config ./config/hooks.json"
			project.Matcher = "Bash|Edit|Write"
			body, _ := json.Marshal(catalog)
			welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, body, nil)
			ui.Frame()
			ui.Advance(time.Second)

			search, _ := ui.Find("搜索 Hooks")
			agent, _ := ui.Find("Custom Agent")
			refresh, _ := ui.Find("刷新 Hooks")
			if search.W < 128*zoom-1 || agent.Y < search.Y+search.H || search.Y != refresh.Y {
				t.Fatal("narrow toolbar buries search or moves refresh", search, agent, refresh)
			}
			event, _ := ui.Find(project.Event)
			trust, _ := ui.Find("待审核")
			eventWidth, _ := n.ShapeText(project.Event, n.Font{Family: "system-ui", Size: 13, Weight: 400}, 0).Size()
			if event.W < eventWidth*zoom || trust.Y <= event.Y || !ui.HasText(project.Matcher) || ui.HasText(project.Command) {
				t.Fatal("list needs a full event and compact identifying metadata", event, trust, ui.Texts())
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-narrow-zoom-%v.png", dark))
			ui.SetSize(1240, 820)
			ui.Advance(time.Second)
			wideSearch, _ := ui.Find("搜索 Hooks")
			wideAgent, _ := ui.Find("Custom Agent")
			if wideAgent.Y < wideSearch.Y || wideAgent.Y+wideAgent.H > wideSearch.Y+wideSearch.H {
				t.Fatal("widening did not restore the inline Agent context", wideSearch, wideAgent)
			}
			ui.SetSize(850, 600)
			ui.Advance(time.Second)
			if err := ui.Click("查看 Hook 详情：" + project.Event); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText(project.Command) || !ui.HasText(project.SourcePath) {
				t.Fatal("details lost the exact command or source", ui.Texts())
			}
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
