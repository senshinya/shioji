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
	draft := &diffCommentDraft{Open: true, Text: "国 next.Left", Selection: editor.DiffTextSelection{Fragments: []editor.DiffTextFragment{{Side: editor.DiffNew, StartLine: 1, EndLine: 1}}}}
	for _, message := range []string{"", "保存失败，请重试"} {
		draft.Error = message
		tester := n.NewTester(func(c *n.Context) {
			diffCommentBubbleHeight(c, c.Theme(), draft, true, 420)
			v.diffCommentEditor(c, c.Theme(), state, draft)
		}, 420, 240)
		tester.Advance(time.Second)
		panel, panelOK := tester.Find("代码评论编辑器")
		header, headerOK := tester.Find("file.go:1:1–1:1")
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
	if len(state.Diff.Annotations) != 1 || state.Diff.Annotations[0].Key != ref.Attachment.ID {
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
	if restoring <= 72 || restoring >= 124 {
		t.Fatal("original comment did not restore with a height transition", restoring)
	}
	tester.Advance(time.Second)
	if len(state.Diff.Annotations) != 1 || state.Diff.Annotations[0].Key != ref.Attachment.ID || state.CommentNotes[0].Draft.Text != "原评论" {
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
				tester := n.NewTester(func(c *n.Context) { v.diffBody(c, c.Theme(), state) }, 420, 240)
				tester.SetDark(dark)
				tester.SetScale(scale)
				tester.Advance(time.Second)
				panel, ok := tester.Find("代码评论编辑器")
				if !ok {
					t.Fatal("bubble absent")
				}
				header, ok := tester.Find("file.go:1:1–1:4")
				if !ok {
					t.Fatal("precise reference absent")
				}
				body, ok := tester.Find("代码评论内容")
				if !ok {
					t.Fatal("shared body absent")
				}
				if math.Abs(float64(header.X-body.X)) > 1 || math.Abs(float64(body.Y-header.Y-26)) > 1 {
					t.Fatal("reference/body alignment or gap differs", header, body)
				}
				top, bottom := header.Y-panel.Y, panel.Y+panel.H-body.Y-body.H
				if top < 6 || top > 8 || bottom < 6 || bottom > 8 || math.Abs(float64(top-bottom)) > 1 {
					t.Fatalf("bubble padding differs: top %.1f bottom %.1f", top, bottom)
				}

			})
		}
	}
}
