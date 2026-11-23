package worker

import (
	"errors"
	"log"

	"shioji/internal/domain"
	"shioji/internal/store"
)

func (s *Server) receiveHomeSubmission(in domain.Submission) (domain.Operation, error) {
	if in.OperationID == "" || len(in.OperationID) > 256 || in.ValidateContent() != nil || in.SessionID != "" || in.Continue {
		return domain.Operation{}, errors.New("invalid new session input")
	}
	if err := store.CheckSubmissionJournal(in); err != nil {
		return domain.Operation{}, err
	}
	defer s.beginInputReceipt(in.OperationID)()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.updateAdmission(); err != nil {
		return domain.Operation{}, err
	}
	workspace, err := s.store.Workspace(in.WorkspaceID)
	if err != nil {
		return domain.Operation{}, err
	}
	if workspace.Archived {
		return domain.Operation{}, errors.New("请先恢复工作区")
	}
	op, fresh, err := s.store.ReceiveHomeSubmission(in)
	if err != nil || !fresh {
		return op, err
	}
	release := s.reservePreparation("")
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer release()
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		_, err := s.prepareSubmissionLocked(in, 0, false, &workspace, nil)
		if err != nil {
			if saveErr := s.store.RejectReceivedSubmission(op.ID, err.Error()); saveErr != nil {
				log.Printf("save submission preparation result: %v", saveErr)
				s.cancel()
			}
		}
	}()
	return op, nil
}
