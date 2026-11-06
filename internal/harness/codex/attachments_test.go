package codex

import (
	"os"
	"path/filepath"
	"shioji/internal/domain"
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
