package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"
)

func (s *Server) comparisonRefs(q protocol.GitQuery) (domain.DiffRefCatalog, error) {
	out := domain.DiffRefCatalog{}
	status, err := s.gitStatus(q)
	if err != nil || !status.Applicable {
		return out, err
	}
	out.Refs = []domain.DiffEndpoint{{Kind: "head", Name: "HEAD", Commit: status.Head, Branch: status.Branch}, {Kind: "worktree", Name: "工作目录", Commit: status.Head, Branch: status.Branch}}
	text, err := gitRun(s.ctx, status.Checkout, "for-each-ref", "--count=2000", "--format=%(refname)%09%(objectname)%09%(symref)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return out, err
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || parts[2] != "" {
			continue
		}
		kind, prefix := "branch", "refs/heads/"
		if strings.HasPrefix(parts[0], "refs/remotes/") {
			kind, prefix = "remote", "refs/remotes/"
		}
		name := strings.TrimPrefix(parts[0], prefix)
		out.Refs = append(out.Refs, domain.DiffEndpoint{Kind: kind, Name: name, Commit: parts[1], Branch: name})
	}
	return out, nil
}
func validCommit(commit string) bool {
	if len(commit) != 40 && len(commit) != 64 {
		return false
	}
	for _, r := range commit {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func (s *Server) resolveComparison(q protocol.GitQuery) (domain.DiffComparison, error) {
	status, err := s.gitStatus(q)
	if err != nil {
		return domain.DiffComparison{}, err
	}
	if !status.Applicable {
		return domain.DiffComparison{}, errors.New("not a Git repository")
	}
	cmp := domain.DefaultDiffComparison()
	if q.Comparison != nil {
		cmp = *q.Comparison
	}
	if cmp.Repository != "" && cmp.Repository != status.Checkout {
		return cmp, errors.New("comparison belongs to another repository")
	}
	pinned := cmp.Repository != ""
	cmp.Repository = status.Checkout
	resolve := func(e domain.DiffEndpoint) (domain.DiffEndpoint, error) {
		if len(e.Name) > 4096 || !utf8.ValidString(e.Name) || strings.ContainsAny(e.Name, "\x00\r\n") {
			return e, errors.New("invalid comparison ref")
		}
		switch e.Kind {
		case "worktree":
			e.Name = "工作目录"
			e.Commit, e.Branch = status.Head, status.Branch
			return e, nil
		case "empty":
			if e.Commit != "" {
				return e, errors.New("invalid empty version")
			}
			return e, nil
		case "head":
			e.Name = "HEAD"
			if !pinned {
				e.Commit, e.Branch = status.Head, status.Branch
			}
			if e.Commit == "" {
				e.Kind = "empty"
				return e, nil
			}
		case "branch", "remote":
			if !pinned {
				prefix := "refs/heads/"
				if e.Kind == "remote" {
					prefix = "refs/remotes/"
				}
				ref := prefix + e.Name
				checked, er := gitRun(s.ctx, status.Checkout, "check-ref-format", ref)
				_ = checked
				if er != nil {
					return e, errors.New("invalid branch")
				}
				commit, er := gitRun(s.ctx, status.Checkout, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
				if er != nil {
					return e, er
				}
				e.Commit, e.Branch = strings.TrimSpace(commit), e.Name
			}
		default:
			return e, errors.New("unsupported comparison version")
		}
		if !validCommit(e.Commit) {
			return e, errors.New("invalid fixed commit")
		}
		if _, er := gitRun(s.ctx, status.Checkout, "cat-file", "-e", e.Commit+"^{commit}"); er != nil {
			return e, er
		}
		return e, nil
	}
	cmp.Base, err = resolve(cmp.Base)
	if err != nil {
		return cmp, err
	}
	cmp.Target, err = resolve(cmp.Target)
	return cmp, err
}

// Empty trees are hashed without -w: comparing an unborn HEAD writes no object.
func (s *Server) comparisonTree(dir string, e domain.DiffEndpoint) (string, error) {
	if e.Kind != "empty" {
		return e.Commit, nil
	}
	return gitRun(s.ctx, dir, "hash-object", "-t", "tree", "--stdin")
}
func (s *Server) compareGit(q protocol.GitQuery) (domain.ComparisonResult, error) {
	out := domain.ComparisonResult{Files: []domain.ChangedFile{}}
	cmp, err := s.resolveComparison(q)
	out.Comparison = cmp
	if err != nil {
		return out, err
	}
	if cmp.Base == cmp.Target || cmp.Base.Kind != "worktree" && cmp.Target.Kind != "worktree" && cmp.Base.Commit == cmp.Target.Commit {
		return s.finishComparison(q, out)
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--raw", "--numstat", "-z"}
	working := cmp.Base.Kind == "worktree" || cmp.Target.Kind == "worktree"
	if working {
		endpoint := cmp.Base
		if cmp.Base.Kind == "worktree" {
			endpoint = cmp.Target
			args = append(args, "-R")
		}
		tree, e := s.comparisonTree(cmp.Repository, endpoint)
		if e != nil {
			return out, e
		}
		args = append(args, strings.TrimSpace(tree), "--")
	} else {
		for _, endpoint := range []domain.DiffEndpoint{cmp.Base, cmp.Target} {
			tree, e := s.comparisonTree(cmp.Repository, endpoint)
			if e != nil {
				return out, e
			}
			args = append(args, strings.TrimSpace(tree))
		}
		args = append(args, "--")
	}
	text, err := gitRun(s.ctx, cmp.Repository, args...)
	if err != nil {
		return out, err
	}
	out.Files = parseGitChangedFiles(text)
	if working {
		text, err = gitRun(s.ctx, cmp.Repository, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return out, err
		}
		for _, path := range strings.Split(text, "\x00") {
			if path == "" {
				continue
			}
			// A path untracked in this checkout can still exist in the compared branch.
			// Read both actual versions instead of reporting a duplicate add/delete.
			at := slices.IndexFunc(out.Files, func(f domain.ChangedFile) bool { return f.Path == path })
			file, e := s.readComparisonFile(cmp, path)
			if e == nil && file.Old == file.New && at >= 0 {
				out.Files = slices.DeleteFunc(out.Files, func(f domain.ChangedFile) bool { return f.Path == path })
				continue
			}
			if at >= 0 {
				out.Files[at].Added, out.Files[at].Removed = nil, nil
				out.Files[at].Kind = "update"
			} else {
				kind := "add"
				if cmp.Base.Kind == "worktree" {
					kind = "delete"
				}
				out.Files = append(out.Files, domain.ChangedFile{Path: path, Kind: kind})
			}
		}
	}
	if len(out.Files) > 500 {
		return out, errors.New("too many changed files; narrow changes before review")
	}
	slices.SortFunc(out.Files, func(a, b domain.ChangedFile) int { return strings.Compare(a.Path, b.Path) })
	return s.finishComparison(q, out)
}
func validReviewPath(path string) bool {
	if !filepath.IsLocal(path) || path == "." || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsAny(path, "\x00\r\n") || strings.HasPrefix(path, ":") {
		return false
	}
	return !slices.Contains(strings.Split(filepath.ToSlash(path), "/"), ".git")
}
func (s *Server) comparisonFile(q protocol.FileQuery) (domain.FileVersions, error) {
	if !validReviewPath(q.Path) {
		return domain.FileVersions{}, errors.New("invalid repository path")
	}
	cmp, err := s.resolveComparison(q.GitQuery)
	if err != nil {
		return domain.FileVersions{}, err
	}
	if expected := q.Comparison; expected != nil && expected.Repository != "" {
		if expected.Base.Kind == "worktree" && expected.Base != cmp.Base || expected.Target.Kind == "worktree" && expected.Target != cmp.Target {
			return domain.FileVersions{}, errors.New("工作目录分支已变化，请刷新比较")
		}
	}
	out, err := s.readComparisonFile(cmp, q.Path)
	if err == nil {
		err = diffPreviewSize(out)
	}
	if err == nil && s.attachments != nil {
		out.Snapshot, err = s.storeDiffSnapshot(q.WorkspaceID, out)
	}
	return out, err
}
func (s *Server) readComparisonFile(cmp domain.DiffComparison, path string) (domain.FileVersions, error) {
	out := domain.FileVersions{Path: path, Comparison: cmp}
	if !validReviewPath(path) {
		return out, errors.New("invalid repository path")
	}
	read := func(endpoint domain.DiffEndpoint) (string, error) {
		if endpoint.Kind == "empty" {
			return "", nil
		}
		var text string
		if endpoint.Kind == "worktree" {
			root, err := os.OpenRoot(cmp.Repository)
			if err != nil {
				return "", err
			}
			defer root.Close()
			file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
			if errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				return "", errors.New("only regular files can be reviewed")
			}
			data, err := io.ReadAll(io.LimitReader(file, (256<<10)+1))
			if err != nil {
				return "", err
			}
			text = string(data)
		} else {
			// Literal pathspec excludes wildcard/pathspec interpretation. Symlink and
			// submodule blobs are not ordinary source files, even at fixed commits.
			entry, err := gitRun(s.ctx, cmp.Repository, "ls-tree", "-z", endpoint.Commit, "--", ":(literal)"+filepath.ToSlash(path))
			if err != nil {
				return "", err
			}
			if entry == "" {
				return "", nil
			}
			if !strings.HasPrefix(entry, "100644 blob ") && !strings.HasPrefix(entry, "100755 blob ") {
				return "", errors.New("only regular files can be reviewed")
			}
			text, err = gitRun(s.ctx, cmp.Repository, "show", endpoint.Commit+":"+filepath.ToSlash(path))
			if err != nil {
				return "", err
			}
		}
		if len(text) > 256<<10 {
			return "", errors.New("file exceeds preview limit")
		}
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return "", fmt.Errorf("binary files do not have a text preview")
		}
		return text, nil
	}
	var err error
	out.Old, err = read(cmp.Base)
	if err != nil {
		return out, err
	}
	out.New, err = read(cmp.Target)
	return out, err
}

func (s *Server) finishComparison(q protocol.GitQuery, out domain.ComparisonResult) (domain.ComparisonResult, error) {
	if q.ReviewPath != "" {
		if !validReviewPath(q.ReviewPath) {
			return out, errors.New("invalid repository path")
		}
		if !slices.ContainsFunc(out.Files, func(file domain.ChangedFile) bool { return file.Path == q.ReviewPath }) {
			out.Versions = &domain.FileVersions{Path: q.ReviewPath, Comparison: out.Comparison, Unchanged: true}
			return out, diffPreviewSize(out)
		}
		query := q
		query.Comparison = &out.Comparison
		versions, err := s.comparisonFile(protocol.FileQuery{GitQuery: query, Path: q.ReviewPath})
		if err != nil {
			return out, err
		}
		out.Versions = &versions
	}
	return out, diffPreviewSize(out)
}

func diffPreviewSize(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// Leave space for the response envelope and the snapshot descriptor.
	if len(data) > protocol.MaxFrame-4096 {
		return errors.New("预览内容超过传输上限，请选择更小的文件或比较范围")
	}
	return nil
}
