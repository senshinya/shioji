package worker

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/protocol"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/store"
)

// All gate changes occur under dispatchMu. Git and filesystem waits release it;
// only admissions referencing the same owned checkout wait for the operation.
func (s *Server) beginWorktreeTransition(path string) func() {
	if s.worktreeTransitions == nil {
		s.worktreeTransitions = map[string]chan struct{}{}
	}
	done := make(chan struct{})
	s.worktreeTransitions[path] = done
	return func() { delete(s.worktreeTransitions, path); close(done) }
}
func (s *Server) waitWorktreeTransition(path string) error {
	for s.worktreeTransitions[path] != nil {
		done := s.worktreeTransitions[path]
		s.dispatchMu.Unlock()
		select {
		case <-done:
		case <-s.ctx.Done():
		}
		s.dispatchMu.Lock()
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) ownedWorktreePath(w store.OwnedWorktree) error {
	base, err := filepath.EvalSymlinks(filepath.Join(s.config.Directory, "worktrees"))
	if err != nil {
		return err
	}
	if filepath.Dir(w.Path) != base || w.CommonDir == "" || w.Head == "" {
		return errors.New("自建 worktree 的归属记录不完整，保留目录")
	}
	if info, err := os.Lstat(w.Path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("自建 worktree 路径已变为符号链接，保留目录")
	}
	return nil
}
func worktreeUsesPaths(w store.OwnedWorktree, paths []string) bool {
	for _, path := range paths {
		if store.WorktreeContains(w.Path, path) {
			return true
		}
	}
	return false
}

// Restoration is lazy and requires an explicit execution admission. Merely
// restoring metadata, opening history or reconnecting never launches anything.
func (s *Server) restoreOwnedWorktrees(paths []string) error {
	records, err := s.store.OwnedWorktrees()
	if err != nil {
		return err
	}
	for _, w := range records {
		if !worktreeUsesPaths(w, paths) {
			continue
		}
		if err = s.waitWorktreeTransition(w.Path); err != nil {
			return err
		}
		// A collector may have changed branch/head while this admission waited.
		latest, e := s.store.OwnedWorktrees()
		if e != nil {
			return e
		}
		for _, current := range latest {
			if current.Path == w.Path {
				w = current
				break
			}
		}
		if err = s.ownedWorktreePath(w); err != nil {
			return err
		}
		common := ""
		if _, e := os.Lstat(w.Path); os.IsNotExist(e) {
			common = w.CommonDir
		}
		release, e := s.beginGitTransition(w.Path, common)
		if e != nil {
			return e
		}
		e = func() error {
			defer release()
			if _, e = os.Lstat(w.Path); e == nil {
				status, e := s.queryGitOutsideDispatch(s.ctx, w.Path)
				if e != nil || status.Checkout != w.Path || status.CommonDir != w.CommonDir {
					return errors.New("自建 worktree 的目录身份已改变")
				}
				return s.updateAdmission()
			} else if !os.IsNotExist(e) {
				return e
			}
			e = s.addOwnedWorktree(w, false, s.updateAdmission)
			if e == nil {
				s.dispatchMu.Unlock()
				// Git does not preserve empty execution subdirectories.
				for _, path := range paths {
					if store.WorktreeContains(w.Path, path) {
						e = errors.Join(e, os.MkdirAll(path, 0700))
					}
				}
				s.dispatchMu.Lock()
			}
			if e == nil {
				w.Removed = false
				e = s.store.SaveOwnedWorktree(w)
			}
			if e != nil {
				return fmt.Errorf("恢复自建 worktree：%w", e)
			}
			return s.updateAdmission()
		}()
		if e != nil {
			return e
		}
	}
	return s.updateAdmission()
}

// A shared checkout is removable only after all independent users are archived
// or deleted, every captured generation is physically stopped, and no transition
// can still create a fork/producer. Unknown state always retains the directory.
func (s *Server) ownedWorktreeCollectable(w store.OwnedWorktree) (bool, bool, error) {
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return false, false, err
	}
	workspaces := map[string]domain.Workspace{}
	referenced := false
	for _, workspace := range snapshot.Workspaces {
		workspaces[workspace.ID] = workspace
		if worktreeUsesPaths(w, workspace.Folders) {
			referenced = true
			if !workspace.Archived {
				return false, true, nil
			}
		}
	}
	owners := map[string]bool{}
	for _, id := range w.DeletedSessions {
		owners[id] = true
	}
	for _, session := range snapshot.Sessions {
		if !store.WorktreeSession(w, session) {
			continue
		}
		referenced = true
		owners[session.ID] = true
		if !session.Archived && !workspaces[session.WorkspaceID].Archived || s.sessionTransitions[session.ID] != nil {
			return false, true, nil
		}
		if r := s.currentRuntime(session.ID); r != nil && !r.Stopped() {
			return false, true, nil
		}
		scope, e := s.executions.Existing(session.ID)
		if e != nil {
			return false, true, e
		}
		if scope != nil {
			if e = scope.VerifyStopped(); e != nil {
				return false, true, nil
			}
		}
	}
	// Deleted sessions no longer occur in Snapshot. Their outbox must settle
	// before the directory they used can be removed.
	for _, id := range w.DeletedSessions {
		if s.sessionTransitions[id] != nil {
			return false, referenced, nil
		}
		if r := s.currentRuntime(id); r != nil && !r.Stopped() {
			return false, referenced, nil
		}
		scope, e := s.executions.Existing(id)
		if e != nil {
			return false, referenced, e
		}
		if scope != nil {
			if e = scope.VerifyStopped(); e != nil {
				return false, referenced, nil
			}
		}
	}
	detached, err := s.executions.Detached()
	if err != nil {
		return false, referenced, err
	}
	for _, scope := range detached {
		if owners[scope.Session] {
			return false, referenced, nil
		}
	}
	// A prepared creation without any accepted/deleted owner is still unknown,
	// not an archive. Keep its checkout and intent available to an exact retry.
	if !referenced && len(w.DeletedSessions) == 0 && w.CreationOperation != "" {
		if _, e := s.store.Operation(w.CreationOperation); errors.Is(e, sql.ErrNoRows) {
			return false, false, nil
		} else if e != nil {
			return false, false, e
		}
	}
	return true, referenced, nil
}

type worktreeRetry struct {
	delay time.Duration
	next  time.Time
}

func (s *Server) cleanOwnedWorktrees(now time.Time) error {
	result := s.discoverOwnedWorktrees("", "")
	records, err := s.store.OwnedWorktrees()
	if err != nil {
		return err
	}
	for path := range s.worktreeRetries {
		found := false
		for _, w := range records {
			found = found || path == w.Path
		}
		if !found {
			delete(s.worktreeRetries, path)
		}
	}
	for _, w := range records {
		if now.Before(s.worktreeRetries[w.Path].next) {
			continue
		}
		if s.worktreeTransitions[w.Path] != nil || s.worktreeTransitions[w.CommonDir] != nil {
			continue
		}
		ready, referenced, e := s.ownedWorktreeCollectable(w)
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		if !ready {
			continue
		}
		if e = s.ownedWorktreePath(w); e != nil {
			result = errors.Join(result, e)
			continue
		}
		release, e := s.beginGitTransition(w.Path, w.CommonDir)
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		s.dispatchMu.Unlock()
		e = s.archiveOwnedWorktree(w)
		s.dispatchMu.Lock()
		release()
		if e == nil && !referenced {
			// A restore may have happened during Git I/O. Keep metadata if it now
			// references the removed checkout; its next submission can recreate it.
			snapshot, se := s.store.Snapshot()
			e = se
			used := false
			for _, session := range snapshot.Sessions {
				used = used || store.WorktreeSession(w, session)
			}
			for _, workspace := range snapshot.Workspaces {
				used = used || worktreeUsesPaths(w, workspace.Folders)
			}
			if e == nil && !used {
				e = s.store.ForgetOwnedWorktree(w.Path)
			}
		}
		if e != nil {
			if s.worktreeRetries == nil {
				s.worktreeRetries = map[string]worktreeRetry{}
			}
			delay := s.worktreeRetries[w.Path].delay * 2
			if delay == 0 {
				delay = 30 * time.Second
			}
			if delay > time.Hour {
				delay = time.Hour
			}
			s.worktreeRetries[w.Path] = worktreeRetry{delay: delay, next: now.Add(delay)}
		} else {
			delete(s.worktreeRetries, w.Path)
		}
		result = errors.Join(result, e)
	}
	return result
}

func (s *Server) archiveOwnedWorktree(w store.OwnedWorktree) error {
	if _, err := os.Lstat(w.Path); os.IsNotExist(err) {
		w.Removed = true
		return s.store.SaveOwnedWorktree(w)
	} else if err != nil {
		return err
	}
	status, err := queryGit(s.ctx, w.Path)
	if err != nil {
		return err
	}
	if status.Checkout != w.Path || status.CommonDir != w.CommonDir {
		return errors.New("自建 worktree 的 Git 归属已改变，保留目录")
	}
	w.Branch, w.Head = status.Branch, status.Head
	// A detached HEAD needs a durable ref before removal makes its backup
	// unreachable. The branch remains even after permanent session deletion.
	if w.Branch == "" {
		w.Branch = "shioji/archive-" + filepath.Base(w.Path)
		if _, err = gitRun(s.ctx, w.Path, "branch", w.Branch, w.Head); err != nil {
			return err
		}
		if _, err = gitRun(s.ctx, w.Path, "switch", w.Branch); err != nil {
			return err
		}
	}
	if status.Dirty {
		if _, err = gitRun(s.ctx, w.Path, "add", "-A"); err != nil {
			return err
		}
		staged, e := gitRun(s.ctx, w.Path, "diff", "--cached", "--name-only")
		if e != nil {
			return e
		}
		if strings.TrimSpace(staged) != "" {
			_, err = gitRun(s.ctx, w.Path, "-c", "commit.gpgsign=false", "-c", "user.name=Shioji Archive", "-c", "user.email=archive@shioji.invalid", "commit", "-m", "chore: archive backup for worktree "+filepath.Base(w.Path))
			if err != nil {
				return err
			}
		}
	}
	head, err := gitRun(s.ctx, w.Path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	w.Head = strings.TrimSpace(head)
	if err = s.store.RecordWorktreeBackup(w); err != nil {
		return err
	}
	final, err := gitRun(s.ctx, w.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if final != "" {
		return errors.New("备份后 worktree 又有修改，保留目录后重试")
	}
	// Like Lody, the backup includes tracked and non-ignored untracked files.
	// Ignored build/dependency output is disposable and is recreated on demand.
	if _, err = gitRun(s.ctx, w.CommonDir, "worktree", "remove", "--force", w.Path); err != nil {
		return err
	}
	w.Removed = true
	return s.store.SaveOwnedWorktree(w)
}

// Earlier versions encoded a creation operation digest in their owned path.
// Recover only a path backed by that durable receipt and an accepted session;
// a similarly named arbitrary directory is never adopted for deletion.
func (s *Server) discoverOwnedWorktrees(sessionID, workspaceID string) error {
	base, err := filepath.EvalSymlinks(filepath.Join(s.config.Directory, "worktrees"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	records, err := s.store.OwnedWorktrees()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, w := range records {
		known[w.Path] = true
	}
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return err
	}
	ops, err := s.store.OperationIdentities()
	if err != nil {
		return err
	}
	receipts := map[string]bool{}
	for _, op := range ops {
		sum := sha256.Sum256([]byte(op.ID))
		receipts[hex.EncodeToString(sum[:12])] = true
	}
	var result error
	for _, session := range snapshot.Sessions {
		if sessionID != "" && session.ID != sessionID || workspaceID != "" && session.WorkspaceID != workspaceID {
			continue
		}
		rel, e := filepath.Rel(base, session.Context.CWD)
		if e != nil || !filepath.IsLocal(rel) {
			continue
		}
		id := strings.Split(rel, string(filepath.Separator))[0]
		path := filepath.Join(base, id)
		if !receipts[id] || known[path] || s.worktreeTransitions[path] != nil {
			continue
		}
		if info, e := os.Lstat(path); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		release := s.beginWorktreeTransition(path)
		s.dispatchMu.Unlock()
		status, e := queryGit(s.ctx, path)
		s.dispatchMu.Lock()
		if e == nil && status.Checkout == path && status.Head != "" {
			e = s.store.SaveOwnedWorktree(store.OwnedWorktree{Path: path, CommonDir: status.CommonDir, Branch: status.Branch, Head: status.Head})
			known[path] = e == nil
		}
		release()
		result = errors.Join(result, e)
	}
	return result
}

func (s *Server) removedWorktreeStatus(q protocol.GitQuery) (domain.GitStatus, bool, error) {
	if q.SessionID == "" {
		return domain.GitStatus{}, false, nil
	}
	d, err := s.store.Session(q.SessionID, 0, 1)
	if err != nil {
		return domain.GitStatus{}, false, err
	}
	if d.Session.WorkspaceID != q.WorkspaceID {
		return domain.GitStatus{}, false, errors.New("session is not in this workspace")
	}
	records, err := s.store.OwnedWorktrees()
	if err != nil {
		return domain.GitStatus{}, false, err
	}
	for _, w := range records {
		if worktreeUsesPaths(w, []string{d.Session.Context.CWD}) {
			if _, e := os.Lstat(w.Path); !os.IsNotExist(e) {
				continue
			}
			names, e := s.gitRunOutsideDispatch(s.ctx, w.CommonDir, "for-each-ref", "--count=2000", "--format=%(refname:lstrip=2)", "refs/heads/")
			if e != nil {
				return domain.GitStatus{}, true, e
			}
			current, readErr := s.store.Session(q.SessionID, 0, 1)
			if readErr != nil {
				return domain.GitStatus{}, true, readErr
			}
			if current.Session.ThreadID != d.Session.ThreadID || current.Session.Context.CWD != d.Session.Context.CWD {
				return domain.GitStatus{}, true, errors.New("会话执行目录已改变")
			}
			if _, err := os.Lstat(w.Path); !os.IsNotExist(err) {
				return domain.GitStatus{}, false, nil
			}
			latest, e := s.store.OwnedWorktrees()
			if e != nil {
				return domain.GitStatus{}, true, e
			}
			owned := false
			for _, record := range latest {
				owned = owned || record.Path == w.Path && record.CommonDir == w.CommonDir && record.Branch == w.Branch && record.Head == w.Head
			}
			if !owned {
				return domain.GitStatus{}, true, errors.New("自建目录归属已改变，请重新读取")
			}

			status := domain.GitStatus{Applicable: true, Checkout: w.Path, Repository: w.Path, CommonDir: w.CommonDir, Branch: w.Branch, Head: w.Head, DefaultBranch: w.Branch, Branches: strings.Fields(names)}
			return status, true, e
		}
	}
	return domain.GitStatus{}, false, nil
}
