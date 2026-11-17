package managedexec

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"time"
)

// Terminate retires only registered groups. All signals are sent before waiting
// so each group shares the caller's deadline. Failed attempts retain tickets.
func (s *Scope) Terminate(ctx context.Context, force bool) error {
	if !force || !s.ForceAllowed() {
		return s.Stop(ctx)
	}
	if err := s.Fence(); err != nil {
		return err
	}
	records, err := s.tickets()
	if err != nil {
		return err
	}
	var result error
	for _, t := range records {
		if err = ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if t.Group <= 1 || t.Group == unix.Getpgrp() {
			result = errors.Join(result, errors.New("refusing to signal controlling worker group"))
			continue
		}
		if err = knownMemberStayedInGroup(t); err != nil {
			result = errors.Join(result, err)
			continue
		}
		members, e := groupMembers(t.Group)
		if e != nil {
			result = errors.Join(result, e)
			continue
		}
		if len(members) == 0 {
			continue
		}
		if !anchorPresent(t, members) {
			result = errors.Join(result, errors.New("managed group ownership is no longer verifiable"))
			continue
		}
		if e = unix.Kill(-t.Group, unix.SIGKILL); e != nil && !errors.Is(e, unix.ESRCH) {
			// A signal error is provisional: final observation decides disappearance.
			if after, observe := groupMembers(t.Group); observe != nil || len(after) > 0 {
				result = errors.Join(result, e, observe)
			}
		}
	}
	// Cancellation can race our observations and signals. Keep any earlier
	// errors until the common deadline, but let a fresh proof that this exact
	// fenced generation is gone decide success. No unverified group is signalled.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = s.VerifyStopped()
		if err == nil {
			for _, t := range records {
				t.State = LaunchStopped
				if e := writeJSON(s.launchPath(t.ID), t); e != nil {
					return e
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), result, err)
		case <-ticker.C:
		}
	}
}
