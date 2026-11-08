package worker

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (s *Server) importOutputResource(harnessID, sessionID string, source harness.OutputResource) (domain.Attachment, error) {
	var none domain.Attachment
	if (source.Kind != "image" && source.Kind != "file") || source.SessionID == "" || source.ItemID == "" || source.Index < 0 || source.Index >= 8 || sessionID != "" && source.SessionID != sessionID {
		return none, errors.New("资源不属于当前会话")
	}
	detail, err := s.store.Session(source.SessionID, 0, 1)
	if err != nil {
		return none, err
	}
	session := detail.Session
	if session.HarnessID != harnessID {
		return none, errors.New("资源不属于当前 Agent")
	}
	old, err := s.store.Item(source.SessionID, source.ItemID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	if err == nil {
		index := 0
		for _, part := range old.Parts {
			if part.Kind != source.Kind {
				continue
			}
			if index == source.Index && part.Attachment != nil {
				return *part.Attachment, nil
			}
			index++
		}
	}
	data := source.Data
	name := source.Name
	if source.Path != "" {
		if len(data) != 0 {
			return none, errors.New("资源来源不明确")
		}
		path := source.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(session.Context.CWD, path)
		}
		allowed := false
		roots := append([]string{session.Context.CWD}, session.Context.AdditionalDirectories...)
		valid, e := validateFolders(roots)
		if e != nil || strings.Join(valid, "\x00") != strings.Join(roots, "\x00") {
			return none, errors.New("会话目录身份已改变")
		}
		for _, dir := range roots {
			rel, e := filepath.Rel(dir, path)
			if e != nil || !filepath.IsLocal(rel) || rel == "." {
				continue
			}
			root, e := os.OpenRoot(dir)
			if e != nil {
				return none, e
			}
			file, e := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
			root.Close()
			if e != nil {
				return none, e
			}
			info, e := file.Stat()
			if e == nil && (!info.Mode().IsRegular() || info.Size() > 8<<20) {
				e = errors.New("资源必须是 8 MiB 以内的普通文件")
			}
			if e == nil {
				data, e = io.ReadAll(io.LimitReader(file, (8<<20)+1))
			}
			file.Close()
			if e != nil {
				return none, e
			}
			name = filepath.Base(path)
			allowed = true
			break
		}
		if !allowed {
			return none, errors.New("资源不在本会话的已接纳目录中")
		}
	}
	if name == "" {
		name = "工具结果"
	}
	a, err := attachments.Describe(name, data)
	if err != nil {
		return none, err
	}
	if source.Kind == "image" && !strings.HasPrefix(a.MIME, "image/") {
		return none, errors.New("工具结果不是支持的图片")
	}
	if source.Kind == "file" && a.MIME != "text/plain" {
		return none, errors.New("文件结果仅支持 UTF-8 文本或代码")
	}
	// Content-addressed UUIDv8 identity makes duplicate events/history imports
	// reuse the same bytes without putting a source path in the resource record.
	digest := sha256.Sum256([]byte(session.WorkspaceID + "\x00" + a.Name + "\x00" + a.SHA256))
	digest[6], digest[8] = digest[6]&15|0x80, digest[8]&63|0x80
	a.ID = fmt.Sprintf("%x-%x-%x-%x-%x", digest[:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
	for offset := 0; offset < len(data); offset += attachments.ChunkSize {
		if _, err = s.attachments.Put(session.WorkspaceID, a, int64(offset), data[offset:min(offset+attachments.ChunkSize, len(data))]); err != nil {
			return none, err
		}
	}
	return a, nil
}
