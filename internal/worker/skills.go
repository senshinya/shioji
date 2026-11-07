package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"time"
)

func (s *Server) listSkills(in protocol.SkillQuery) (domain.SkillCatalog, error) {
	var out domain.SkillCatalog
	runtime, err := s.inputRuntime(domain.Submission{WorkspaceID: in.WorkspaceID, SessionID: in.SessionID, HarnessID: in.HarnessID})
	if err != nil {
		return out, err
	}
	provider, ok := runtime.(harness.SkillProvider)
	if !ok || !runtime.Readiness().Capabilities.Skills {
		return out, harness.Unsupported("skills")
	}
	roots, err := s.referenceRoots(in.GitQuery)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	return provider.Skills(ctx, domain.LaunchContext{CWD: roots[0], AdditionalDirectories: roots[1:]})
}
func (s *Server) validateSkills(in domain.Submission) error {
	if len(in.Skills) == 0 {
		return nil
	}
	catalog, err := s.listSkills(protocol.SkillQuery{GitQuery: protocol.GitQuery{WorkspaceID: in.WorkspaceID, SessionID: in.SessionID}, HarnessID: in.HarnessID})
	if err != nil {
		return err
	}
	for _, ref := range in.Skills {
		found := false
		for _, skill := range catalog.Skills {
			if skill.SkillReference == ref {
				found = true
				break
			}
		}
		if !found {
			return errors.New("所选 Skill 已不可用，请移除后重新选择")
		}
	}
	return nil
}
