package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func fileResourceVersion(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%d:%v:%v", st.Dev, st.Ino, info.Size(), info.Mode(), info.ModTime().UnixNano(), st.Ctimespec)))
	return hex.EncodeToString(digest[:])
}
func (s *Server) openFileResource(q protocol.FileResourceQuery) (*os.File, domain.FileResource, error) {
	var meta domain.FileResource
	if q.SessionID == "" || len(q.Path) > 4096 || q.Path == "" || !utf8.ValidString(q.Path) || strings.ContainsAny(q.Path, "\x00\r\n\\") {
		return nil, meta, errors.New("文件链接无效")
	}
	roots, err := s.referenceRoots(protocol.GitQuery{WorkspaceID: q.WorkspaceID, SessionID: q.SessionID})
	if err != nil {
		return nil, meta, err
	}
	path := q.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(roots[0], path)
	}
	path = filepath.Clean(path)
	// Generated documents and media use the same device-local temporary roots as
	// tool images. Root.OpenFile confines symlinks to each permitted directory.
	for _, dir := range []string{"/tmp", os.TempDir()} {
		if !filepath.IsAbs(dir) {
			continue
		}
		roots = append(roots, filepath.Clean(dir))
		if real, e := filepath.EvalSymlinks(dir); e == nil {
			roots = append(roots, real)
		}
	}
	for _, dir := range roots {
		rel, e := filepath.Rel(dir, path)
		if e != nil || rel == "." || !filepath.IsLocal(rel) {
			continue
		}
		root, e := os.OpenRoot(dir)
		if e != nil {
			return nil, meta, e
		}
		f, e := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
		root.Close()
		if e != nil {
			return nil, meta, e
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, meta, errors.New("只能打开普通文件")
		}
		var head [512]byte
		n, e := f.ReadAt(head[:], 0)
		if e != nil && e != io.EOF {
			f.Close()
			return nil, meta, e
		}
		typ := http.DetectContentType(head[:n])
		if ext := mime.TypeByExtension(filepath.Ext(path)); ext != "" && (typ == "application/octet-stream" || strings.HasPrefix(typ, "text/plain")) {
			typ = ext
		}
		typ, _, _ = mime.ParseMediaType(typ)
		meta = domain.FileResource{Path: path, Name: filepath.Base(path), MIME: typ, Size: info.Size(), Version: fileResourceVersion(info)}
		return f, meta, nil
	}
	return nil, meta, errors.New("文件不在本会话的已接纳目录或设备临时目录中")
}
func (s *Server) fileResourceCommand(method string, q protocol.FileResourceQuery) (any, error) {
	file, meta, err := s.openFileResource(q)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if method == "file.resolve" {
		return meta, nil
	}
	if q.Version == "" || q.Version != meta.Version {
		return nil, errors.New("文件已改变，请重新打开")
	}
	if q.Offset < 0 || q.Offset > meta.Size {
		return nil, errors.New("文件读取位置无效")
	}
	data := make([]byte, min(int64(attachments.ChunkSize), meta.Size-q.Offset))
	n, err := file.ReadAt(data, q.Offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || fileResourceVersion(info) != meta.Version || n != len(data) {
		return nil, errors.New("文件在读取期间改变，请重新打开")
	}
	offset := q.Offset + int64(n)
	return domain.FileResourceChunk{Resource: meta, Data: data, Offset: offset, Complete: offset == meta.Size}, nil
}
