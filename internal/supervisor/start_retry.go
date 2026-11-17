package supervisor

import (
	"errors"
	"os/exec"
	"time"
)

// These startup-only exits mean a dependency/owned endpoint is not yet free.
const ExitRetryableStartup = 2
const ExitAlreadyOwned = 3

func retryableStartup(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && (exit.ExitCode() == ExitRetryableStartup || exit.ExitCode() == ExitAlreadyOwned)
}

type permanentStartup struct{ error }

func (e *permanentStartup) Unwrap() error { return e.error }

// PermanentStartup keeps invalid signatures, formats and ownership uncertainty
// out of the ordinary retry loop. Temporary launch/preparation failures retry.
func PermanentStartup(err error) error {
	if err == nil {
		return nil
	}
	return &permanentStartup{err}
}

func (h *host) startSelectedRetry(initial string, paused bool, operation string) error {
	for {
		h.mu.Lock()
		previous := h.child
		h.mu.Unlock()
		err := h.startSelected(initial, paused, operation)
		if err == nil || h.ctx.Err() != nil {
			return err
		}
		var permanent *permanentStartup
		if paused || errors.As(err, &permanent) {
			return err
		}
		h.mu.Lock()
		var started time.Time
		if h.child != nil && h.child != previous {
			started = h.child.startedAt
		}
		var attempt int
		var exhausted bool
		if retryableStartup(err) {
			attempt = h.retry.attempt
			h.retry.attempt++
		} else {
			attempt, exhausted = h.retry.failed(time.Now(), started)
		}
		h.readyDone = false
		h.health.Phase, h.health.Error = "restarting", err.Error()
		if exhausted {
			h.blocked = true
			h.health.Phase, h.health.CrashLoop = "blocked", true
		}
		saved := h.saveHealth()
		h.mu.Unlock()
		if saved != nil {
			return PermanentStartup(saved)
		}
		if exhausted {
			return err
		}
		timer := time.NewTimer(crashRetryDelay(attempt, h.options.RetryMin, h.options.RetryMax, 0))
		select {
		case <-h.ctx.Done():
			timer.Stop()
			return h.ctx.Err()
		case <-timer.C:
		}
	}
}

func (h *host) blockStartup(err error) error {
	h.mu.Lock()
	exhausted := h.health.CrashLoop
	h.mu.Unlock()
	if exhausted {
		return nil
	}
	return h.blockHealth(err)
}
