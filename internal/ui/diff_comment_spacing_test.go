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

func TestDiffCommentControlEdgesAndPaddingAlignInReadingAndEditing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprint(dark, "/", scale), func(t *testing.T) {
				m := layoutModel()
				m.WorkerMinor = 33
				v := New(m, app.NewController(t.TempDir()), "")
				s := v.changesFor("s")
				s.Path = "file.go"
				s.installDiff(domain.FileVersions{Path: s.Path, New: "next.Left\n", Comparison: s.Comparison, Snapshot: domain.Attachment{ID: "snapshot", SHA256: "sha"}})
				ref, _ := bubbleFixtureReference(v, s, "aligned", "国 next.Left", []domain.CodeSelection{{Side: "target", Start: 0, End: 9, StartLine: 1, EndLine: 1, Text: "next.Left"}})
				m.Attachments[m.DraftKey()] = []app.DraftAttachment{ref}
				tester := n.NewTester(func(c *n.Context) { v.diffBody(c, c.Theme(), s) }, 420, 300)
				tester.SetDark(dark)
				tester.SetScale(scale)
				tester.Advance(time.Second)
				equal := func(label string, a, b float32) {
					t.Helper()
					if math.Abs(float64(a-b)) > 1 {
						t.Fatalf("%s differs: %.1f vs %.1f", label, a, b)
					}
				}
				check := func(editing bool) {
					t.Helper()
					panel, _ := tester.Find("代码评论编辑器")
					badge, ok := tester.Find("file.go:1:1–1:10")
					if !ok {
						t.Fatal("reference absent")
					}
					body, _ := tester.Find("代码评论内容")
					equal("capsule border/body left", badge.X, body.X)
					last, _ := tester.Find("移除评论")
					if editing {
						last, _ = tester.Find("提交代码评论")
					}
					equal("action border/body right", last.X+last.W, body.X+body.W)
					equal("container left/right padding", badge.X-panel.X, panel.X+panel.W-last.X-last.W)
					bottom := last
					if !editing {
						bottom = body
					}
					equal("container top/bottom padding", badge.Y-panel.Y, panel.Y+panel.H-bottom.Y-bottom.H)
					name, _ := tester.Find("file.go")
					position, _ := tester.Find(":1:1–1:10")
					equal("capsule horizontal padding", name.X-badge.X, badge.X+badge.W-position.X-position.W)
					equal("capsule vertical padding", name.Y-badge.Y, badge.Y+badge.H-name.Y-name.H)
					if editing {
						label, _ := tester.Find("保存评论")
						equal("save horizontal padding", label.X-last.X, last.X+last.W-label.X-label.W)
						equal("save vertical padding", label.Y-last.Y, last.Y+last.H-label.Y-label.H)
					}
				}
				check(false)
				tester.Click("编辑评论")
				tester.Advance(time.Second)
				check(true)
			})
		}
	}
}
