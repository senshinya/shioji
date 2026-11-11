package ui

import (
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func bubbleFixtureReference(v *View, s *changesState, id, text string, parts []domain.CodeSelection) (app.DraftAttachment, domain.DiffCommentRecord) {
	ref := app.DraftAttachment{Attachment: domain.Attachment{ID: id, Kind: domain.DiffCommentResource}, WorkspaceID: v.Model.WorkspaceID, DeviceID: v.referenceDeviceID(), Ready: true}
	record := domain.DiffCommentRecord{Snapshot: s.Versions.Snapshot, Content: domain.CodeCommentContent{Path: s.Path, Comparison: s.Comparison, Comment: text, Fragments: parts}}
	if v.commentResources == nil {
		v.commentResources = map[string]*diffCommentResource{}
	}
	v.commentResources[v.commentResourceKey(ref)] = &diffCommentResource{Record: record}
	return ref, record
}

func TestDiffCommentBubbleKeepsReferenceAndBodyThroughEditAndCancel(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	v := New(m, nil, "")
	s := v.changesFor("s")
	s.Path = "rotation.go"
	s.installDiff(domain.FileVersions{Path: s.Path, New: "next.Left\n", Snapshot: domain.Attachment{ID: "snapshot", SHA256: "sha"}, Comparison: s.Comparison})
	ref, _ := bubbleFixtureReference(v, s, "comment", "原评论", []domain.CodeSelection{{Side: "target", Start: 0, End: 9, StartLine: 1, EndLine: 1, Text: "next.Left"}})
	m.Attachments[m.DraftKey()] = []app.DraftAttachment{ref}
	tester := n.NewTester(func(c *n.Context) { v.diffBody(c, c.Theme(), s) }, 420, 300)
	tester.Advance(time.Second)
	before, ok := tester.Find("代码评论内容")
	if !ok {
		t.Fatal("reading comment must use the shared text control")
	}
	key := s.Diff.Annotations[0].Key
	if err := tester.Click("编辑评论"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(80 * time.Millisecond)
	mid, _ := tester.Find("代码评论内容")
	if mid.Y != before.Y {
		t.Fatal("editing moved first body line", before, mid)
	}
	if len(s.Diff.Annotations) != 1 || s.Diff.Annotations[0].Key != key {
		t.Fatal("editing replaced the stable annotation", s.Diff.Annotations)
	}
	tester.Advance(time.Second)
	s.commentDraft().Text = "未保存修改"
	tester.Frame()
	if err := tester.Click("取消评论"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(80 * time.Millisecond)
	after, _ := tester.Find("代码评论内容")
	if after.Y != before.Y {
		t.Fatal("cancel moved first body line", before, after)
	}
	tester.Advance(time.Second)
	if s.commentDraft().Closing {
		t.Fatal("closed saved comment still blocks diff refresh")
	}
	if s.CommentNotes[0].Draft.Text != "原评论" {
		t.Fatal("cancel did not restore immutable original")
	}
	if err := tester.Click("编辑评论"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(time.Second)
	if s.commentDraft().Text != "原评论" {
		t.Fatal("cancelled text resurfaced")
	}
	tester.SetPreferences(n.Preferences{ReduceMotion: true})
	tester.Click("取消评论")
	tester.Frame()
	if len(s.Diff.Annotations) != 1 || s.Diff.Annotations[0].Height != 72 {
		t.Fatal("reduced-motion close did not finish immediately", s.Diff.Annotations)
	}
}

func TestDiffCommentLocationPreservesExactUnicodeColumnsAndGaps(t *testing.T) {
	old, next := "old\r\n国😀 tail\r\n", "first\r\n  国😀.Left\r\nend\n"
	cases := []struct {
		path  string
		parts []domain.CodeSelection
		want  string
	}{
		{"internal/rotation.go", []domain.CodeSelection{{Side: "target", Start: 9, End: len("first\r\n  国😀")}}, "rotation.go:2:3–2:5"},
		{"rotation.go", []domain.CodeSelection{{Side: "target", Start: 9, End: len(next) - 1}}, "rotation.go:2:3–3:4"},
		{"rotation.go", []domain.CodeSelection{{Side: "base", Start: 5, End: len("old\r\n国😀")}, {Side: "target", Start: 0, End: 5}}, "rotation.go:2:1–2:3, 1:1–1:6"},
	}
	for _, tc := range cases {
		got := diffCommentLocation(tc.path, tc.parts, domain.FileVersions{Old: old, New: next})
		if got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestDiffCommentFailureMovesActionsWithItsHeightAndKeepsDraft(t *testing.T) {
	v := New(layoutModel(), nil, "")
	s := v.changesFor("s")
	s.Path = "rotation.go"
	d := &diffCommentDraft{UIKey: "failure", Open: true, Text: "保留草稿", Selection: editor.DiffTextSelection{Fragments: []editor.DiffTextFragment{{Side: editor.DiffNew, StartLine: 1, EndLine: 1}}}}
	tester := n.NewTester(func(c *n.Context) {
		diffCommentBubbleHeight(c, c.Theme(), d, true, 420)
		v.diffCommentEditor(c, c.Theme(), s, d)
	}, 420, 260)
	tester.Advance(time.Second)
	before, _ := tester.Find("取消评论")
	d.Error = "保存失败，请重试"
	tester.Frame()
	tester.Advance(80 * time.Millisecond)
	mid, _ := tester.Find("取消评论")
	if mid.Y <= before.Y || mid.Y >= before.Y+24 {
		t.Fatal("error action position jumped instead of following height", before, mid)
	}
	tester.Advance(time.Second)
	if !d.Open || d.Text != "保留草稿" {
		t.Fatal("save failure discarded edit")
	}
}

func TestDiffCommentOverlappingHighlightsAreNotPaintedTwice(t *testing.T) {
	got := diffCommentHighlights([]domain.CodeSelection{{Side: "target", Start: 1, End: 8}, {Side: "target", Start: 4, End: 10}, {Side: "base", Start: 1, End: 8}}, false)
	if len(got) != 2 || got[1].Start != 1 || got[1].End != 10 || got[0].Side != editor.DiffOld {
		t.Fatal("overlapping source highlight blends twice", got)
	}
}

func TestDiffCommentSwitchingEditorsRestoresThePreviousOriginal(t *testing.T) {
	v := New(layoutModel(), nil, "")
	s := v.changesFor("s")
	s.Path = "file.go"
	s.Versions.New = "new"
	first, r1 := bubbleFixtureReference(v, s, "one", "原评论一", nil)
	second, r2 := bubbleFixtureReference(v, s, "two", "原评论二", nil)
	d1, d2 := commentDraftFromRecord(s, first, r1), commentDraftFromRecord(s, second, r2)
	s.CommentNotes = []diffCommentNote{{Ref: first, Record: r1, Draft: d1}, {Ref: second, Record: r2, Draft: d2}}
	v.editDiffComment(s, first, r1)
	d1.Text = "取消的编辑"
	v.editDiffComment(s, second, r2)
	if d1.Open || d1.Text != "原评论一" || !d2.Open || s.commentDraft() != d2 {
		t.Fatal("switching left multiple editors or stale cancelled text", d1, d2)
	}
	d2.Saving = true
	v.editDiffComment(s, first, r1)
	if d1.Open || s.commentDraft() != d2 {
		t.Fatal("editor switched while save acceptance pending")
	}
}

func TestDiffCommentEscapeUsesLiveKeyboardStateWithoutExpiredElement(t *testing.T) {
	v := New(layoutModel(), nil, "")
	s := v.changesFor("s")
	s.Path = "file.go"
	d := &diffCommentDraft{UIKey: "keyboard", Original: "原评论", Text: "修改草稿", Open: true, Focus: true, Editing: domain.Attachment{ID: "saved"}}
	tester := n.NewTester(func(c *n.Context) {
		diffCommentBubbleHeight(c, c.Theme(), d, true, 420)
		v.diffCommentEditor(c, c.Theme(), s, d)
	}, 420, 260)
	tester.Advance(time.Second)
	tester.Compose("国", 1)
	tester.Key(0, n.KeyEscape)
	if !d.Open {
		t.Fatal("Escape cancelled comment during IME composition")
	}
	tester.Type("输入")
	tester.Key(0, n.KeyEscape)
	tester.Advance(time.Second)
	if d.Open || d.Text != "原评论" {
		t.Fatal("Escape did not restore original")
	}
}

func TestDiffCommentInlineTagElidesFilenameAndPreservesLocationAndCopyName(t *testing.T) {
	name := "a-very-long-rotation-file-name-for-review.go:14:3–16:4"
	tester := n.NewTester(func(c *n.Context) {
		display := compactDiffCommentTag(name, n.Font{Family: c.Theme().Font, Size: 12, Weight: 400})
		if !strings.Contains(display, "…") || !strings.HasSuffix(display, ":14:3–16:4") {
			t.Fatal("tag truncated exact location", display)
		}
		v := New(layoutModel(), nil, "")
		a := v.referenceAttachment(c, composerReference{file: app.DraftAttachment{Attachment: domain.Attachment{Kind: domain.DiffCommentResource, Name: name}}})
		if a.Name != name || a.Width > 213 {
			t.Fatal("tag lost full copy label or exceeded compact width", a)
		}
	}, 420, 100)
	tester.Frame()
}

func TestDiffCommentEditingKeepsAllWrappedGlyphs(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	v := New(m, nil, "")
	s := v.changesFor("s")
	s.Path = "file.go"
	s.installDiff(domain.FileVersions{Path: s.Path, New: "next.Left\n", Snapshot: domain.Attachment{ID: "snapshot", SHA256: "sha"}, Comparison: s.Comparison})
	ref, _ := bubbleFixtureReference(v, s, "wrapped", "先判断 next 是否为空，避免读取 next.Left 时崩溃。旋转后还应检查父子指针能否双向对应。", []domain.CodeSelection{{Side: "target", Start: 0, End: 9, StartLine: 1, EndLine: 1, Text: "next.Left"}})
	m.Attachments[m.DraftKey()] = []app.DraftAttachment{ref}
	tester := n.NewTester(func(c *n.Context) { v.diffBody(c, c.Theme(), s) }, 472, 300)
	tester.Advance(time.Second)
	ink := func() int {
		r, _ := tester.Find("代码评论内容")
		img := tester.Image()
		n := 0
		for y := int(r.Y + 21); y < int(r.Y+r.H); y++ {
			for x := int(r.X); x < int(r.X+r.W); x++ {
				p := img.RGBAAt(x, y)
				if p.R < 120 && p.G < 120 && p.B < 120 {
					n++
				}
			}
		}
		return n
	}
	before := ink()
	if before < 20 {
		t.Fatal("fixture did not wrap")
	}
	tester.Click("编辑评论")
	tester.Advance(time.Second)
	if after := ink(); after < before*9/10 {
		t.Fatal("wrapped glyphs disappeared when editing", before, after)
	}
}

func TestDiffCommentHighlightDoesNotReplaceSelectionOrMapStaleSnapshots(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 33
	v := New(m, nil, "")
	s := v.changesFor("s")
	s.Path = "rotation.go"
	snapshot := domain.Attachment{ID: "snapshot", SHA256: "sha"}
	raw := "  next.Left\n"
	s.installDiff(domain.FileVersions{Path: s.Path, New: raw, Snapshot: snapshot, Comparison: s.Comparison})
	ref, record := bubbleFixtureReference(v, s, "comment", "检查引用", []domain.CodeSelection{{Side: "target", Start: 2, End: 11, Text: "next.Left", StartLine: 1, EndLine: 1}})
	m.Attachments[m.DraftKey()] = append(m.Attachments[m.DraftKey()], ref)
	s.Diff.SetTextSelection(editor.DiffTextPoint{Side: editor.DiffNew, Offset: 2}, editor.DiffTextPoint{Side: editor.DiffNew, Offset: 6})
	tester := n.NewTester(func(c *n.Context) { v.diffBody(c, c.Theme(), s) }, 420, 300)
	if len(s.Diff.TextHighlights) != 1 {
		t.Fatal("missing persistent quote highlight", s.Diff.TextHighlights)
	}
	selected, ok := s.Diff.SelectedText()
	if !ok || selected.Text != "next" {
		t.Fatal("comment replaced ordinary selection", selected)
	}
	v.commentResources[v.commentResourceKey(ref)].Record.Snapshot.SHA256 = "stale"
	tester.Frame()
	if len(s.Diff.TextHighlights) != 0 {
		t.Fatal("stale comment mapped onto different code")
	}
	v.commentResources[v.commentResourceKey(ref)].Record = record
	tester.Frame()
	if len(s.Diff.TextHighlights) != 1 {
		t.Fatal("matching snapshot did not restore highlight")
	}
	if strings.Contains(diffCommentLocation(s.Path, record.Content.Fragments, s.Versions), " 行") {
		t.Fatal("imprecise row-only reference")
	}
}
