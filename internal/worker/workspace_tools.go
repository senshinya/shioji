package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"slices"
	"time"
)

func (s *Server) inspectToolServices(in protocol.ToolInspectionInput) (domain.ToolServiceInspection, error) {
	empty := domain.ToolServiceInspection{}
	if in.HarnessID == "" || in.WorkspaceID == "" || len(in.Directory) > 4096 || len(in.SessionID) > 256 {
		return empty, errors.New("MCP 查询范围无效")
	}
	roots, err := s.referenceRoots(protocol.GitQuery{WorkspaceID: in.WorkspaceID, SessionID: in.SessionID})
	if err != nil {
		return empty, err
	}
	launch := domain.LaunchContext{CWD: roots[0]}
	var session domain.Session
	if in.SessionID != "" {
		if in.Directory != "" {
			return empty, errors.New("会话使用已接纳目录，不能覆盖查询目录")
		}
		detail, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return empty, err
		}
		session = detail.Session
		if session.HarnessID != in.HarnessID {
			return empty, errors.New("会话不属于此 Agent")
		}
		launch = session.Context
	} else if in.Directory != "" {
		if !slices.Contains(roots, in.Directory) {
			return empty, errors.New("目录不属于此工作区")
		}
		launch.CWD = in.Directory
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	// Never acquire a session runtime through the recovery/start path.
	if in.SessionID != "" {
		r := s.currentRuntime(in.SessionID)
		if r != nil && !r.Stopped() && r.Readiness().HarnessID == in.HarnessID && r.Observes(session) {
			inspector, ok := r.(harness.ToolServiceInspector)
			if !ok {
				return empty, harness.Unsupported("tool-service-inspection")
			}
			out, err := inspector.InspectToolServices(ctx, launch, session.ThreadID)
			current, e := s.store.Session(in.SessionID, 0, 1)
			if e != nil || current.Session.ThreadID != session.ThreadID || current.Session.Context.CWD != launch.CWD {
				return empty, errors.New("会话上下文已变化，请重新核对")
			}
			return out, err
		}
	}
	r, release, err := s.borrowCatalog(in.HarnessID)
	if err != nil {
		return empty, err
	}
	defer release()
	inspector, ok := r.(harness.ToolServiceInspector)
	if !ok {
		return empty, harness.Unsupported("tool-service-inspection")
	}
	out, err := inspector.InspectToolServices(ctx, launch, "")
	if in.SessionID != "" {
		out.RuntimeKnown = false
		out.Warnings = append(out.Warnings, "此会话没有可查询的运行实例，连接状态未知")
	}
	return out, err
}
