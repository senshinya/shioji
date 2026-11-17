package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"slices"
	"strings"
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
	if a.Kind == domain.PathAttachmentResource {
		return DescribePathResource(a.Name, data)
	}
	if a.Kind == domain.DiffCommentResource || a.Kind == domain.DiffSnapshotResource {
		return DescribeDiffResource(a.Kind, a.Name, data)
	}
	return Describe(a.Name, data)
}

// RetainDiffSnapshot follows the existing immutable-input lifecycle: an
// explicitly started comment keeps its source until the user clears originals.
// Passive previews alone do not acquire this retention marker.
func (r *Repository) RetainDiffSnapshot(workspace string, a domain.Attachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a.Kind != domain.DiffSnapshotResource {
		return errors.New("invalid Diff snapshot")
	}
	base, err := r.Path(workspace, a)
	if err != nil {
		return err
	}
	return os.WriteFile(base+".retained", nil, 0600)
}

// PruneDiffSnapshots bounds passive previews without touching explicit inputs.
// Put, retain and pruning share the repository lock; a retained source cannot
// disappear between acceptance of a comment and persistence of its record.
func (r *Repository) PruneDiffSnapshots(workspace string, latest domain.Attachment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	base, err := r.base(workspace, latest.ID)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(base))
	if err != nil {
		return err
	}
	type candidate struct {
		path     string
		size     int64
		modified int64
	}
	var previews []candidate
	protected := map[string]bool{latest.ID: true}
	// Recognize records written before retention markers were introduced too.
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(filepath.Dir(base), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var a domain.Attachment
		if json.Unmarshal(data, &a) != nil {
			continue
		}
		if a.Kind == domain.DiffCommentResource {
			data, err := os.ReadFile(strings.TrimSuffix(path, ".json"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			var record domain.DiffCommentRecord
			if json.Unmarshal(data, &record) != nil {
				return errors.New("invalid stored Diff comment")
			}
			protected[record.Snapshot.ID] = true
		}
	}
	var total int64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(filepath.Dir(base), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var a domain.Attachment
		if json.Unmarshal(data, &a) != nil || a.Kind != domain.DiffSnapshotResource {
			continue
		}
		path = strings.TrimSuffix(path, ".json")
		if protected[a.ID] {
			continue
		}
		if _, err := os.Stat(path + ".retained"); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		previews = append(previews, candidate{path, info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
	}
	slices.SortFunc(previews, func(a, b candidate) int {
		if a.modified < b.modified {
			return -1
		}
		if a.modified > b.modified {
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
	// The newest response is protected above; reserve its slot and bytes here.
	total += latest.Size
	count := len(previews) + 1
	for _, preview := range previews {
		if count <= 64 && total <= 32<<20 {
			break
		}
		if err := os.Remove(preview.path); err != nil {
			return err
		}
		if err := os.Remove(preview.path + ".json"); err != nil {
			return err
		}
		total -= preview.size
		count--
	}
	return nil
}
