package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

// Ending preedit without a replacement happens when native focus changes or
// the input method cancels its candidate. The next Enter belongs to the app.
func TestComposerEnterAfterCancelledPreedit(t *testing.T) {
	v, ui := nativeQueueFixture()
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("send immediately")
	ui.Compose("中", 1)
	ui.Compose("", 0)
	saveNativeSkillImage(t, ui, "before-enter.png")
	ui.Key(0, n.KeyEnter)
	saveNativeSkillImage(t, ui, "after-enter.png")
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "turn.enqueue" {
			return
		}
	}
	t.Fatalf("Enter did not queue after preedit ended; draft=%q", v.Model.Drafts["session:s"])
}

func TestComposerImmediateEnterAfterReopeningSession(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, preedit := range []string{"none", "cancelled", "committed"} {
			t.Run(fmt.Sprintf("dark=%t/preedit=%s", dark, preedit), func(t *testing.T) {
				v, ui := nativeQueueFixture()
				ui.SetDark(dark)
				v.Model.Drafts["session:s"] = "send immediately"
				v.home()
				ui.Frame()
				if err := ui.Click("Layout"); err != nil {
					t.Fatal(err)
				}
				if err := ui.Click("消息输入"); err != nil {
					t.Fatal(err)
				}
				if preedit != "none" {
					ui.Compose("中", 1)
					if preedit == "cancelled" {
						ui.Compose("", 0)
					} else {
						ui.Type("中")
					}
				}
				ui.Key(0, n.KeyEnter)
				if !composerEnqueued(t, v) {
					t.Fatalf("Enter did not queue after opening session; draft=%q", v.Model.Drafts["session:s"])
				}
			})
		}
	}
}

// Optional video captures production native rendering with deterministic
// catalogs and queued RPCs; it does not drive the installed app or a worker.
func TestComposerEnterNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_ENTER_RECORDING")
	if dir == "" {
		t.Skip("opt-in native Enter recording")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(dark)))
			v, ui := nativeQueueFixture()
			ui.SetDark(dark)
			v.home()
			ui.Frame()
			for frame := range 120 {
				switch frame {
				case 30:
					if err := ui.Click("Layout"); err != nil {
						t.Fatal(err)
					}
				case 31:
					if err := ui.Click("消息输入"); err != nil {
						t.Fatal(err)
					}
					ui.Type("立即发送这条消息")
				case 32:
					ui.Compose("中", 1)
				case 33:
					ui.Compose("", 0)
				case 34:
					ui.Key(0, n.KeyEnter)
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
			if !composerEnqueued(t, v) {
				t.Fatalf("Enter inserted a newline: %q", v.Model.Drafts["session:s"])
			}
		})
	}
}

func composerEnqueued(t *testing.T, v *View) bool {
	t.Helper()
	found := false
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; strings.HasPrefix(job.Method, "turn.") {
			if job.Method != "turn.enqueue" {
				t.Fatal("unexpected dispatch", job.Method)
			}
			if found {
				t.Fatal("Enter enqueued more than once")
			}
			found = true
		}
	}
	return found
}
