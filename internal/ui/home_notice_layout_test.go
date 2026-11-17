package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestHomeNoticeBelowComposerKeepsComposerAnchored(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1440, 950} {
			for _, reduced := range []bool{false, true} {
				t.Run(fmt.Sprintf("dark=%v/width=%d/reduced=%v", dark, width, reduced), func(t *testing.T) {
					m := layoutModel()
					m.SessionID = ""
					m.Drafts["home"] = "保留首页草稿"
					v := New(m, nil, "")
					ui := n.NewTester(v.View, width, 820)
					ui.SetDark(dark)
					ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
					ui.Advance(time.Second)
					if dir := os.Getenv("SHIOJI_HOME_NOTICE_CAPTURE"); dir != "" {
						t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprintf("dark-%v-width-%d-reduced-%v", dark, width, reduced)))
					}
					saveNativeSkillImage(t, ui, "idle.png")
					before, ok := ui.Find("消息编辑器")
					if !ok {
						t.Fatal("missing home composer")
					}
					check := func() {
						t.Helper()
						after, ok := ui.Find("消息编辑器")
						if !ok || before != after {
							t.Fatalf("notice moved the composer: before=%+v after=%+v", before, after)
						}
						if panel, shown := ui.Find("会话状态面板"); shown {
							if panel.Y < after.Y+after.H || math.Abs(float64(panel.X-after.X)) > 1 || math.Abs(float64(panel.W-after.W)) > 1 {
								t.Fatalf("home notice is not below and aligned with composer: notice=%+v composer=%+v", panel, after)
							}
						}
					}
					frame := 0
					advance := func(count int) {
						t.Helper()
						for range count {
							ui.Advance(time.Second / 60)
							check()
							if !reduced {
								saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
							}
							frame++
						}
					}
					m.Connected = false
					ui.Frame()
					ui.Advance(6 * time.Second)
					advance(30)
					saveNativeSkillImage(t, ui, "disconnected.png")
					if !ui.HasText("无法连接执行设备") {
						t.Fatal("missing disconnected notice")
					}
					check()
					for i, label := range []string{"折叠会话状态", "展开会话状态", "折叠会话状态", "展开会话状态"} {
						if err := ui.Click(label); err != nil {
							t.Fatal(err)
						}
						frames := 20
						if i == 1 || i == 2 {
							frames = 6
						}
						advance(frames)
					}
					m.Connected = true
					ui.Frame()
					advance(30)
					if ui.HasText("无法连接执行设备") || m.Drafts["home"] != "保留首页草稿" {
						t.Fatal("reconnection left notice or changed draft")
					}
				})
			}
		}
	}
}
