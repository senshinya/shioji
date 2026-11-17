package codex

import (
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestPathAttachmentUsesOwningPathWithoutReadingBinary(t *testing.T) {
	inputs, err := attachmentInputs("检查文件", []AttachmentInput{{Attachment: domain.Attachment{Kind: "file-path", Name: "design.pdf", MIME: "application/octet-stream"}, Path: "/owning-device/private/immutable-resource"}})
	if err != nil {
		t.Fatal("path reference rejected", err)
	}
	if len(inputs) != 2 || inputs[1].Type != "text" || !strings.Contains(inputs[1].Text, "design.pdf") || !strings.Contains(inputs[1].Text, "/owning-device/private/immutable-resource") {
		t.Fatal("actual owning path did not reach model input", inputs)
	}
}
