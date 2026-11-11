package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// Git output and file bodies share the IPC page budget.
type cappedOutput struct {
	bytes.Buffer
	limit int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("Git output exceeds preview limit")
	}
	return b.Buffer.Write(p)
}
func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	argv := append([]string{"--no-optional-locks", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") && !strings.HasPrefix(entry, "LC_ALL=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out := &cappedOutput{limit: 512 << 10}
	diagnostic := &cappedOutput{limit: 4096}
	cmd.Stdout = out
	cmd.Stderr = diagnostic
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("Git: %s (%w)", strings.TrimSpace(diagnostic.String()), err)
	}
	return out.String(), nil
}
func queryGit(ctx context.Context, cwd string) (domain.GitStatus, error) {
	var out domain.GitStatus
	top, err := gitRun(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 128 {
			inside, e := gitRun(ctx, cwd, "rev-parse", "--is-inside-work-tree")
			if e != nil && strings.Contains(err.Error(), "not a git repository") {
				return out, nil
			}
			_ = inside
		}
		return out, err
	}
	out.Applicable = true
	out.Checkout, err = filepath.EvalSymlinks(strings.TrimSpace(top))
	if err != nil {
		return out, err
	}
	out.Repository = out.Checkout
	common, err := gitRun(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return out, err
	}
	out.CommonDir, err = filepath.EvalSymlinks(strings.TrimSpace(common))
	if err != nil {
		return out, err
	}
	branch, _ := gitRun(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
	out.Branch = strings.TrimSpace(branch)
	head, _ := gitRun(ctx, cwd, "rev-parse", "--verify", "HEAD")
	out.Head = strings.TrimSpace(head)
	names, err := gitRun(ctx, cwd, "for-each-ref", "--count=2000", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return out, err
	}
	for _, name := range strings.Split(strings.TrimSpace(names), "\n") {
		if name != "" {
			out.Branches = append(out.Branches, name)
		}
	}
	if out.Branch != "" && !slices.Contains(out.Branches, out.Branch) {
		out.Branches = append(out.Branches, out.Branch)
	}
	defaultRef, _ := gitRun(ctx, cwd, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	candidate := strings.TrimPrefix(strings.TrimSpace(defaultRef), "origin/")
	if slices.Contains(out.Branches, candidate) {
		out.DefaultBranch = candidate
	} else if out.Branch != "" {
		out.DefaultBranch = out.Branch
	} else if len(out.Branches) > 0 {
		out.DefaultBranch = out.Branches[0]
	}
	status, err := gitRun(ctx, cwd, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	out.Dirty = status != ""
	return out, err
}
func (s *Server) gitDirectory(q protocol.GitQuery) (string, error) {
	snap, err := s.store.Snapshot()
	if err != nil {
		return "", err
	}
	for _, w := range snap.Workspaces {
		if w.ID != q.WorkspaceID {
			continue
		}
		if q.SessionID == "" {
			validated, e := validateFolders([]string{w.Folders[0]})
			if e != nil {
				return "", e
			}
			if validated[0] != w.Folders[0] {
				return "", errors.New("execution directory identity changed")
			}
			return validated[0], nil
		}
		for _, session := range snap.Sessions {
			if session.ID == q.SessionID && session.WorkspaceID == w.ID {
				validated, e := validateFolders([]string{session.Context.CWD})
				if e != nil {
					return "", e
				}
				if validated[0] != session.Context.CWD {
					return "", errors.New("execution directory identity changed")
				}
				return validated[0], nil
			}
		}
		return "", errors.New("session is not in this workspace")
	}
	return "", errors.New("workspace unavailable")
}
func (s *Server) gitStatus(q protocol.GitQuery) (domain.GitStatus, error) {
	dir, err := s.gitDirectory(q)
	if err != nil {
		return domain.GitStatus{}, err
	}
	status, err := queryGit(s.ctx, dir)
	if err == nil && status.Applicable {
		err = s.detectGitContext(status)
	}
	return status, err
}
func sameCheckout(ctx context.Context, cwd, checkout string) bool {
	top, err := gitRun(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	actual, err := filepath.EvalSymlinks(strings.TrimSpace(top))
	return err == nil && actual == checkout
}
func (s *Server) detectGitContext(status domain.GitStatus) error {
	snap, err := s.store.Snapshot()
	if err != nil {
		return err
	}
	for _, session := range snap.Sessions {
		if session.ContextChanged || (session.GitBranch == "" && session.GitHead == "") {
			continue
		}
		changed := session.GitBranch != status.Branch || (session.GitBranch == "" && session.GitHead != status.Head)
		if changed && sameCheckout(s.ctx, session.Context.CWD, status.Checkout) {
			session.ContextChanged = true
			if _, err = s.store.Apply(session.ID, domain.Event{Type: "session.git", Session: &session}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Server) checkoutGuard(status domain.GitStatus, own string) error {
	snap, err := s.store.Snapshot()
	if err != nil {
		return err
	}
	for _, session := range snap.Sessions {
		if !sameCheckout(s.ctx, session.Context.CWD, status.Checkout) {
			continue
		}
		busy := session.Status.BlocksCheckout()
		workBusy, e := s.store.HasActiveWork(session.ID)
		if e != nil {
			return e
		}
		busy = busy || workBusy
		for _, r := range snap.PendingRequests {
			if r.SessionID == session.ID && (r.State.AwaitingResolution()) {
				busy = true
			}
		}
		unsettled, e := s.store.HasUnsettledOperation(session.ID)
		if e != nil {
			return e
		}
		if busy || unsettled {
			return fmt.Errorf("%s 正在使用此目录；请等待或新建 worktree", session.Title)
		}
	}
	return nil
}
func (s *Server) checkout(in protocol.CheckoutInput) (domain.GitStatus, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if in.SessionID == "" {
		return domain.GitStatus{}, errors.New("Home branch selection is applied only on submission")
	}
	status, err := s.gitStatus(in.GitQuery)
	if err != nil {
		return status, err
	}
	if !status.Applicable {
		return status, errors.New("not a Git repository")
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return status, err
	}
	if d.Session.Status.BlocksCheckout() {
		return status, errors.New("session must be idle")
	}
	for _, r := range d.Requests {
		if r.State.AwaitingResolution() {
			return status, errors.New("resolve pending requests before switching branches")
		}
	}
	if in.Branch != status.Branch && !slices.Contains(status.Branches, in.Branch) {
		return status, errors.New("branch unavailable")
	}
	if unsettled, e := s.store.HasUnsettledOperation(in.SessionID); e != nil || unsettled {
		if e != nil {
			return status, e
		}
		return status, errors.New("session has an unsettled operation")
	}
	if in.Branch != status.Branch {
		if err = s.checkoutGuard(status, in.SessionID); err != nil {
			return status, err
		}
		if status.Dirty {
			return status, errors.New("请先处理已修改、暂存和未跟踪文件")
		}
		if _, err = gitRun(s.ctx, status.Checkout, "switch", "--no-guess", in.Branch); err != nil {
			return status, err
		}
		status, err = queryGit(s.ctx, status.Checkout)
		if err != nil {
			return status, err
		}
		if err = s.detectGitContext(status); err != nil {
			return status, err
		}
	}
	d, err = s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return status, err
	}
	d.Session.GitBranch = status.Branch
	d.Session.GitHead = status.Head
	d.Session.ContextChanged = false
	_, err = s.store.Apply(d.Session.ID, domain.Event{Type: "session.git", Session: &d.Session})
	return status, err
}
func (s *Server) prepareGit(in domain.Submission, launch domain.LaunchContext) (domain.LaunchContext, error) {
	status, err := queryGit(s.ctx, launch.CWD)
	if err != nil {
		return launch, err
	}
	if !status.Applicable {
		if in.Git.Branch != "" || in.Git.NewWorktree {
			return launch, errors.New("Git selection no longer applies to this directory")
		}
		return launch, nil
	}
	if err = s.detectGitContext(status); err != nil {
		return launch, err
	}
	if in.SessionID != "" {
		d, e := s.store.Session(in.SessionID, 0, 1)
		if e != nil {
			return launch, e
		}
		if in.Git.NewWorktree {
			return launch, errors.New("existing sessions cannot be relocated")
		}
		firstImport := d.Session.Origin != nil && d.Session.ThreadID == "" && d.Session.Context.Repository == ""
		if d.Session.ContextChanged || (d.Session.GitBranch == "" && d.Session.GitHead == "" && !firstImport) {
			return launch, errors.New("执行分支已改变，请在分支选择中明确确认后继续")
		}
		if in.Git.Branch != "" && in.Git.Branch != status.Branch {
			return launch, errors.New("switch an existing session explicitly before submission")
		}
		if firstImport {
			// History import did not accept execution or claim the source's old
			// checkout. The first explicit input pins the current owning checkout.
			launch.Repository, launch.Branch, launch.Head = status.Repository, status.Branch, status.Head
		}
		return launch, nil
	}
	branch := in.Git.Branch
	if branch == "" {
		branch = status.Branch
	}
	if in.Git.NewWorktree {
		if !slices.Contains(status.Branches, branch) || status.Head == "" {
			return launch, errors.New("select a committed branch before creating a worktree")
		}
		sum := sha256.Sum256([]byte(in.OperationID))
		id := hex.EncodeToString(sum[:12])
		base := filepath.Join(s.config.Directory, "worktrees")
		if err = os.MkdirAll(base, 0700); err != nil {
			return launch, err
		}
		dest := filepath.Join(base, id)
		if _, err = gitRun(s.ctx, status.Checkout, "worktree", "add", "-b", "shioji/"+id, dest, branch); err != nil {
			return launch, err
		}
		// Preserve a subdirectory execution context within the new checkout.
		original := launch.CWD
		relative, err := filepath.Rel(status.Checkout, launch.CWD)
		if err != nil {
			s.removeUnacceptedWorktree(original, in)
			return launch, err
		}
		launch.CWD = filepath.Join(dest, relative)
		validated, e := validateFolders([]string{launch.CWD})
		if e != nil {
			s.removeUnacceptedWorktree(original, in)
			return launch, e
		}
		launch.CWD = validated[0]
		status, err = queryGit(s.ctx, launch.CWD)
		if err != nil {
			s.removeUnacceptedWorktree(original, in)
			return launch, err
		}
	} else if branch != status.Branch {
		if !slices.Contains(status.Branches, branch) {
			return launch, errors.New("branch unavailable")
		}
		if err = s.checkoutGuard(status, ""); err != nil {
			return launch, err
		}
		if status.Dirty {
			return launch, errors.New("请先处理已修改、暂存和未跟踪文件")
		}
		if _, err = gitRun(s.ctx, status.Checkout, "switch", "--no-guess", branch); err != nil {
			return launch, err
		}
		status, err = queryGit(s.ctx, launch.CWD)
		if err != nil {
			return launch, err
		}
		if err = s.detectGitContext(status); err != nil {
			return launch, err
		}
	}
	launch.Repository = status.Checkout
	launch.Branch = status.Branch
	launch.Head = status.Head
	return launch, nil
}
func (s *Server) changedFiles(q protocol.GitQuery) ([]domain.ChangedFile, error) {
	status, err := s.gitStatus(q)
	if err != nil || !status.Applicable {
		return nil, err
	}
	files := []domain.ChangedFile{}
	if status.Head != "" {
		out, e := gitRun(s.ctx, status.Checkout, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--numstat", "-z", "HEAD", "--")
		if e != nil {
			return nil, e
		}
		for _, line := range strings.Split(out, "\x00") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) != 3 {
				continue
			}
			f := domain.ChangedFile{Path: parts[2]}
			a, ae := strconv.Atoi(parts[0])
			d, de := strconv.Atoi(parts[1])
			if ae == nil && de == nil {
				f.Added = &a
				f.Removed = &d
			}
			files = append(files, f)
		}
	}
	args := []string{"ls-files", "--others", "--exclude-standard", "-z"}
	if status.Head == "" {
		args = []string{"ls-files", "--cached", "--others", "--exclude-standard", "-z"}
	}
	out, err := gitRun(s.ctx, status.Checkout, args...)
	if err != nil {
		return nil, err
	}
	for _, path := range strings.Split(out, "\x00") {
		if path != "" {
			files = append(files, domain.ChangedFile{Path: path})
		}
	}
	if len(files) > 500 {
		return nil, errors.New("too many changed files; narrow changes before review")
	}
	return files, nil
}
func (s *Server) fileVersions(q protocol.FileQuery) (domain.FileVersions, error) {
	out := domain.FileVersions{Path: q.Path}
	if !filepath.IsLocal(q.Path) || q.Path == "." {
		return out, errors.New("invalid repository path")
	}
	for _, part := range strings.Split(filepath.ToSlash(q.Path), "/") {
		if part == ".git" {
			return out, errors.New("Git metadata is outside file review")
		}
	}
	status, err := s.gitStatus(q.GitQuery)
	if err != nil {
		return out, err
	}
	if !status.Applicable {
		return out, errors.New("not a Git repository")
	}
	root, err := os.OpenRoot(status.Checkout)
	if err != nil {
		return out, err
	}
	defer root.Close()
	f, err := root.OpenFile(q.Path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	if err == nil {
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			return out, errors.New("only regular files can be reviewed")
		}
		b, e := io.ReadAll(io.LimitReader(f, 256<<10+1))
		if e != nil {
			return out, e
		}
		if len(b) > 256<<10 {
			return out, errors.New("file exceeds preview limit")
		}
		out.New = string(b)
	}
	if status.Head != "" {
		out.Old, err = gitRun(s.ctx, status.Checkout, "show", "HEAD:"+filepath.ToSlash(q.Path))
		if err != nil {
			exists, e := gitRun(s.ctx, status.Checkout, "ls-tree", "--name-only", "HEAD", "--", q.Path)
			if e != nil || strings.TrimSpace(exists) != "" {
				return out, err
			}
			out.Old = ""
		}
	}
	if strings.ContainsRune(out.Old, 0) || strings.ContainsRune(out.New, 0) || !utf8.ValidString(out.Old) || !utf8.ValidString(out.New) {
		return out, errors.New("binary files do not have a text preview")
	}
	return out, nil
}

func (s *Server) removeUnacceptedWorktree(original string, in domain.Submission) {
	sum := sha256.Sum256([]byte(in.OperationID))
	id := hex.EncodeToString(sum[:12])
	dest := filepath.Join(s.config.Directory, "worktrees", id)
	// No force: unexpected files or another Git user keep the worktree intact.
	if _, err := gitRun(s.ctx, original, "worktree", "remove", dest); err == nil {
		_, _ = gitRun(s.ctx, original, "branch", "-d", "shioji/"+id)
	}
}

func (s *Server) checkGitContexts() error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, session := range snapshot.Sessions {
		if session.GitBranch == "" && session.GitHead == "" {
			continue
		}
		key := session.Context.Repository
		if key == "" {
			key = session.Context.CWD
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		status, e := queryGit(s.ctx, session.Context.CWD)
		if e != nil {
			continue
		}
		if status.Applicable {
			if err = s.detectGitContext(status); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Server) monitorGit() {
	defer s.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			_ = s.checkGitContexts()
		}
	}
}
