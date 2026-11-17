package ui

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Opt-in snapshots render the real native dialog, including text wrapping in
// both appearances, without starting or deleting any user runtime.
func TestArchiveLifecycleDialogSnapshots(t *testing.T) {
	if os.Getenv("SHIOJI_SKILL_SNAPSHOTS") == "" {
		t.Skip("opt-in native capture")
	}
	for _, dark := range []bool{false, true} {
		v, ui := archiveViewFixture()
		v.Model.WorkerMinor = 44
		ui.SetDark(dark)
		if e := ui.Click("删除全部"); e != nil {
			t.Fatal(e)
		}
		ui.Advance(time.Second)
		if !ui.HasText("全部 1 个归档会话将无法恢复") {
			t.Fatal("native delete confirmation missing", ui.Texts())
		}
		saveNativeSkillImage(t, ui, fmt.Sprintf("archive-delete-dark-%v.png", dark))
		if e := ui.Click("取消"); e != nil {
			t.Fatal(e)
		}
		ui.Advance(time.Second)
		if len(v.Model.Snapshot.Sessions) != 1 {
			t.Fatal("cancel mutated archive")
		}
	}
}
