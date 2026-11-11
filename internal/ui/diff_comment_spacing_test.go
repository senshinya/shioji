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
				name, _ := tester.Find("file.go")
				if math.Abs(float64(name.X-body.X)) > 1 || math.Abs(float64(body.Y-header.Y-26)) > 1 {
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

func TestDiffCommentVisibleContentEdgesAlignInReadingAndEditing(t *testing.T) {
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
				name, ok := tester.Find("file.go")
				if !ok {
					t.Fatal("reference name absent")
				}
				body, _ := tester.Find("代码评论内容")
				if math.Abs(float64(name.X-body.X)) > 1 {
					t.Fatalf("reference text is inset from body: %.1f vs %.1f", name.X, body.X)
				}
				close, _ := tester.Find("移除评论")
				img := tester.Image()
				right := -1
				// Measure the visible neutral icon, not its larger hit target.
				for y := int(close.Y * scale); y < int((close.Y+close.H)*scale); y++ {
					for x := int(close.X * scale); x < int((close.X+close.W)*scale); x++ {
						p := img.RGBAAt(x, y)
						if (!dark && p.R < 180) || (dark && p.R > 85) {
							right = max(right, x)
						}
					}
				}
				target := body.X + body.W
				if right < 0 || math.Abs(float64(float32(right+1)/scale-target)) > 1 {
					t.Fatalf("remove icon does not meet body right edge: %.1f vs %.1f", float32(right+1)/scale, target)
				}
				tester.Click("编辑评论")
				tester.Advance(time.Second)
				action, _ := tester.Find("提交代码评论")
				if action.X+action.W < target+7 {
					t.Fatal("save button's right padding/radius is clipped", action, target)
				}
				img = tester.Image()
				right = -1
				// The center band excludes the button's rounded corners. Its inverted
				// label must end on the same grid as the reading-state icon.
				for y := int((action.Y + 6) * scale); y < int((action.Y+action.H-6)*scale); y++ {
					for x := int(action.X * scale); x < int((action.X+action.W)*scale); x++ {
						p := img.RGBAAt(x, y)
						if (!dark && p.R > 210) || (dark && p.R < 60) {
							right = max(right, x)
						}
					}
				}
				if right < 0 || math.Abs(float64(float32(right+1)/scale-target)) > 1 {
					t.Fatalf("save text does not meet body right edge: %.1f vs %.1f", float32(right+1)/scale, target)
				}
			})
		}
	}
}
