package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestHistoryEditorEntryDoesNotRewrapInlineMessageForOneFrame(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, references := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%t/references=%t", dark, references), func(t *testing.T) {
				v, ui, item := historyFixture()
				enableReferenceHistory(v)
				item.Text = " 请检查边界条件 "
				if references {
					item.Skills = []domain.SkillReference{{ID: "check", Name: "check"}}
					item.Attachments = []domain.Attachment{{ID: "file", Name: "rotate_test.go", SourcePath: "/project/rotate_test.go", MIME: "text/plain"}}
					item.ReferencePositions = []domain.ReferencePosition{{SkillID: "check", Position: 0}, {AttachmentID: "file", Position: len([]rune(item.Text))}}
				}
				d := v.Model.Details["s"]
				d.Items[0] = item
				v.Model.Details["s"] = d
				ui.SetDark(dark)
				ui.Frame()
				ui.Advance(time.Second)
				label := item.Text
				if references {
					label = "用户消息正文"
				}
				before, ok := ui.Find(label)
				if !ok || before.H > 24.5 {
					t.Fatal("fixture is not a one-line message", before, ok)
				}
				if err := ui.Click("修改并重新开始"); err != nil {
					t.Fatal(err)
				}
				first, ok := ui.Find("编辑历史消息")
				if !ok || math.Abs(float64(first.X-before.X)) > .5 || math.Abs(float64(first.Y-before.Y)) > .5 {
					t.Fatal("opening editor shifts text before its width animation", before, first)
				}
				for frame := 0; frame < 24; frame++ {
					binding := v.composerDocuments[v.Model.HistoryDraftKey()]
					for _, paragraph := range binding.doc.VisibleParagraphs() {
						if paragraph.Height > 24.5 {
							t.Fatalf("one-line message flashes as wrapped text during editor entry, frame %d: %+v", frame, paragraph)
						}
					}
					ui.Advance(time.Second / 60)
				}
			})
		}
	}
}
