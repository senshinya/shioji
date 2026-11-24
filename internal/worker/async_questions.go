package worker

import (
	"database/sql"
	"errors"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (s *Server) replyToQuestion(in protocol.QuestionReplyInput) (domain.Operation, error) {
	var none domain.Operation
	if in.SessionID == "" || in.OperationID == "" || len(in.OperationID) > 128 ||
		strings.HasPrefix(in.OperationID, "scheduled:") || len(in.Text) > 16<<10 || strings.TrimSpace(in.Text) == "" {
		return none, errors.New("请输入有效的答案")
	}
	defer s.beginInputReceipt(in.OperationID)()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if _, err := s.store.Operation(in.OperationID); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return none, err
		}
		return none, errors.New("此提交标识已使用，请查询原回执")
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return none, err
	}
	session := d.Session
	if session.Archived || session.ReadOnlyHistory() || session.ExecutionGuard != "" {
		return none, errors.New("此会话暂不能提交答案")
	}
	question, err := s.store.Question(session.ID, in.ItemID, in.QuestionID)
	if err != nil {
		return none, err
	}
	if question.Secret {
		return none, errors.New("请在 Agent 的原生界面回答保密问题")
	}
	input := domain.Submission{OperationID: in.OperationID, WorkspaceID: session.WorkspaceID, SessionID: session.ID,
		HarnessID: session.HarnessID, Config: in.Config, Text: strings.TrimSpace(in.Text),
		QuestionReply: &domain.QuestionReply{ItemID: in.ItemID, QuestionID: question.ID, Title: question.Text}}
	if session.Status.Active() {
		for _, turn := range d.Turns {
			if turn.Status == domain.ExecutionRunning && turn.Presentation != "compaction" {
				return s.steerLocked(protocol.SteerInput{Input: input, TurnID: turn.ID}, 0)
			}
		}
		return none, errors.New("请等待当前轮次确认后再提交答案")
	}
	return s.submitLocked(input, 0, false)
}
