package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"unicode/utf8"
)

const diffSnapshotMIME = "application/vnd.shioji.diff+json"

// DescribeDiffResource is used for worker-created snapshots and their local
// download cache. The network upload command rejects these internal kinds.
func DescribeDiffResource(kind, name string, data []byte) (domain.Attachment, error) {
	var a domain.Attachment
	if kind != domain.DiffSnapshotResource && kind != domain.DiffCommentResource {
		return a, errors.New("invalid Diff resource kind")
	}
	if !utf8.Valid(data) || !json.Valid(data) || len(data) == 0 {
		return a, errors.New("invalid Diff resource")
	}
	mime := diffSnapshotMIME
	if kind == domain.DiffSnapshotResource {
		var versions domain.FileVersions
		if len(data) > 4<<20 || json.Unmarshal(data, &versions) != nil || len(versions.Old) > 256<<10 || len(versions.New) > 256<<10 {
			return a, errors.New("Diff snapshot exceeds preview limit")
		}
	} else {
		if len(data) > 128<<10 {
			return a, errors.New("评论及选中文字过长")
		}
		mime = "text/plain"
	}
	// Reuse the ordinary descriptor's filename validation without subjecting
	// complete UI-only snapshots to the model text attachment limit.
	a, err := Describe(name, []byte("resource"))
	if err != nil {
		return a, err
	}
	hash := sha256.Sum256(data)
	a.Kind, a.MIME, a.Size, a.SHA256 = kind, mime, int64(len(data)), hex.EncodeToString(hash[:])
	if kind == domain.DiffSnapshotResource {
		// UUID v8: the first 128 digest bits identify this UI-only snapshot.
		// Repository/workspace ownership remains enforced by the resource store.
		id := [16]byte(hash[:16])
		id[6] = id[6]&0x0f | 0x80
		id[8] = id[8]&0x3f | 0x80
		a.ID = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	}
	return a, nil
}
func describeStoredResource(a domain.Attachment, data []byte) (domain.Attachment, error) {
	if a.Kind == domain.DiffCommentResource || a.Kind == domain.DiffSnapshotResource {
		return DescribeDiffResource(a.Kind, a.Name, data)
	}
	return Describe(a.Name, data)
}
