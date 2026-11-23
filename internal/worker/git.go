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
	"shioji/internal/store"
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
	timeout := 10 * time.Second
	worktreeCheckout := len(args) >= 2 && args[0] == "worktree" && args[1] == "add"
	if worktreeCheckout {
		// A worktree checkout writes the entire tree, unlike Git metadata reads.
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append([]string{"--no-optional-locks", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	// Git filters may retain inherited output after the Git leader exits. This
	// invocation owns a private group; cancellation ends it, while WaitDelay
	// bounds pipe collection even when a descendant no longer closes its fd.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stop := func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = stop
	cmd.WaitDelay = 100 * time.Millisecond
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
		// Output-limit and inherited-pipe failures abandon this same invocation
		// too. Never leave its filter running merely because the leader exited.
		_ = stop()
		if cause := ctx.Err(); cause != nil {
			if worktreeCheckout && errors.Is(cause, context.DeadlineExceeded) {
				return "", fmt.Errorf("Git 工作树检出超时：%w", cause)
			}
			return "", cause
		}
		if errors.Is(err, exec.ErrWaitDelay) {
			return "", &protocol.RemoteError{Code: "gitOutputIncomplete", Message: "Git 未返回完整结果"}
		}
		if message := strings.TrimSpace(diagnostic.String()); message != "" {
			return "", fmt.Errorf("Git: %s (%w)", message, err)
		}
		return "", fmt.Errorf("Git: %w", err)
	}
	return out.String(), nil
}
func queryGit(ctx context.Context, cwd string) (domain.GitStatus, error) {
	return readGitStatus(ctx, cwd, true)
}

func readGitStatus(ctx context.Context, cwd string, detailed bool) (domain.GitStatus, error) {
	var out domain.GitStatus
	top, err := gitRun(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 128 && strings.Contains(err.Error(), "not a git repository") {
			return out, nil
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
	// Porcelain v2 supplies branch, HEAD and dirty state in the same native
	// read. Ahead/behind counts and rename detection are not needed for admission.
	untracked := "--untracked-files=no"
	if detailed {
		untracked = "--untracked-files=all"
	}
	status, err := gitRun(ctx, cwd, "status", "--porcelain=v2", "--branch", "--no-ahead-behind", "--no-renames", "-z", untracked)
	if err != nil {
		return out, err
	}
	branchRead, headRead := false, false
	for _, record := range strings.Split(status, "\x00") {
		switch {
		case strings.HasPrefix(record, "# branch.head "):
			out.Branch = strings.TrimPrefix(record, "# branch.head ")
			branchRead = out.Branch != ""
		case strings.HasPrefix(record, "# branch.oid "):
			out.Head = strings.TrimPrefix(record, "# branch.oid ")
			headRead = out.Head != ""
			if out.Head == "(initial)" {
				out.Head = ""
			}
		case record == "" || strings.HasPrefix(record, "# "):
		default:
			out.Dirty = true
		}
	}
	if !branchRead || !headRead {
		return out, errors.New("Git status omitted checkout identity")
	}
	if out.Branch == "(detached)" {
		// This is also a legal literal branch name. Disambiguate it without
		// paying for a separate branch query on the ordinary path.
		branch, e := gitRun(ctx, cwd, "branch", "--show-current")
		if e != nil {
			return out, e
		}
		out.Branch = strings.TrimSpace(branch)
	}
	if !detailed {
		return out, nil
	}
	names, err := gitRun(ctx, cwd, "for-each-ref", "--count=2000", "--format=%(refname:lstrip=2)", "refs/heads/")
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
	defaultRef, _ := gitRun(ctx, cwd, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	candidate := strings.TrimPrefix(strings.TrimSpace(defaultRef), "refs/remotes/origin/")
	if slices.Contains(out.Branches, candidate) {
		out.DefaultBranch = candidate
	} else if out.Branch != "" {
		out.DefaultBranch = out.Branch
	} else if len(out.Branches) > 0 {
		out.DefaultBranch = out.Branches[0]
	}
	return out, nil
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
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return s.gitStatusLocked(q)
}
func (s *Server) gitStatusLocked(q protocol.GitQuery) (domain.GitStatus, error) {
	if status, removed, err := s.removedWorktreeStatus(q); removed || err != nil {
		return status, err
	}
	dir, err := s.gitDirectory(q)
	if err != nil {
		return domain.GitStatus{}, err
	}
	observed, err := s.store.Snapshot()
	if err != nil {
		return domain.GitStatus{}, err
	}
	status, err := s.queryGitOutsideDispatch(s.ctx, dir)
	if err == nil && status.Applicable {
		err = s.detectGitContext(status, observed.Sessions)
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
func (s *Server) detectGitContext(status domain.GitStatus, observed ...[]domain.Session) error {
	snap, err := s.store.Snapshot()
	if err != nil {
		return err
	}
	sessions := snap.Sessions
	if len(observed) > 0 {
		sessions = observed[0]
	}
	for _, session := range sessions {
		if session.ContextChanged || (session.GitBranch == "" && session.GitHead == "") {
			continue
		}
		changed := session.GitBranch != status.Branch || (session.GitBranch == "" && session.GitHead != status.Head)
		if changed && s.sameCheckoutOutsideDispatch(session.Context.CWD, status.Checkout) {
			current, e := s.store.SessionRecord(session.ID)
			if e != nil {
				continue
			}
			fresh := current
			if fresh.ThreadID != session.ThreadID || fresh.Context.CWD != session.Context.CWD || fresh.GitBranch != session.GitBranch || fresh.GitHead != session.GitHead {
				continue
			}
			session = fresh
			session.ContextChanged = true
			if _, err = s.store.Apply(session.ID, domain.Event{Type: "session.git", Session: &session}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Server) checkoutGuard(status domain.GitStatus, own string) error {
	retired, err := s.executions.Detached()
	if err != nil {
		return err
	}
	for _, scope := range retired {
		session, e := s.store.SessionRecord(scope.Session)
		if e != nil {
			return e
		}
		if s.sameCheckoutOutsideDispatch(session.Context.CWD, status.Checkout) {
			return fmt.Errorf("%s 的旧执行尚未确认停止；请等待或新建 worktree", session.Title)
		}
	}
	snap, err := s.store.Snapshot()
	if err != nil {
		return err
	}
	for _, session := range snap.Sessions {
		if !s.sameCheckoutOutsideDispatch(session.Context.CWD, status.Checkout) {
			continue
		}
		busy := session.Status.BlocksCheckout() || session.Goal.Active()
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
	return s.updateCheckout(in, nil)
}

// An observed head is supplied only for confirmation. A stale confirmation
// cannot fall through to the code that changes the shared checkout.
func (s *Server) updateCheckout(in protocol.CheckoutInput, observedHead *string) (domain.GitStatus, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if in.SessionID == "" {
		return domain.GitStatus{}, errors.New("Home branch selection is applied only on submission")
	}
	status, err := s.gitStatusLocked(in.GitQuery)
	if err != nil {
		return status, err
	}
	if !status.Applicable {
		return status, errors.New("not a Git repository")
	}
	checkout, common := status.Checkout, status.CommonDir
	release, err := s.beginGitTransition(checkout, common)
	if err != nil {
		return status, err
	}
	defer release()
	status, err = s.queryGitOutsideDispatch(s.ctx, checkout)
	if err != nil {
		return status, err
	}
	if observedHead != nil && (!status.Applicable || status.Checkout != checkout || status.CommonDir != common ||
		status.Branch != in.Branch || status.Head != *observedHead) {
		return status, &protocol.RemoteError{Code: "gitContextChanged", Message: "分支或提交已变化，请刷新后再确认"}
	}
	if err = s.updateAdmission(); err != nil {
		return status, err
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return status, err
	}
	if d.Session.Status.BlocksCheckout() || d.Session.Goal.Active() {
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
	if observedHead != nil {
		if d.Session.Archived || d.Session.ExecutionGuard != "" {
			return status, errors.New("当前会话暂时无法确认分支")
		}
		workspace, e := s.store.Workspace(d.Session.WorkspaceID)
		if e != nil {
			return status, e
		}
		if workspace.Archived {
			return status, errors.New("请先恢复工作区")
		}
		if err = s.sessionMaintenanceAdmission(in.SessionID); err != nil {
			return status, err
		}
		if busy, e := s.store.HasActiveWork(in.SessionID); e != nil {
			return status, e
		} else if busy {
			return status, errors.New("会话仍有活动工作")
		}
		if !d.Session.ContextChanged && d.Session.GitBranch == status.Branch && d.Session.GitHead == status.Head {
			return status, nil
		}
	} else if in.Branch != status.Branch {
		if err = s.checkoutGuard(status, in.SessionID); err != nil {
			return status, err
		}
		if status.Dirty {
			return status, dirtyBranchSwitchError(status, in.Branch)
		}
		if err = s.updateAdmission(); err != nil {
			return status, err
		}
		latest, e := s.store.Session(in.SessionID, 0, 1)
		if e != nil {
			return status, e
		}
		if latest.Session.ThreadID != d.Session.ThreadID || latest.Session.Context.CWD != d.Session.Context.CWD || latest.Session.Archived || (latest.Session.Status.BlocksCheckout() || latest.Session.Goal.Active()) || latest.Session.ExecutionGuard != "" {
			return status, errors.New("会话执行上下文已改变，分支未切换")
		}
		for _, req := range latest.Requests {
			if req.State.AwaitingResolution() {
				return status, errors.New("请先处理当前会话请求")
			}
		}
		workspace, e := s.store.Workspace(latest.Session.WorkspaceID)
		if e != nil {
			return status, e
		}
		if workspace.Archived {
			return status, errors.New("工作区已归档，分支未切换")
		}
		if busy, e := s.store.HasActiveWork(in.SessionID); e != nil || busy {
			if e != nil {
				return status, e
			}
			return status, errors.New("会话仍有活动工作")
		}
		if busy, e := s.store.HasUnsettledOperation(in.SessionID); e != nil || busy {
			if e != nil {
				return status, e
			}
			return status, errors.New("会话输入尚未结清")
		}

		if _, err = s.gitRunOutsideDispatch(s.ctx, status.Checkout, "switch", "--no-guess", in.Branch); err != nil {
			return status, err
		}
		status, err = s.queryGitOutsideDispatch(s.ctx, status.Checkout)
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
func (s *Server) prepareGitLocked(in domain.Submission, launch domain.LaunchContext) (domain.LaunchContext, error) {
	owner := s.sessionTransitions[in.SessionID]
	valid := func() error {
		if err := s.updateAdmission(); err != nil {
			return err
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if owner != nil && !owner.active() {
			return errSessionTransitionRetired
		}
		if in.WorkspaceID != "" {
			w, e := s.store.Workspace(in.WorkspaceID)
			if e != nil {
				return e
			}
			if w.Archived {
				return errors.New("工作区已归档")
			}
		}
		if in.SessionID != "" {
			d, e := s.store.Session(in.SessionID, 0, 1)
			if e != nil {
				return e
			}
			if d.Session.Archived || d.Session.Context.CWD != launch.CWD {
				return errors.New("会话执行上下文已改变")
			}
		}
		return nil
	}

	status, err := s.readGitOutsideDispatch(s.ctx, launch.CWD, false)
	if err != nil {
		return launch, err
	}
	if !status.Applicable {
		if in.Git.Branch != "" || in.Git.NewWorktree {
			return launch, errors.New("Git selection no longer applies to this directory")
		}
		return launch, nil
	}

	var release func()
	for {
		common := ""
		mutating := in.Git.NewWorktree || in.SessionID == "" && in.Git.Branch != "" && in.Git.Branch != status.Branch
		if mutating {
			common = status.CommonDir
		}
		release, err = s.beginGitTransition(status.Checkout, common)
		if err != nil {
			return launch, err
		}
		current, e := s.readGitOutsideDispatch(s.ctx, launch.CWD, mutating)
		if e != nil {
			release()
			return launch, e
		}
		if current.Checkout != status.Checkout || current.CommonDir != status.CommonDir {
			release()
			return launch, errors.New("执行目录的 Git checkout 已改变，输入未发送")
		}
		if !mutating && in.SessionID == "" && in.Git.Branch != "" && in.Git.Branch != current.Branch {
			release()
			status = current
			continue
		}
		status = current
		break
	}
	defer release()
	if err = valid(); err != nil {
		return launch, err
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
		base, err = filepath.EvalSymlinks(base)
		if err != nil {
			return launch, err
		}
		dest := filepath.Join(base, id)
		err = s.prepareOwnedWorktree(in, status, dest, branch, "shioji/"+id, valid)
		if err != nil {
			return launch, err
		}
		// Preserve a subdirectory execution context within the new checkout.
		relative, err := filepath.Rel(status.Checkout, launch.CWD)
		if err != nil {
			return launch, err
		}
		launch.CWD = filepath.Join(dest, relative)
		validated, e := validateFolders([]string{launch.CWD})
		if e != nil {
			return launch, e
		}
		launch.CWD = validated[0]
		status, err = s.queryGitOutsideDispatch(s.ctx, launch.CWD)
		if err != nil {
			return launch, err
		}
		if err = s.store.SaveOwnedWorktree(store.OwnedWorktree{Path: status.Checkout, CommonDir: status.CommonDir, Branch: status.Branch, Head: status.Head, CreationOperation: in.OperationID, CreationBase: branch}); err != nil {
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
			return launch, dirtyBranchSwitchError(status, branch)
		}
		// Reject a known conflict before journaling a possible checkout mutation.
		// Git itself still guards the switch against concurrent external changes.
		ref := "refs/heads/" + branch
		occupancy, err := s.gitRunOutsideDispatch(s.ctx, status.Checkout, "for-each-ref", "--count=1", "--format=%(refname)%00%(worktreepath)", ref)
		if err != nil {
			return launch, err
		}
		name, path, _ := strings.Cut(strings.TrimSuffix(occupancy, "\n"), "\x00")
		if name == ref && path != "" && path != status.Checkout {
			return launch, fmt.Errorf("分支“%s”已在工作树“%s”中检出。勾选“新建 worktree”可基于该分支开始会话，也可以选择其他分支。", branch, path)
		}
		if err = valid(); err != nil {
			return launch, err
		}
		if err = s.store.MarkSubmissionPreparation(in.OperationID, domain.SubmissionSwitchingBranch); err != nil {
			return launch, err
		}
		if err = s.store.RecordSubmissionGit(in.OperationID, status.Checkout, status.Branch, branch); err != nil {
			return launch, err
		}
		if _, err = s.gitRunOutsideDispatch(s.ctx, status.Checkout, "switch", "--no-guess", branch); err != nil {
			return launch, err
		}
		status, err = s.queryGitOutsideDispatch(s.ctx, launch.CWD)
		if err != nil {
			return launch, err
		}
		if err = s.detectGitContext(status); err != nil {
			return launch, err
		}
	}
	if err = valid(); err != nil {
		return launch, err
	}
	launch.Repository = status.Checkout
	launch.Branch = status.Branch
	launch.Head = status.Head
	return launch, nil
}

func dirtyBranchSwitchError(status domain.GitStatus, target string) error {
	current := status.Branch
	if current == "" {
		current = "分离 HEAD"
	}
	return fmt.Errorf("无法从“%s”切换到“%s”：当前目录有未提交的改动或未跟踪文件。选择当前分支“%s”可直接继续；如需切换，请先提交改动并处理未跟踪文件，或在新建会话时勾选“新建 worktree”。", current, target, current)
}

// --raw supplies file identity even for empty/binary files; --numstat supplies
// independent line counts. -z keeps tabs and newlines inside paths unambiguous.
func parseGitChangedFiles(text string) []domain.ChangedFile {
	files := []domain.ChangedFile{}
	kinds := map[string]string{}
	records := strings.Split(text, "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		if strings.HasPrefix(record, ":") && i+1 < len(records) {
			fields := strings.Fields(record)
			i++
			if len(fields) == 5 && len(fields[4]) > 0 {
				kinds[records[i]] = map[byte]string{'A': "add", 'D': "delete", 'M': "update", 'T': "update"}[fields[4][0]]
			}
			continue
		}
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		file := domain.ChangedFile{Path: parts[2]}
		a, ae := strconv.Atoi(parts[0])
		d, de := strconv.Atoi(parts[1])
		if ae == nil && de == nil {
			file.Added, file.Removed = &a, &d
		}
		files = append(files, file)
	}
	for i := range files {
		files[i].Kind = kinds[files[i].Path]
	}
	return files
}

func (s *Server) changedFiles(q protocol.GitQuery) ([]domain.ChangedFile, error) {
	status, err := s.gitStatus(q)
	if err != nil || !status.Applicable {
		return nil, err
	}
	files := []domain.ChangedFile{}
	if status.Head != "" {
		out, e := gitRun(s.ctx, status.Checkout, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--raw", "--numstat", "-z", status.Head, "--")
		if e != nil {
			return nil, e
		}
		files = parseGitChangedFiles(out)
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
			files = append(files, domain.ChangedFile{Path: path, Kind: "add"})
		}
	}
	if len(files) > 500 {
		return nil, errors.New("too many changed files; narrow changes before review")
	}
	return files, nil
}
func (s *Server) fileVersions(q protocol.FileQuery) (domain.FileVersions, error) {
	if q.Comparison != nil {
		return s.comparisonFile(q)
	}
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
		out.Old, err = gitRun(s.ctx, status.Checkout, "show", status.Head+":"+filepath.ToSlash(q.Path))
		if err != nil {
			exists, e := gitRun(s.ctx, status.Checkout, "ls-tree", "--name-only", status.Head, "--", q.Path)
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
		status, e := s.readGitOutsideDispatch(s.ctx, session.Context.CWD, false)
		if e != nil {
			continue
		}
		if status.Applicable {
			if err = s.detectGitContext(status, snapshot.Sessions); err != nil {
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
