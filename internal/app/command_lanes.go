package app

import (
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func urgentLane(job Job) bool {
	return job.Method == "turn.interrupt" || job.Method == "request.respond" || job.Method == "operation"
}

func commandLane(job Job) string {
	switch in := job.Input.(type) {
	case domain.Submission:
		if in.SessionID == "" {
			return "workspace:" + in.WorkspaceID
		}
		return "session:" + in.SessionID
	case protocol.SteerInput:
		return "session:" + in.Input.SessionID
	case protocol.AnswerInput:
		return "response:" + in.SessionID
	case protocol.QueueInput:
		return "session:" + in.SessionID
	case domain.ContextActionRequest:
		return "session:" + in.SessionID
	case domain.GoalRequest:
		return "session:" + in.SessionID
	case domain.SessionReloadRequest:
		return "session:" + in.SessionID
	case domain.WorkInput:
		return "work:" + in.SessionID
	case domain.HistoryChange:
		return "session:" + in.SessionID
	case protocol.RecoveryInput:
		return "session:" + in.SessionID
	case protocol.OperationQuery:
		return "receipt:" + in.ID
	}
	if job.Method == "turn.interrupt" {
		in, _ := job.Input.(map[string]string)
		return "stop:" + in["sessionId"]
	}
	return ""
}

func commandTimeout(method string) time.Duration {
	switch method {
	case "conversation.import":
		return 60 * time.Second
	case "session.reconcile":
		return 25 * time.Second
	case "session.fork", "session.restart":
		return 45 * time.Second
	}
	if strings.HasPrefix(method, "runtime.tools.") {
		return 45 * time.Second
	}
	return 10 * time.Second
}
