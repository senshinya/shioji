package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

// Real MyGo View and menu rendering; the worker receipt is a UI fixture.
// session_reload_live_test.go separately exercises actual processes and hooks.
func TestSessionReloadNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_RELOAD_CAPTURE")
	if dir == "" {
		t.Skip("set SHIOJI_RELOAD_CAPTURE for native menu recording")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
		m := layoutModel()
		m.WorkerMinor = 45
		m.Readiness.Capabilities.Continue = true
		m.Readiness.Capabilities.Reload = true
		s := m.Snapshot.Sessions[0]
		s.Title = "更新插件与后台监听"
		s.ThreadID = "native-fixture-source"
		s.Revision = 7
		m.Snapshot.Sessions[0] = s
		d := m.Details[s.ID]
		d.Session = s
		d.Items = []domain.Item{{ID: "reply", Kind: "assistant", Text: "插件已更新。重新加载会话后，会在原对话中接续新版运行环境。", Final: true}}
		m.Details[s.ID] = d
		m.Drafts["session:s"] = "保留这份未发送的草稿，稍后继续检查。"
		c := app.NewController("")
		v := New(m, c, "")
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		var job app.Job
		for frame := 0; frame < 210; frame++ {
			switch frame {
			case 25:
				if err := ui.RightClick(s.Title); err != nil {
					t.Fatal(err)
				}
			case 45:
				saveNativeSkillImage(t, ui, "menu.png")
			case 65:
				if err := ui.ChooseMenuItem("重新加载"); err != nil {
					t.Fatal(err)
				}
				job = historyJob(t, v, "session.reload")
			case 85:
				ui.RightClick(s.Title)
			case 95:
				saveNativeSkillImage(t, ui, "pending.png")
			case 105:
				ui.Key(0, n.KeyEscape)
			case 125:
				in := job.Input.(domain.SessionReloadRequest)
				b, _ := json.Marshal(domain.SessionReloadResult{Operation: domain.SessionReloadOperation{Input: in, State: "completed"}, Session: s})
				job.Apply(m, b, nil)
			case 150:
				saveNativeSkillImage(t, ui, "complete.png")
			case 170:
				m.Snapshot.Sessions[0].Status = "running"
				d = m.Details[s.ID]
				d.Session.Status = "running"
				m.Details[s.ID] = d
				ui.RightClick(s.Title)
			case 185:
				saveNativeSkillImage(t, ui, "running-disabled.png")
			case 200:
				ui.Key(0, n.KeyEscape)
			}
			ui.Advance(time.Second / 30)
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
		}
		if m.Drafts["session:s"] != "保留这份未发送的草稿，稍后继续检查。" || m.SessionID != "s" {
			t.Fatal("recorded reload changed the draft or selected session")
		}
	}
}
