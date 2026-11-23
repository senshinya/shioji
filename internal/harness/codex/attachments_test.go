package codex

import (
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestHarnessInputsKeepImagesStructuredAndTextAttachmentsSeparate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resource")
	os.WriteFile(path, []byte("package example\n"), 0600)
	inputs, err := attachmentInputs("explain", []AttachmentInput{{Attachment: domain.Attachment{Name: "sample.go", MIME: "text/plain"}, Path: path}, {Attachment: domain.Attachment{Name: "screen.png", MIME: "image/png"}, Path: "/worker/private/image"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 || inputs[0].Text != "explain" || inputs[1].Text != "Attachment: sample.go\n\npackage example\n" || inputs[2].Type != "localImage" || inputs[2].Path != "/worker/private/image" {
		t.Fatal("inputs lost structure", inputs)
	}
}

func TestWorkspaceReferenceCarriesSourceAndCapturedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot")
	if err := os.WriteFile(path, []byte("package captured\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inputs, err := attachmentInputs("check", []AttachmentInput{{Attachment: domain.Attachment{Name: "rotate.go", MIME: "text/plain", SourcePath: "/owning-device/project/rotate.go"}, Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || !strings.Contains(inputs[1].Text, "/owning-device/project/rotate.go") || !strings.Contains(inputs[1].Text, "package captured") {
		t.Fatal("reference source or content missing", inputs)
	}
}

func TestCodeCommentAdapterReceivesOnlySelectedCodeAndBranchMetadata(t *testing.T) {
	comment := domain.CodeCommentContent{Comparison: domain.DiffComparison{Base: domain.DiffEndpoint{Kind: "branch", Name: "main", Commit: strings.Repeat("a", 40)}, Target: domain.DiffEndpoint{Kind: "branch", Name: "feat/rotation", Commit: strings.Repeat("b", 40)}}, Path: "rotation.go", Comment: "先检查空值", Fragments: []domain.CodeSelection{{Side: "target", StartLine: 13, EndLine: 13, StartColumn: 16, EndColumn: 25, Text: "next.Left"}}}
	inputs, err := attachmentInputs("", []AttachmentInput{{Attachment: domain.Attachment{Kind: domain.DiffCommentResource, Name: "rotation.go:13"}, Comment: &comment, ExecutionBranch: "other", Path: "/must-not-open-ui-snapshot"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Type != "text" {
		t.Fatalf("comment input %v", inputs)
	}
	for _, value := range []string{"main", "feat/rotation", "rotation.go:13", "next.Left", "先检查空值", "other", strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		if !strings.Contains(inputs[0].Text, value) {
			t.Fatalf("lost %q in %s", value, inputs[0].Text)
		}
	}
	if strings.Contains(inputs[0].Text, "root.Right =") {
		t.Fatal("selected text was expanded")
	}
}

func TestCodeCommentAdapterUsesExclusiveEndRowForTrailingNewline(t *testing.T) {
	for _, tc := range []struct {
		text, want         string
		endLine, endColumn int
	}{
		{"abc\n", "位置：f.go:1:1–2:1（末端不含）", 1, 1},
		{"abc\r\ndef\r\n", "位置：f.go:1:1–3:1（末端不含）", 2, 1},
		{"国😀", "位置：f.go:1:1–1:3（末端不含）", 1, 3},
	} {
		c := domain.CodeCommentContent{Path: "f.go", Comment: "check", Fragments: []domain.CodeSelection{{Side: "target", StartLine: 1, StartColumn: 1, EndLine: tc.endLine, EndColumn: tc.endColumn, Text: tc.text}}}
		inputs, err := attachmentInputs("", []AttachmentInput{{Comment: &c, ExecutionBranch: "main"}})
		if err != nil || len(inputs) != 1 {
			t.Fatal(inputs, err)
		}
		got := inputs[0].Text
		if !strings.Contains(got, tc.want) {
			t.Fatalf("exclusive coordinate missing: want %q in %s", tc.want, got)
		}
	}
}
