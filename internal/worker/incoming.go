package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
)

// Host callbacks enter the same durable queue as ordinary queued messages. They
// cannot select another workspace/configuration, resume a paused queue, or start
// a harness turn directly. SourceRef is only compared as an opaque identity.
func (s *Server) receiveHarnessMessage(ctx context.Context, harnessID, session, generation string, input harness.IncomingMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.ctx.Err() != nil || input.SessionID != session {
		return errors.New("callback session is unavailable")
	}
	scope, err := s.executions.Existing(session)
	if err != nil || scope == nil || scope.Generation != generation || !scope.Admitting() {
		return errors.New("callback execution owner has retired")
	}
	runtime := s.currentRuntime(session)
	if runtime == nil || !runtime.ExecutionReady() {
		return errors.New("callback runtime is unavailable")
	}
	d, err := s.store.Session(session, 0, 1)
	if err != nil {
		return err
	}
	if d.Session.HarnessID != harnessID || input.SourceRef == "" || d.Session.ThreadID != input.SourceRef {
		return errors.New("callback history is no longer current")
	}
	_, err = s.enqueueLocked(domain.Submission{OperationID: input.OperationID, SessionID: session, WorkspaceID: d.Session.WorkspaceID, HarnessID: harnessID, Text: input.Text, Config: d.Session.Config})
	return err
}
