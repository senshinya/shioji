package ui

import (
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
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
