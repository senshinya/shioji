package domain

import (
	"strings"
	"testing"
)

func TestCommentResourcesHaveIndependentReferenceBudget(t *testing.T) {
	in := Submission{Text: "check"}
	for i := 0; i < 8; i++ {
		in.Attachments = append(in.Attachments, Attachment{ID: NewID(), Name: "file.go:1", Kind: DiffCommentResource, MIME: "text/plain", Size: 10, SHA256: strings.Repeat("a", 64)})
	}
	in.Attachments = append(in.Attachments, Attachment{ID: NewID(), Name: "file.go", MIME: "text/plain", Size: 10, SHA256: strings.Repeat("b", 64)})
	if err := in.ValidateContent(); err != nil {
		t.Fatal("eight comments must not consume the ordinary file budget", err)
	}
	in.Attachments = append(in.Attachments, Attachment{ID: NewID(), Name: "file.go:2", Kind: DiffCommentResource, MIME: "text/plain", Size: 10, SHA256: strings.Repeat("c", 64)})
	if in.ValidateContent() == nil {
		t.Fatal("ninth comment accepted")
	}
}
