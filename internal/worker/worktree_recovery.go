package worker

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/store"
)

// Caller owns this repository's transition and dispatchMu. Native waits release
// only dispatchMu; each mutation rechecks the preparation that owns the gate.
func (s *Server) ownedBranchHeads(common string) (map[string]string, error) {
	text, err := s.gitRunOutsideDispatch(s.ctx, common, "for-each-ref", "--format=%(refname:lstrip=2)%09%(objectname)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if name, head, ok := strings.Cut(line, "\t"); ok {
			heads[name] = strings.TrimSpace(head)
		}
	}
	return heads, nil
}

func availableWorktreeBranch(base string, heads map[string]string) string {
	if heads[base] == "" {
		return base
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d", base, n)
		if heads[name] == "" {
			return name
		}
	}
}

func worktreeBranchConflict(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "branch named") && strings.Contains(text, "already exists") || strings.Contains(text, "cannot lock ref")
}

func (s *Server) addOwnedWorktree(w store.OwnedWorktree, create bool, valid func() error) error {
	run := func() error {
		if err := valid(); err != nil {
			return err
		}
		args := []string{"worktree", "add"}
		target := w.Branch
		if create {
			args = append(args, "-b", w.Branch)
			target = w.Head
		} else if w.Branch == "" {
			args = append(args, "--detach")
			target = w.Head
		}
		_, err := s.gitRunOutsideDispatch(s.ctx, w.CommonDir, append(args, w.Path, target)...)
		return err
	}
	err := run()
	if err == nil || create && worktreeBranchConflict(err) {
		return err
	}
	text := strings.ToLower(err.Error())
	stale := false
	for _, hint := range []string{"missing", "locked", "administrative", "not a working tree", "already exists"} {
		stale = stale || strings.Contains(text, hint)
	}
	if !stale {
		return err
	}
	if e := valid(); e != nil {
		return e
	}
	if _, e := s.gitRunOutsideDispatch(s.ctx, w.CommonDir, "worktree", "prune"); e != nil {
		return errors.Join(err, e)
	}
	if create {
		heads, e := s.ownedBranchHeads(w.CommonDir)
		if e != nil {
			return e
		}
		if head := heads[w.Branch]; head != "" {
			if head != w.Head {
				return errors.New("准备分支已改变，保留原目录和引用")
			}
			// This invocation allocated the absent branch and did not receive a
			// name conflict. Reattach its partial add, never somebody else's ref.
			create = false
		}
	}
	return run()
}

func (s *Server) prepareOwnedWorktree(in domain.Submission, status domain.GitStatus, dest, baseBranch, branchPrefix string, valid func() error) error {
	records, err := s.store.OwnedWorktrees()
	if err != nil {
		return err
	}
	var owned *store.OwnedWorktree
	for _, w := range records {
		if w.Path == dest {
			owned = &w
			break
		}
	}
	if owned != nil && (owned.CommonDir != status.CommonDir || owned.CreationOperation != in.OperationID || owned.CreationBase != baseBranch) {
		return errors.New("worktree 准备归属或起始分支已改变，保留原目录")
	}
	info, pathErr := os.Lstat(dest)
	if pathErr == nil {
		if owned == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("worktree 目录已存在且不属于本次创建，保留原件")
		}
		actual, err := s.queryGitOutsideDispatch(s.ctx, dest)
		if err != nil {
			return err
		}
		if actual.Checkout != dest || actual.CommonDir != owned.CommonDir || actual.Branch != owned.Branch || actual.Head != owned.Head {
			return errors.New("部分创建的 worktree 身份已改变，保留原件")
		}
		return valid()
	}
	if !os.IsNotExist(pathErr) {
		return pathErr
	}
	heads, err := s.ownedBranchHeads(status.CommonDir)
	if err != nil {
		return err
	}
	if owned == nil {
		if heads[baseBranch] == "" {
			return errors.New("起始分支已不可用")
		}
		owned = &store.OwnedWorktree{Path: dest, CommonDir: status.CommonDir, Branch: availableWorktreeBranch(branchPrefix, heads), Head: heads[baseBranch], CreationOperation: in.OperationID, CreationBase: baseBranch, Removed: true}
	} else if heads[owned.Branch] != "" {
		// Across calls an existing ref alone cannot prove who created it.
		// Preserve it and cut a fresh branch from the saved preparation HEAD.
		owned.Branch = availableWorktreeBranch(branchPrefix, heads)
	}
	if err = valid(); err != nil {
		return err
	}
	// Persist provenance before Git can create either the branch or directory.
	// The common-dir transition also keeps the independent collector away.
	if err = s.store.SaveOwnedWorktree(*owned); err != nil {
		return err
	}
	if err = s.store.MarkSubmissionPreparation(in.OperationID, domain.SubmissionCreatingWorktree); err != nil {
		return err
	}
	err = s.addOwnedWorktree(*owned, true, valid)
	if err != nil && worktreeBranchConflict(err) {
		// A concurrent Git user won that name. Allocate once more; never adopt
		// or remove the conflicting ref even if it points at the same commit.
		heads, e := s.ownedBranchHeads(status.CommonDir)
		if e != nil {
			return e
		}
		owned.Branch = availableWorktreeBranch(branchPrefix, heads)
		if e = valid(); e != nil {
			return e
		}
		if e = s.store.SaveOwnedWorktree(*owned); e != nil {
			return e
		}
		err = s.addOwnedWorktree(*owned, true, valid)
	}
	if err != nil {
		return err
	}
	return nil
}

// Only a known local rejection reaches here. An unknown preparation result is
// retained for the same operation's retry, just as Lody retains an unknown owner.
func (s *Server) removeUnacceptedWorktree(in domain.Submission) {
	if op, e := s.store.Operation(in.OperationID); !errors.Is(e, sql.ErrNoRows) && (e != nil || op.State != domain.OperationPreparing) {
		return
	}
	sum := sha256.Sum256([]byte(in.OperationID))
	base, e := filepath.EvalSymlinks(filepath.Join(s.config.Directory, "worktrees"))
	if e != nil {
		return
	}
	dest := filepath.Join(base, hex.EncodeToString(sum[:12]))
	records, e := s.store.OwnedWorktrees()
	if e != nil {
		return
	}
	for _, w := range records {
		if w.Path != dest || w.CreationOperation != in.OperationID {
			continue
		}
		release, e := s.beginGitTransition(dest, w.CommonDir)
		if e != nil {
			return
		}
		defer release()
		if e = s.ownedWorktreePath(w); e != nil {
			return
		}
		status, e := s.queryGitOutsideDispatch(s.ctx, dest)
		if e != nil || status.Checkout != dest || status.CommonDir != w.CommonDir || status.Branch != w.Branch || status.Head != w.Head {
			return
		}
		if op, lookup := s.store.Operation(in.OperationID); !errors.Is(lookup, sql.ErrNoRows) && (lookup != nil || op.State != domain.OperationPreparing) {
			return
		}
		snapshot, e := s.store.Snapshot()
		if e != nil {
			return
		}
		for _, workspace := range snapshot.Workspaces {
			if worktreeUsesPaths(w, workspace.Folders) {
				return
			}
		}
		for _, session := range snapshot.Sessions {
			if store.WorktreeSession(w, session) {
				return
			}
		}
		if e = s.updateAdmission(); e != nil {
			return
		}
		// No force: unexpected files or another Git user preserve the checkout.
		if _, e = s.gitRunOutsideDispatch(s.ctx, w.CommonDir, "worktree", "remove", dest); e != nil {
			return
		}
		_, _ = s.gitRunOutsideDispatch(s.ctx, w.CommonDir, "branch", "-d", "--", w.Branch)
		_ = s.store.ForgetOwnedWorktree(dest)
		return
	}
}
