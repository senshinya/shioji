package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestDiffCommentEditorInsetsAreCompactAndSymmetric(t *testing.T) {
	v := New(layoutModel(), nil, "")
	state := v.changesFor("s")
	state.Path = "file.go"
	draft := &diffCommentDraft{Text: "国 next.Left", Selection: editor.DiffTextSelection{Fragments: []editor.DiffTextFragment{{Side: editor.DiffNew, StartLine: 1, EndLine: 1}}}}
	for _, message := range []string{"", "保存失败，请重试"} {
		draft.Error = message
		tester := n.NewTester(func(c *n.Context) { v.diffCommentEditor(c, c.Theme(), state, draft) }, 420, 240)
		panel, panelOK := tester.Find("代码评论编辑器")
		header, headerOK := tester.Find("file.go:1")
		action, actionOK := tester.Find("取消评论")
		if !panelOK || !headerOK || !actionOK {
			t.Fatal("comment editor contents absent")
		}
		top, bottom := header.Y-panel.Y, panel.Y+panel.H-action.Y-action.H
		if top < 4 || bottom < 4 || top > 8 || bottom > 8 || math.Abs(float64(top-bottom)) > 1 {
			t.Fatalf("editor has loose/asymmetric insets: top %.1f, bottom %.1f; panel %+v, actions %+v", top, bottom, panel, action)
		}
	}
}

func TestEditingSavedDiffCommentReplacesItsRowAndCancelRestoresIt(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	v := New(m, nil, "")
	state := v.changesFor("s")
	state.Path = "file.go"
	snapshot := domain.Attachment{ID: "snapshot", SHA256: "sha"}
	state.installDiff(domain.FileVersions{Path: state.Path, New: "new\n", Comparison: state.Comparison, Snapshot: snapshot})
	ref := app.DraftAttachment{Attachment: domain.Attachment{ID: "comment", Kind: domain.DiffCommentResource}, WorkspaceID: m.WorkspaceID, DeviceID: v.referenceDeviceID(), Ready: true}
	record := domain.DiffCommentRecord{Snapshot: snapshot, Content: domain.CodeCommentContent{Path: state.Path, Comparison: state.Comparison, Comment: "原评论", Fragments: []domain.CodeSelection{{Side: "target", Start: 0, End: 3, StartLine: 1, EndLine: 1}}}}
	m.Attachments[m.DraftKey()] = []app.DraftAttachment{ref}
	v.commentResources = map[string]*diffCommentResource{v.commentResourceKey(ref): {Record: record}}
	tester := n.NewTester(func(c *n.Context) { v.diffBody(c, c.Theme(), state) }, 420, 300)
	tester.Advance(time.Second)
	if err := tester.Click("编辑评论"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(time.Second)
	if len(state.Diff.Annotations) != 1 || state.Diff.Annotations[0].Key != "editor" {
		t.Fatal("saved comment still appears underneath its editor", state.Diff.Annotations)
	}
	state.commentDraft().Text = "未保存修改"
	if err := tester.Click("取消评论"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(80 * time.Millisecond)
	var restoring float32
	for _, a := range state.Diff.Annotations {
		if a.Key == ref.Attachment.ID {
			restoring = a.Height
		}
	}
	if restoring <= 0 || restoring >= 32 {
		t.Fatal("original comment did not restore with a height transition", restoring)
	}
	tester.Advance(time.Second)
	if len(state.Diff.Annotations) != 1 || state.Diff.Annotations[0].Key != ref.Attachment.ID || !tester.HasText("原评论") {
		t.Fatal("cancel did not restore only the immutable original", state.Diff.Annotations)
	}
}

func TestSavedDiffCommentHasBalancedVisibleVerticalInsets(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprint(dark, "/", scale), func(t *testing.T) {
				m := layoutModel()
				m.WorkerMinor = 33
				v := New(m, nil, "")
				state := v.changesFor("s")
				state.Path = "file.go"
				snapshot := domain.Attachment{ID: "snapshot", SHA256: "snapshot-sha"}
				state.installDiff(domain.FileVersions{Path: state.Path, New: "new\n", Comparison: state.Comparison, Snapshot: snapshot})
				ref := app.DraftAttachment{Attachment: domain.Attachment{ID: "comment", Kind: domain.DiffCommentResource}, WorkspaceID: m.WorkspaceID, DeviceID: v.referenceDeviceID(), Ready: true}
				m.Attachments[m.DraftKey()] = []app.DraftAttachment{ref}
				v.commentResources = map[string]*diffCommentResource{v.commentResourceKey(ref): {Record: domain.DiffCommentRecord{Snapshot: snapshot, Content: domain.CodeCommentContent{Path: state.Path, Comparison: state.Comparison, Comment: "国 next.Left", Fragments: []domain.CodeSelection{{Side: "target", Start: 0, End: 3, StartLine: 1, EndLine: 1}}}}}}
				var opticalOffset float32
				tester := n.NewTester(func(c *n.Context) {
					opticalOffset = inlineCenterOffset("国 next.Left", n.Font{Family: c.Theme().Font, Size: 12, Weight: 400})
					v.diffBody(c, c.Theme(), state)
				}, 420, 200)
				tester.SetDark(dark)
				tester.SetScale(scale)
				tester.Advance(time.Second)
				button, ok := tester.Find("编辑评论")
				if !ok {
					t.Fatal("comment absent")
				}
				// The 22-DIP action is centered inside this 32-DIP row. Measure
				// painted text, rather than assuming its line box is its ink.
				top := int((button.Y - 5) * scale)
				bottom := top + int(32*scale) - 1
				image := tester.Image()
				bg := image.RGBAAt(int(4*scale), top+int(2*scale))
				first, last := bottom+1, top-1
				for y := top; y <= bottom; y++ {
					for x := int(32 * scale); x < int((button.X-8)*scale); x++ {
						pixel := image.RGBAAt(x, y)
						if abs(int(pixel.R)-int(bg.R)) > 100 {
							first = min(first, y)
							last = max(last, y)
						}
					}
				}
				if last < first {
					t.Fatal("comment text was not painted")
				}
				above, below := float32(first-top)/scale, float32(bottom-last)/scale
				if math.Abs(float64(above-below)) > 1 {
					t.Fatalf("visible comment insets differ: top %.1f, bottom %.1f DIP; optical correction %.2f; button %+v", above, below, opticalOffset, button)
				}
			})
		}
	}
}
