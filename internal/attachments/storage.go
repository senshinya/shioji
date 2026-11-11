package attachments

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"shioji/internal/domain"
	"strings"
	"time"
)

// Storage serializes with Put/Get. cache is reserved for a dedicated,
// reconstructible download repository, never the mixed original/staging store.
func (r *Repository) Storage(now time.Time, clean, cache bool) (domain.StorageReport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out domain.StorageReport
	info, err := os.Lstat(r.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !info.IsDir() {
		return out, errors.New("附件目录不是独立目录")
	}
	root, err := os.OpenRoot(r.Directory)
	if err != nil {
		return out, err
	}
	defer root.Close()
	return inspectStorage(root, now, clean, cache)
}

func inspectStorage(root *os.Root, now time.Time, clean, cache bool) (domain.StorageReport, error) {
	var out domain.StorageReport
	files := map[string]fs.FileInfo{}
	var order []string
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files[name] = info
			order = append(order, name)
			out.Usage.RetainedBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	for _, name := range order {
		file := files[name]
		parent, leaf := path.Split(name)
		if !validID(strings.TrimSuffix(parent, "/")) {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimSuffix(leaf, ".part"), ".json")
		if !validID(stem) {
			continue
		}
		if cache {
			if leaf != stem && leaf != stem+".part" && leaf != stem+".json" {
				continue
			}
			out.Usage.RetainedBytes -= file.Size()
			out.Usage.PreviewBytes += file.Size()
			if clean {
				removeStorageFile(root, name, file.Size(), &out)
			}
			continue
		}
		if leaf != stem+".part" || now.Sub(file.ModTime()) < 7*24*time.Hour {
			continue
		}
		base := parent + stem
		if _, exists := files[base]; exists {
			continue
		}
		// Only attachment-shaped metadata created by the upload protocol can
		// make a partial upload disposable. Unknown files remain protected.
		metaInfo, exists := files[base+".json"]
		if !exists || metaInfo.Size() > 4096 {
			continue
		}
		data, err := root.ReadFile(base + ".json")
		var a domain.Attachment
		if err != nil || json.Unmarshal(data, &a) != nil || a.ID != stem {
			continue
		}
		// Keep the tiny identity manifest: it binds retries to the same
		// immutable attachment metadata even after expired bytes are removed.
		size := file.Size()
		out.Usage.RetainedBytes -= size
		out.Usage.UploadBytes += size
		if clean {
			removeStorageFile(root, name, file.Size(), &out)
		}
	}
	if clean {
		// Rescan after removals rather than deriving remaining size from a
		// successful command count. A failed file stays visible and retryable.
		remaining, err := inspectStorage(root, now, false, cache)
		if err != nil {
			return out, err
		}
		out.Usage = remaining.Usage
	}
	return out, nil
}

func removeStorageFile(root *os.Root, name string, size int64, out *domain.StorageReport) bool {
	if err := root.Remove(name); err != nil {
		if !errors.Is(err, os.ErrNotExist) && len(out.Failures) < 32 {
			out.Failures = append(out.Failures, domain.StorageFailure{Name: name, Reason: "无法删除文件"})
		}
		return errors.Is(err, os.ErrNotExist)
	}
	out.FreedBytes += size
	return true
}
