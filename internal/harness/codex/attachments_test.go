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
