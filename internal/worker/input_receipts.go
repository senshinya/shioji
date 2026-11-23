package worker

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

func (s *Server) claimInput(gate *runtimeEventGate, runtime harness.Runtime, op domain.Operation, commit ...func() error) error {
	if gate == nil {
		return errRuntimeRetired
	}
	if runtime.ManagedCoverage() {
		if err := gate.apply(func() error { return s.coverOperation(op.SessionID, op.ID) }); err != nil {
			return err
		}
	}
	// Drain and Stop share the short commit boundary. Process ownership I/O
	// happens above, where it cannot stall another session's admission.
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return gate.apply(func() error {
		if err := s.updateAdmission(); err != nil {
			return err
		}
		if len(commit) > 0 {
			return commit[0]()
		}
		return s.store.BeginInputHandoff(op.ID)
	})
}

func (s *Server) beginInputReceipt(id string) func() {
	s.inputReceiptMu.Lock()
	if s.inputReceipts == nil {
		s.inputReceipts = make(map[string]int)
	}
	s.inputReceipts[id]++
	s.inputReceiptMu.Unlock()
	return func() {
		s.inputReceiptMu.Lock()
		if s.inputReceipts[id] == 1 {
			delete(s.inputReceipts, id)
		} else {
			s.inputReceipts[id]--
		}
		s.inputReceiptMu.Unlock()
	}
}

func (s *Server) inputReceipt(q protocol.OperationQuery) (domain.Operation, error) {
	if q.SealGeneration == "" || q.SealGeneration != s.store.Generation() {
		return s.store.Operation(q.ID)
	}
	if q.ID == "" || len(q.ID) > 256 || strings.HasPrefix(q.ID, "scheduled:") {
		return domain.Operation{}, errors.New("无效的提交回执标识")
	}
	receipt, err := s.store.Operation(q.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	}
	// Only recording a negative receipt is a write. Lookup of an existing
	// receipt remains available during maintenance and drain.
	s.maintenance.RLock()
	defer s.maintenance.RUnlock()
	if err := s.updateAdmission(); err != nil {
		return domain.Operation{}, err
	}
	s.inputReceiptMu.Lock()
	defer s.inputReceiptMu.Unlock()
	if s.inputReceipts[q.ID] != 0 {
		return s.store.Operation(q.ID)
	}
	return s.store.SealUnreceivedInput(q.ID)
}

func awaitNativeInput(ctx context.Context, runtime harness.Runtime, receipt <-chan error) error {
	select {
	case err := <-receipt:
		return err
	case <-runtime.Done():
		select {
		case err := <-receipt:
			return err
		default:
			return &harness.DeliveryError{Uncertain: true, Message: "运行环境已结束，输入结果待核对"}
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}
