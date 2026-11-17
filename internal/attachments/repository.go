// Package attachments stores immutable, workspace-scoped inputs outside user repositories.
package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"sync"
	"unicode/utf8"
)

const ChunkSize = 64 << 10
const StagingWorkspace = "00000000-0000-4000-8000-000000000000"

var ErrUnsupportedType = errors.New("支持 PNG、JPEG、GIF 图片，以及不超过 128 KiB 的 UTF-8 文本文件")

type Repository struct {
	Directory string
	mu        sync.Mutex
}

func New(dir string) *Repository { return &Repository{Directory: dir} }

type Receipt struct {
	Attachment domain.Attachment `json:"attachment"`
	Offset     int64             `json:"offset"`
	Complete   bool              `json:"complete"`
	Data       []byte            `json:"data,omitempty"`
}

func Describe(name string, data []byte) (domain.Attachment, error) {
	var a domain.Attachment
	if len(data) == 0 || len(data) > 8<<20 || !utf8.ValidString(name) || name == "" || len(name) > 256 || strings.ContainsAny(name, "/\\\x00\r\n") {
		return a, errors.New("附件名称无效、文件为空或超过 8 MiB")
	}
	mime := "text/plain"
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return a, errors.New("图片超过 4000 万像素")
		}
		mime = "image/" + format
		if format == "jpg" {
			mime = "image/jpeg"
		}
	} else if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return a, ErrUnsupportedType
	} else if len(data) > 128<<10 {
		return a, errors.New("文本附件不能超过 128 KiB")
	}
	h := sha256.Sum256(data)
	return domain.Attachment{ID: domain.NewID(), Name: name, MIME: mime, Size: int64(len(data)), SHA256: hex.EncodeToString(h[:])}, nil
}
func validID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil
}
func (r *Repository) base(workspace, id string) (string, error) {
	if !validID(workspace) || !validID(id) {
		return "", errors.New("invalid attachment identity")
	}
	return filepath.Join(r.Directory, workspace, id), nil
}
func same(a, b domain.Attachment) bool { return a == b }
func (r *Repository) Put(workspace string, a domain.Attachment, offset int64, data []byte) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Receipt{Attachment: a}
	base, err := r.base(workspace, a.ID)
	if err != nil {
		return out, err
	}
	if err = (domain.Submission{Attachments: []domain.Attachment{a}}).ValidateContent(); err != nil {
		return out, err
	}
	if offset < 0 || len(data) == 0 || len(data) > ChunkSize || offset+int64(len(data)) > a.Size {
		return out, errors.New("invalid attachment chunk")
	}
	if err = os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		return out, err
	}
	meta, err := os.ReadFile(base + ".json")
	if err == nil {
		var old domain.Attachment
		if json.Unmarshal(meta, &old) != nil || !same(old, a) {
			return out, errors.New("attachment identity already used")
		}
	} else if os.IsNotExist(err) {
		if offset != 0 {
			return out, errors.New("attachment upload must start at zero")
		}
		meta, _ = json.Marshal(a)
		if err = os.WriteFile(base+".json", meta, 0600); err != nil {
			return out, err
		}
	} else {
		return out, err
	}
	file := base + ".part"
	if _, err = os.Stat(base); err == nil {
		file = base
		out.Complete = true
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return out, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return out, err
	}
	if offset < stat.Size() {
		previous := make([]byte, len(data))
		_, err = f.ReadAt(previous, offset)
		if err != nil || !bytes.Equal(previous, data) {
			return out, errors.New("attachment retry differs from stored bytes")
		}
	} else if offset != stat.Size() || out.Complete {
		return out, errors.New("attachment chunk is out of order")
	} else {
		if _, err = f.WriteAt(data, offset); err != nil {
			return out, err
		}
	}
	stat, err = f.Stat()
	if err != nil {
		return out, err
	}
	out.Offset = stat.Size()
	if stat.Size() == a.Size && !out.Complete {
		if _, err = f.Seek(0, 0); err != nil {
			return out, err
		}
		content, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
		if err != nil {
			return out, err
		}
		actual, err := describeStoredResource(a, content)
		if err != nil || actual.MIME != a.MIME || actual.SHA256 != a.SHA256 {
			f.Close()
			os.Remove(file)
			return out, errors.New("attachment content or digest does not match")
		}
		if err = f.Sync(); err != nil {
			return out, err
		}
		if err = f.Close(); err != nil {
			return out, err
		}
		if err = os.Rename(file, base); err != nil {
			return out, err
		}
		out.Complete = true
	}
	return out, nil
}
func (r *Repository) Path(workspace string, a domain.Attachment) (string, error) {
	base, err := r.base(workspace, a.ID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(base + ".json")
	if err != nil {
		return "", err
	}
	var stored domain.Attachment
	if json.Unmarshal(data, &stored) != nil || !same(a, stored) {
		return "", errors.New("attachment metadata differs")
	}
	stat, err := os.Stat(base)
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() != a.Size {
		return "", errors.New("attachment is incomplete")
	}
	return base, nil
}
func (r *Repository) Get(workspace, id string, offset int64) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out Receipt
	base, err := r.base(workspace, id)
	if err != nil {
		return out, err
	}
	meta, err := os.ReadFile(base + ".json")
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(meta, &out.Attachment); err != nil {
		return out, err
	}
	path, err := r.Path(workspace, out.Attachment)
	if err != nil {
		return out, err
	}
	if offset < 0 || offset >= out.Attachment.Size {
		return out, errors.New("invalid attachment offset")
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	out.Data = make([]byte, min(int64(ChunkSize), out.Attachment.Size-offset))
	_, err = f.ReadAt(out.Data, offset)
	if err != nil {
		return out, fmt.Errorf("read attachment: %w", err)
	}
	out.Offset = offset + int64(len(out.Data))
	out.Complete = out.Offset == out.Attachment.Size
	return out, nil
}
func (r *Repository) Save(name string, data []byte) (domain.Attachment, error) {
	a, err := Describe(name, data)
	if err != nil {
		return a, err
	}
	return r.save(a, data)
}

func (r *Repository) save(a domain.Attachment, data []byte) (domain.Attachment, error) {
	for offset := 0; offset < len(data); offset += ChunkSize {
		if _, err := r.Put(StagingWorkspace, a, int64(offset), data[offset:min(offset+ChunkSize, len(data))]); err != nil {
			return domain.Attachment{}, err
		}
	}
	return a, nil
}
