package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

var linkedFileCacheMu sync.RWMutex

type LinkedFile struct {
	Resource domain.FileResource `json:"resource"`
	Path     string              `json:"-"`
	SHA256   string              `json:"sha256"`
}

func hashLinkedFile(ctx context.Context, path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	r := io.LimitReader(f, size+1)
	var n int64
	buffer := make([]byte, 64<<10)
	for {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		count, e := r.Read(buffer)
		n += int64(count)
		h.Write(buffer[:count])
		if e == io.EOF {
			break
		}
		if e != nil {
			err = e
			break
		}
	}
	if err != nil {
		return "", err
	}
	if n != size {
		return "", errors.New("文件副本大小不匹配")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FetchFile always resolves on the authenticated owning worker first. No remote
// path is interpreted or opened on the controlling Mac. Download chunks use an
// independent connection and bounded memory, separate from model attachments.
func (t AttachmentTarget) FetchFile(ctx context.Context, directory, session, path string, progress func(domain.FileResource, int64)) (LinkedFile, error) {
	var result LinkedFile
	if err := ctx.Err(); err != nil {
		return result, err
	}
	client, err := t.connect(ctx)
	if err != nil {
		return result, err
	}
	defer client.Close()
	if client.Minor < 35 {
		return result, errors.New("请先更新此设备的执行服务以打开文件链接")
	}
	call := func(method string, q protocol.FileResourceQuery, out any) error {
		next, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return client.Call(next, method, q, out)
	}
	q := protocol.FileResourceQuery{WorkspaceID: t.WorkspaceID, SessionID: session, Path: path}
	var meta domain.FileResource
	if err = call("file.resolve", q, &meta); err != nil {
		return result, err
	}
	if !filepath.IsAbs(meta.Path) || meta.Version == "" || len(meta.Version) > 256 || meta.Size < 0 || meta.Name == "" || meta.Name == "." || meta.Name == ".." || len(meta.Name) > 256 || !utf8.ValidString(meta.Name) || strings.ContainsAny(meta.Name, "/\\\x00\r\n") {
		return result, errors.New("设备返回了无效的文件信息")
	}
	if progress != nil {
		progress(meta, 0)
	}
	identity := sha256.Sum256([]byte(t.DeviceID + "\x00" + t.WorkspaceID + "\x00" + session + "\x00" + meta.Path + "\x00" + meta.Version))
	linkedFileCacheMu.RLock()
	defer linkedFileCacheMu.RUnlock()
	folder := filepath.Join(directory, hex.EncodeToString(identity[:]))
	result = LinkedFile{Resource: meta, Path: filepath.Join(folder, meta.Name)}
	manifest := filepath.Join(folder, ".resource.json")
	if result.Path == manifest {
		result.Path = filepath.Join(folder, "file-"+meta.Name)
	}
	if data, e := os.ReadFile(manifest); e == nil {
		var saved LinkedFile
		if json.Unmarshal(data, &saved) == nil && saved.Resource == meta {
			if hash, e := hashLinkedFile(ctx, result.Path, meta.Size); e == nil && hash == saved.SHA256 {
				result.SHA256 = hash
				if progress != nil {
					progress(meta, meta.Size)
				}
				return result, nil
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = os.MkdirAll(folder, 0700); err != nil {
		return result, err
	}
	f, err := os.CreateTemp(folder, ".download-*.part")
	if err != nil {
		return result, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	q.Path, q.Version = meta.Path, meta.Version
	for q.Offset < meta.Size {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		var chunk domain.FileResourceChunk
		if err = call("file.read", q, &chunk); err != nil {
			return result, err
		}
		n := int64(len(chunk.Data))
		if chunk.Resource != meta || n == 0 || n > attachments.ChunkSize || chunk.Offset != q.Offset+n || chunk.Offset > meta.Size || chunk.Complete != (chunk.Offset == meta.Size) {
			return result, errors.New("文件分块回执不一致，请重新下载")
		}
		if _, err = f.Write(chunk.Data); err != nil {
			return result, err
		}
		h.Write(chunk.Data)
		q.Offset = chunk.Offset
		if progress != nil {
			progress(meta, q.Offset)
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = f.Sync(); err != nil {
		return result, err
	}
	if err = f.Close(); err != nil {
		return result, err
	}
	if err = os.Rename(f.Name(), result.Path); err != nil {
		return result, err
	}
	result.SHA256 = hex.EncodeToString(h.Sum(nil))
	encoded, _ := json.Marshal(result)
	if err = os.WriteFile(manifest, encoded, 0600); err != nil {
		return result, err
	}
	return result, nil
}

// LinkedFileStorage is the disposable linked-resource portion of client cache.
// Only our hash-named directories are scanned; symlinks are never followed.
func LinkedFileStorage(directory string, clean bool) (domain.StorageReport, error) {
	linkedFileCacheMu.Lock()
	defer linkedFileCacheMu.Unlock()
	var report domain.StorageReport
	root, err := os.OpenRoot(directory)
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer root.Close()
	dirs, err := os.ReadDir(directory)
	if err != nil {
		return report, err
	}
	for _, dir := range dirs {
		if !dir.IsDir() || len(dir.Name()) != 64 {
			continue
		}
		if _, err = hex.DecodeString(dir.Name()); err != nil {
			continue
		}
		entries, e := os.ReadDir(filepath.Join(directory, dir.Name()))
		if e != nil {
			return report, e
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
				continue
			}
			name := filepath.Join(dir.Name(), entry.Name())
			info, e := root.Lstat(name)
			if e != nil || !info.Mode().IsRegular() {
				continue
			}
			if clean {
				if e = root.Remove(name); e == nil {
					report.FreedBytes += info.Size()
					continue
				}
				report.Failures = append(report.Failures, domain.StorageFailure{Name: entry.Name(), Reason: e.Error()})
			}
			report.Usage.PreviewBytes += info.Size()
		}
		if clean {
			root.Remove(dir.Name())
		}
	}
	return report, nil
}
