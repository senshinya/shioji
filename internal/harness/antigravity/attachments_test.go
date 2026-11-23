package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestPromptPreservesOwningDeviceReferences(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snapshot")
	original := filepath.Join(dir, "original.txt")
	binary := filepath.Join(dir, "stored.zip")
	for path, value := range map[string]string{snapshot: "selected snapshot", original: "new contents", binary: "do not transmit ZIP bytes"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := &runtime{}
	r.native.AgentCapabilities.PromptCapabilities.EmbeddedContext = true
	blocks, err := r.promptContent(context.Background(), harness.TurnInput{
		Text: "Compare the data",
		Attachments: []harness.AttachmentInput{
			{Attachment: domain.Attachment{Name: "original.txt", SourcePath: original, MIME: "text/plain"}, Path: snapshot},
			{Attachment: domain.Attachment{Name: "archive.zip", Kind: domain.PathAttachmentResource, MIME: "application/octet-stream"}, Path: binary},
			{Attachment: domain.Attachment{Kind: domain.DiffCommentResource}, Comment: &domain.CodeCommentContent{Path: "file.go", Comment: "Please review this line"}, ExecutionBranch: "main"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 4 || blocks[0].Text != "Compare the data" {
		t.Fatalf("unexpected prompt: %+v", blocks)
	}
	ref := blocks[1].Resource
	if blocks[1].Type != "resource" || ref == nil || !strings.Contains(ref.Text, "selected snapshot") || strings.Contains(ref.Text, "new contents") {
		t.Fatalf("reference did not use frozen snapshot: %+v", ref)
	}
	if !strings.Contains(blocks[2].Text, "archive.zip") || !strings.Contains(blocks[2].Text, binary) || len(blocks[2].Data) != 0 {
		t.Fatalf("binary was not sent as a device path: %+v", blocks[2])
	}
	if !strings.Contains(blocks[3].Text, "file.go") || !strings.Contains(blocks[3].Text, "Please review this line") {
		t.Fatal("comment was not preserved")
	}
}
