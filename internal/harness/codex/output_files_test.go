package codex

import (
	"bytes"
	"encoding/json"
	"testing"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestToolFileResourcesKeepImmutableContentsAndNames(t *testing.T) {
	for _, block := range []string{
		`{"type":"resource","resource":{"uri":"memory://results/hello.go","mimeType":"text/x-go","text":"package hello\n"}}`,
		`{"type":"resource","resource":{"uri":"memory://results/hello.go","text":"package hello\n"}}`,
		`{"type":"resource","resource":{"uri":"memory://results/hello.go","mimeType":"text/plain","blob":"cGFja2FnZSBoZWxsbwo="}}`,
		`{"type":"resource_link","name":"hello.go","uri":"file:///device/hello.go","mimeType":"text/plain"}`,
	} {
		n := NewNormalizer()
		n.Bind("thread", "session", true)
		repo := attachments.New(t.TempDir())
		n.importResource = func(src harness.OutputResource) (domain.Attachment, error) {
			data := src.Data
			if src.Path != "" {
				if src.Path != "/device/hello.go" {
					t.Fatal(src.Path)
				}
				data = []byte("package hello\n")
			}
			return repo.Save(src.Name, data)
		}
		events, _, err := n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":{"id":"files","type":"mcpToolCall","tool":"write","result":{"content":[` + block + `]}}}}`))
		if err != nil || len(events) != 1 {
			t.Fatal(err, events)
		}
		parts := events[0].Item.Parts
		if len(parts) != 1 || parts[0].Kind != "file" || parts[0].Attachment == nil || parts[0].Attachment.Name != "hello.go" {
			t.Fatal("file resource has no named immutable snapshot", parts)
		}
		path, err := repo.Path(attachments.StagingWorkspace, *parts[0].Attachment)
		if err != nil || path == "" {
			t.Fatal(err)
		}
		journal, _ := json.Marshal(events)
		if bytes.Contains(journal, []byte("package hello")) || bytes.Contains(journal, []byte("cGFja2FnZSBoZWxsbwo=")) {
			t.Fatal("file bytes in journal")
		}
	}
}

func TestToolFileLinksNeverFetchRemoteOrImportBeforeCompletion(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	n.importResource = func(harness.OutputResource) (domain.Attachment, error) {
		t.Fatal("unexpected resource import")
		return domain.Attachment{}, nil
	}
	for _, test := range []struct{ method, block string }{
		{"item/started", `{"type":"resource","resource":{"uri":"memory://results/hello.go","mimeType":"text/plain","text":"package hello"}}`},
		{"item/completed", `{"type":"resource_link","name":"secret.txt","uri":"https://example.invalid/secret.txt","mimeType":"text/plain"}`},
		{"item/completed", `{"type":"resource_link","name":"secret.txt","uri":"file://other-device/private/secret.txt","mimeType":"text/plain"}`},
	} {
		events, _, err := n.Feed([]byte(`{"method":"` + test.method + `","params":{"threadId":"thread","turnId":"turn","item":{"id":"file","type":"mcpToolCall","tool":"write","result":{"content":[` + test.block + `]}}}}`))
		if err != nil || len(events) != 1 {
			t.Fatal(err, events)
		}
		for _, part := range events[0].Item.Parts {
			if part.Attachment != nil {
				t.Fatal("unexpected resource", part)
			}
		}
	}
}

func TestImageResourceLinkDoesNotShiftExistingHistoryImageSlots(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	n.importResource = func(src harness.OutputResource) (domain.Attachment, error) {
		if src.Kind != "image" || src.Index != 0 || src.Path != "" {
			t.Fatal("existing image slot shifted by a previously unimported link", src)
		}
		return domain.Attachment{ID: "saved-inline-image", Name: "original.png"}, nil
	}
	events, _, err := n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":{"id":"file","type":"mcpToolCall","tool":"render","result":{"content":[{"type":"resource_link","name":"linked.png","uri":"file:///device/linked.png","mimeType":"image/png"},{"type":"image","data":"eA=="}]}}}}`))
	if err != nil || len(events) != 1 {
		t.Fatal(err, events)
	}
	parts := events[0].Item.Parts
	if len(parts) != 2 || parts[0].Kind != "resource" || parts[0].Attachment != nil || parts[1].Attachment == nil {
		t.Fatal("history link stole saved image", parts)
	}
}
