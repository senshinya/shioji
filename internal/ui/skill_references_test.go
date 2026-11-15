package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestSkillMentionTokenWithinMessage(t *testing.T) {
	for _, text := range []string{"/review", "\uFFFC /review", "请使用 /review", "first\n/review"} {
		caret := len([]rune(text))
		_, end, query, ok := mentionToken(text, caret, caret)
		if !ok || end != caret || query != "review" {
			t.Errorf("Skill menu unavailable at word boundary: %q (%d, %q, %v)", text, end, query, ok)
		}
	}
	for _, text := range []string{"https://example.com/review", "src/review", "name/review"} {
		caret := len([]rune(text))
		if _, _, _, ok := mentionToken(text, caret, caret); ok {
			t.Errorf("embedded slash opened Skill menu: %q", text)
		}
	}
}

func TestNativeComposerReferencesMultipleSkills(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			v, ui := referenceComposer(t)
			ui.SetDark(dark)
			root := v.Model.Snapshot.Workspaces[0].Folders[0]
			skill := filepath.Join(root, ".agents", "skills", "check-tree")
			if err := os.MkdirAll(skill, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: check-tree\ndescription: Check tree invariants\n---\n\nCheck parent pointers.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			frame := 0
			record := func() {
				if os.Getenv("SHIOJI_SKILL_SNAPSHOTS") == "" {
					return
				}
				for range 15 {
					ui.Advance(time.Second / 30)
					saveNativeSkillImage(t, ui, fmt.Sprintf("dark-%v-frame-%04d.png", dark, frame))
					frame++
				}
			}
			t.Cleanup(func() {
				if t.Failed() {
					saveNativeSkillImage(t, ui, fmt.Sprintf("dark-%v-before.png", dark))
				}
			})
			if err := ui.Click("消息输入"); err != nil {
				t.Fatal(err)
			}
			ui.Type("/review-tree")
			waitNative(t, ui, func() bool { return ui.HasText("review-tree") })
			record()
			ui.Key(0, n.KeyEnter)
			waitNative(t, ui, func() bool { return len(v.Model.DraftSkills["home"]) == 1 })
			record()
			ui.Type("/check-tree")
			record()
			waitNative(t, ui, func() bool { return ui.HasText("check-tree") })
			ui.Key(0, n.KeyTab)
			waitNative(t, ui, func() bool { return len(v.Model.DraftSkills["home"]) == 2 })
			record()
			if !ui.Focused("消息输入") || len(v.Model.Pending) != 0 || v.Model.Drafts["home"] != "  " {
				t.Fatal("second Skill sent input, lost focus or leaked query text", v.Model.Drafts)
			}
			ui.Key(n.Cmd, n.KeyZ)
			ui.Frame()
			if len(v.Model.DraftSkills["home"]) != 1 || v.Model.Drafts["home"] != " /check-tree" {
				t.Fatal("undo lost first Skill or second query", v.Model.Drafts, v.Model.DraftSkills)
			}
			record()
			ui.Key(n.Cmd|n.Shift, n.KeyZ)
			ui.Frame()
			if len(v.Model.DraftSkills["home"]) != 2 {
				t.Fatal("redo failed to restore second Skill")
			}
			ui.Type("检查旋转")
			record()
			saveNativeSkillImage(t, ui, fmt.Sprintf("dark-%v-after.png", dark))
			in, err := v.Model.BeginSubmission()
			if err != nil {
				t.Fatal(err)
			}
			if in.Text != "  检查旋转" || len(in.Skills) != 2 || in.Skills[0].Name != "review-tree" || in.Skills[1].Name != "check-tree" {
				t.Fatal("submission lost structured Skill references", in)
			}
			if len(in.ReferencePositions) != 2 || in.ReferencePositions[0].Position != 1 || in.ReferencePositions[1].Position != 3 {
				t.Fatal("submission moved inline Skill references", in.ReferencePositions)
			}
		})
	}
}
