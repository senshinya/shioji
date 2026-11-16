package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Enter through the same navigation path as a sidebar click, from an already
// rendered conversation to a known session whose first history read is pending.
func switchToLoadingSession(dark, reduced bool) (*View, *n.Tester) {
	v, ui := sessionLoadingFixture()
	other := domain.Session{ID: "cached", WorkspaceID: "w", Title: "已有会话"}
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, other)
	v.Model.Details[other.ID] = domain.SessionDetail{Session: other, Items: []domain.Item{{ID: "cached-reply", Kind: "assistant", Text: "之前会话的正文", Final: true}}}
	ui.SetDark(dark)
	ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
	v.navigateSession(other.ID, "w")
	ui.Frame()
	ui.Advance(time.Second)
	v.navigateSession("s", "w")
	ui.Frame()
	return v, ui
}

// Delaying the progress curve must not also delay the first visible skeleton.
func TestSessionLoadingSkeletonStartsWithSessionSwitch(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/reduced=%v", dark, reduced), func(t *testing.T) {
				v, ui := switchToLoadingSession(dark, reduced)
				start := copyFrame(ui)
				ui.Advance(40 * time.Millisecond)
				saveNativeSkillImage(t, ui, fmt.Sprintf("entry-40ms-dark-%v-reduced-%v.png", dark, reduced))
				body, ok := ui.Find("会话正文加载骨架")
				if !ok {
					t.Fatal("switching conversations leaves the body blank before delayed progress")
				}
				if _, ok := ui.Find("发送框加载骨架"); !ok || ui.HasText("正在读取会话") || ui.HasText("之前会话的正文") {
					t.Fatal("skeleton entry lost the composer, flashed progress, or retained another conversation")
				}
				if !reduced {
					area := image.Rect(int(body.X), int(body.Y), int(body.X+body.W), int(body.Y+body.H))
					if differentPixels(start, ui.Image(), area) < 30 || v.sessionLoad.frame <= 0 || v.sessionLoad.frame >= 1 {
						t.Fatal("skeleton entry does not paint an intermediate fade frame")
					}
				}
				ui.Advance(110 * time.Millisecond)
				settled, _ := ui.Find("会话正文加载骨架")
				if ui.HasText("正在读取会话") || v.sessionLoad.frame != 1 || !v.loadingBlocksComposer() {
					t.Fatal("skeleton did not settle before delayed progress, or enabled an unhydrated editor")
				}
				ui.Advance(200 * time.Millisecond)
				withProgress, _ := ui.Find("会话正文加载骨架")
				if settled != body || withProgress != body {
					t.Fatal("delayed progress moved the skeleton", body, settled, withProgress)
				}
				if !ui.HasText("正在读取会话") {
					t.Fatal("stalled read lost its delayed progress feedback")
				}
			})
		}
	}
}

// A reply during either entrance fade must leave the first handoff frame
// unchanged, rather than jumping skeleton/progress opacity to one.
func TestSessionLoadingHandoffContinuesFromEntranceOpacity(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, at := range []time.Duration{40 * time.Millisecond, 240 * time.Millisecond} {
			t.Run(fmt.Sprintf("dark=%v/ready=%v", dark, at), func(t *testing.T) {
				v, ui := switchToLoadingSession(dark, false)
				ui.Advance(at)
				ui.Frame()
				before := copyFrame(ui)
				box, _ := ui.Find("消息编辑器")
				area := image.Rect(int(box.X)+8, int(box.Y)+8, int(box.X+box.W)-8, int(box.Y+box.H)-8)
				body, _ := ui.Find("会话正文加载骨架")
				bodyArea := image.Rect(int(body.X), int(body.Y), int(body.X+body.W), int(body.Y+body.H))
				completeSessionLoading(v)
				ui.Frame()
				if differentPixels(before, ui.Image(), area) != 0 {
					t.Fatal("history arrival jumps composer opacity during its entrance fade")
				}
				if differentPixels(before, ui.Image(), bodyArea) != 0 {
					t.Fatal("history arrival jumps body skeleton opacity during its entrance fade")
				}
				if at < 200*time.Millisecond && ui.HasText("正在读取会话") {
					t.Fatal("a fast reply introduced progress feedback during handoff")
				}
				ui.Advance(time.Second)
				if !ui.HasText("设备返回的真实消息") || ui.HasText("正在读取会话") || v.loadingBlocksComposer() || v.Model.Drafts["session:s"] != "保留的普通草稿" {
					t.Fatal("entrance handoff lost the response, editable draft, or completion")
				}
			})
		}
	}
}

// A fast read must display usable history and input on the next refresh,
// rather than waiting for the slow loader's exit sequence.
func TestSessionLoadingFastReadDoesNotWaitForSkeletonExit(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, at := range []time.Duration{0, 16 * time.Millisecond, 50 * time.Millisecond, 150 * time.Millisecond, 199 * time.Millisecond} {
			t.Run(fmt.Sprintf("dark=%v/ready=%v", dark, at), func(t *testing.T) {
				v, ui := switchToLoadingSession(dark, false)
				ui.Advance(at)
				completeSessionLoading(v)
				ui.Frame()
				ui.Advance(time.Second / 60)
				if v.loadingHistoryOpacity() == 0 || v.loadingBlocksComposer() {
					t.Fatal("fast history waits for the loading exit before showing content or enabling input")
				}
				if ui.HasText("正在读取会话") {
					t.Fatal("a fast read introduced the delayed progress animation")
				}
				ui.Click("消息输入")
				ui.Type("，继续")
				if v.Model.Drafts["session:s"] != "保留的普通草稿，继续" {
					t.Fatal("ready input remains blocked by the loading exit")
				}
				ui.Advance(120 * time.Millisecond)
				if !v.sessionLoad.finished || v.loadingHistoryOpacity() != 1 {
					t.Fatal("fast history still runs the slow loading handoff")
				}
			})
		}
	}
}

func TestSessionLoadingEntryNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_SESSION_LOADING_ENTRY_CAPTURE")
	if dir == "" {
		t.Skip("opt-in production native loading-entry recording")
	}
	for _, dark := range []bool{false, true} {
		for _, readyFrame := range []int{0, 3, 15, 60} {
			t.Run(fmt.Sprintf("dark=%v/ready=%d", dark, readyFrame), func(t *testing.T) {
				t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprintf("dark-%v-ready-%d", dark, readyFrame)))
				v, ui := switchToLoadingSession(dark, false)
				v.navigateSession("cached", "w")
				ui.Frame()
				ui.Advance(time.Second)
				for frame := 0; frame < 150; frame++ {
					if frame == 15 {
						v.navigateSession("s", "w")
						ui.Frame()
					}
					if frame == 15+readyFrame {
						completeSessionLoading(v)
						ui.Frame()
					}
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
					ui.Advance(time.Second / 60)
				}
			})
		}
	}
}
