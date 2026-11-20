package worker

import (
	"database/sql"
	"errors"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

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
