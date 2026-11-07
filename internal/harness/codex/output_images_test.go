package codex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestToolImagesBecomeImmutableReferencesWithoutWireBytes(t *testing.T) {
	var pngData bytes.Buffer
	png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	encoded := base64.StdEncoding.EncodeToString(pngData.Bytes())
	for _, source := range []string{
		`{"id":"image","type":"imageView","path":"/device/result.png"}`,
		`{"id":"image","type":"imageGeneration","savedPath":"/device/result.png"}`,
		`{"id":"image","type":"imageGeneration","savedPath":null,"result":"` + encoded + `"}`,
		`{"id":"image","type":"mcpToolCall","tool":"render","result":{"content":[{"type":"image","mimeType":"image/png","data":"` + encoded + `"}]}}`,
		`{"id":"image","type":"dynamicToolCall","tool":"render","contentItems":[{"type":"image","imageUrl":"data:image/png;base64,` + encoded + `"}]}`,
		`{"id":"image","type":"mcpToolCall","tool":"render","result":{"content":[{"type":"resource","resource":{"uri":"memory://image","mimeType":"image/png","blob":"` + encoded + `"}}]}}`,
	} {
		n := NewNormalizer()
		n.Bind("thread", "session", true)
		repo := attachments.New(t.TempDir())
		n.importImage = func(image harness.OutputImage) (domain.Attachment, error) {
			if image.SessionID != "session" {
				t.Fatal("image lost owning session", image.SessionID)
			}
			if image.Path != "" {
				if image.Path != "/device/result.png" {
					t.Fatal("wrong device path", image.Path)
				}
				image.Data = pngData.Bytes()
			} else if !bytes.Equal(image.Data, pngData.Bytes()) {
				t.Fatal("inline image was not decoded")
			}
			return repo.Save("result.png", image.Data)
		}
		events, _, err := n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":` + source + `}}`))
		if err != nil || len(events) != 1 {
			t.Fatal(err, events)
		}
		found := false
		for _, part := range events[0].Item.Parts {
			if part.Kind == "image" && part.Attachment != nil {
				found = true
				if _, err = repo.Path(attachments.StagingWorkspace, *part.Attachment); err != nil {
					t.Fatal("image was not saved", err)
				}
			}
		}
		if !found {
			t.Fatal("tool image has no immutable resource", events[0].Item)
		}
		journal, _ := json.Marshal(events)
		if bytes.Contains(journal, []byte(encoded)) || bytes.Contains(journal, []byte("data:image/")) {
			t.Fatal("wire image bytes leaked into transcript")
		}
	}
}

func TestUnsupportedImageLinkKeepsFollowingImageSlotAndRejectsMalformedBytes(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	n.importImage = func(image harness.OutputImage) (domain.Attachment, error) {
		if image.ItemID != "turn/image" || image.Index != 1 {
			t.Fatal("image slots no longer match persisted parts", image.ItemID, image.Index)
		}
		return domain.Attachment{ID: "saved", Name: "result.png", MIME: "image/png"}, nil
	}
	events, _, err := n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":{"id":"image","type":"mcpToolCall","tool":"render","result":{"content":[{"type":"image","url":"https://example.invalid/image.png"},{"type":"image","data":"eA=="},{"type":"image","data":"!not-base64!"}]}}}}`))
	if err != nil || len(events) != 1 {
		t.Fatal(err, events)
	}
	parts := events[0].Item.Parts
	if len(parts) != 3 || parts[0].Attachment != nil || parts[0].Text != "https://example.invalid/image.png" || parts[1].Attachment == nil || parts[2].Attachment != nil {
		t.Fatal("link or invalid bytes became a usable preview", parts)
	}
}
