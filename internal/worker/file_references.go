package worker

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (s *Server) referenceRoots(q protocol.GitQuery) ([]string, error) {
	roots, _, err := s.referenceScope(q)
	return roots, err
}

func (s *Server) referenceScope(q protocol.GitQuery) ([]string, domain.Session, error) {
	var session domain.Session
	w, err := s.store.Workspace(q.WorkspaceID)
	if err != nil {
		return nil, session, err
	}
	if w.Archived {
		return nil, session, errors.New("请先恢复工作区")
	}
	roots := w.Folders
	if q.SessionID != "" {
		session, err = s.store.SessionRecord(q.SessionID)
		if err != nil {
			return nil, session, err
		}
		if session.WorkspaceID != w.ID || session.Archived {
			return nil, session, errors.New("会话不属于此工作区或已归档")
		}
		roots = append([]string{session.Context.CWD}, session.Context.AdditionalDirectories...)
	}
	valid, err := validateFolders(roots)
	if err != nil {
		return nil, session, err
	}
	if strings.Join(valid, "\x00") != strings.Join(roots, "\x00") {
		return nil, session, errors.New("目录身份已改变，请重新选择工作区")
	}
	return roots, session, nil
}

func (s *Server) searchFiles(q protocol.FileSearchQuery) (domain.FileSearchResult, error) {
	var out domain.FileSearchResult
	if len(q.Query) > 512 || !utf8.ValidString(q.Query) {
		return out, errors.New("文件查询过长或编码无效")
	}
	roots, err := s.referenceRoots(q.GitQuery)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	query := strings.ToLower(strings.TrimSpace(q.Query))
	seen := map[string]bool{}
	for _, dir := range roots {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return out, err
		}
		add := func(path string) {
			if ctx.Err() != nil {
				out.Truncated = true
				return
			}
			path = filepath.ToSlash(path)
			if path == "." || !strings.Contains(strings.ToLower(path), query) || len(path) > 4096 || !utf8.ValidString(path) {
				return
			}
			info, err := root.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return
			}
			full := filepath.Join(dir, path)
			if seen[full] {
				return
			}
			seen[full] = true
			out.Files = append(out.Files, domain.WorkspaceFile{Root: dir, Path: path})
		}
		// Git supplies tracked and non-ignored untracked files. Never include ignored
		// secrets/build trees merely because a client typed part of their name.
		git, gitErr := gitRun(ctx, dir, "rev-parse", "--is-inside-work-tree")
		if gitErr == nil && strings.TrimSpace(git) == "true" {
			paths, e := gitRun(ctx, dir, "ls-files", "-c", "-o", "--exclude-standard", "-z", "--", ".")
			if e != nil {
				root.Close()
				return out, e
			}
			for _, path := range strings.Split(paths, "\x00") {
				if path != "" {
					add(path)
				}
			}
		} else {
			visited := 0
			err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				visited++
				if visited > 20000 || ctx.Err() != nil {
					out.Truncated = true
					return fs.SkipAll
				}
				if entry.IsDir() {
					if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "build" || entry.Name() == "dist") {
						return fs.SkipDir
					}
					return nil
				}
				if !strings.HasPrefix(entry.Name(), ".") {
					add(path)
				}
				return nil
			})
			if err != nil {
				root.Close()
				return out, err
			}
		}
		root.Close()
		if ctx.Err() != nil {
			out.Truncated = true
			break
		}
	}
	rank := func(path string) int {
		base := strings.ToLower(filepath.Base(path))
		if strings.HasPrefix(base, query) {
			return 0
		}
		if strings.Contains(base, query) {
			return 1
		}
		return 2
	}
	sort.Slice(out.Files, func(i, j int) bool {
		a, b := out.Files[i], out.Files[j]
		if rank(a.Path) != rank(b.Path) {
			return rank(a.Path) < rank(b.Path)
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Root < b.Root
	})
	if len(out.Files) > 40 {
		out.Files = out.Files[:40]
		out.Truncated = true
	}
	return out, nil
}
func (s *Server) snapshotFile(q protocol.FileSnapshotQuery) (domain.Attachment, error) {
	roots, err := s.referenceRoots(q.GitQuery)
	if err != nil {
		return domain.Attachment{}, err
	}
	allowed := false
	for _, root := range roots {
		if q.Root == root {
			allowed = true
			break
		}
	}
	if !allowed || q.Path == "." || !filepath.IsLocal(q.Path) || len(q.Path) > 4096 || !utf8.ValidString(q.Path) {
		return domain.Attachment{}, errors.New("引用文件不在当前会话的目录中")
	}
	root, err := os.OpenRoot(q.Root)
	if err != nil {
		return domain.Attachment{}, err
	}
	defer root.Close()
	f, err := root.OpenFile(q.Path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return domain.Attachment{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return domain.Attachment{}, err
	}
	if !info.Mode().IsRegular() {
		return domain.Attachment{}, errors.New("只能引用普通文件")
	}
	if info.Size() > 8<<20 {
		return domain.Attachment{}, errors.New("文件超过 8 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return domain.Attachment{}, err
	}
	a, err := attachments.Describe(filepath.Base(q.Path), data)
	if err != nil {
		return a, err
	}
	a.SourcePath = filepath.Join(q.Root, q.Path)
	for offset := 0; offset < len(data); offset += attachments.ChunkSize {
		if _, err = s.attachments.Put(q.WorkspaceID, a, int64(offset), data[offset:min(offset+attachments.ChunkSize, len(data))]); err != nil {
			return domain.Attachment{}, err
		}
	}
	return a, nil
}
