package worker

import (
	"context"
	"shioji/internal/harness"
	"time"
)

type nativeRuntimeResult struct {
	received bool
	err      error
}

// Caller holds dispatchMu. The captured runtime owns this native wait, not the
// device admission lock. A retired gate releases the caller even if the native
// implementation has not returned; its buffered result cannot publish state.
func (s *Server) runtimeCallOutsideDispatch(ctx context.Context, id string, r harness.Runtime, call func() (bool, error)) (bool, error) {
	s.runtimeMu.RLock()
	gate := s.runtimeEvents[id]
	s.runtimeMu.RUnlock()
	var retired <-chan struct{}
	if gate != nil {
		retired = gate.done
	}
	result := make(chan nativeRuntimeResult, 1)
	s.dispatchMu.Unlock()
	go func() {
		if err := ctx.Err(); err != nil {
			result <- nativeRuntimeResult{err: err}
			return
		}
		if r.Stopped() || gate != nil && !gate.Load() {
			result <- nativeRuntimeResult{err: errRuntimeRetired}
			return
		}
		received, err := call()
		result <- nativeRuntimeResult{received, err}
	}()
	var out nativeRuntimeResult
	select {
	case out = <-result:
	case <-ctx.Done():
		out.err = &harness.DeliveryError{Uncertain: true, Message: "原生操作尚未确认，不会自动重发：" + ctx.Err().Error()}
	case <-r.Done():
		out.err = &harness.DeliveryError{Uncertain: true, Message: "原运行环境已结束，操作结果尚未确认"}
	case <-retired:
		out.err = &harness.DeliveryError{Uncertain: true, Message: "原运行环境已退休，操作结果尚未确认"}
	}
	s.dispatchMu.Lock()
	return out.received, out.err
}
func (s *Server) capturedRuntimeCurrent(id string, r harness.Runtime, source string) bool {
	current := s.currentRuntime(id)
	if current == nil || current.Done() != r.Done() || current.Stopped() {
		return false
	}
	d, err := s.store.Session(id, 0, 1)
	return err == nil && d.Session.ThreadID == source && r.Observes(d.Session)
}
func (s *Server) respondRequest(in AnswerInput) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.updateAdmission(); err != nil {
		return err
	}
	r := s.currentRuntime(in.SessionID)
	if r == nil || r.Readiness().State != "ready" {
		return workNotSent(harness.Unsupported("request-response"))
	}
	session, err := s.store.ValidateAnswer(in.SessionID, in.Answer)
	if err != nil {
		return err
	}
	if !r.Observes(session) {
		return workNotSent(errRuntimeRetired)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	_, err = s.runtimeCallOutsideDispatch(ctx, in.SessionID, r, func() (bool, error) { return false, r.Respond(in.Answer) })
	if !s.capturedRuntimeCurrent(in.SessionID, r, session.ThreadID) {
		return &harness.DeliveryError{Uncertain: true, Message: "请求的原运行环境已退休，回答结果尚未确认"}
	}
	return err
}
