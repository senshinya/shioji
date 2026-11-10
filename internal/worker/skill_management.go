package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"time"
)

func (s *Server) manageSkills(method string, in protocol.SkillManagementInput) (any, error) {
	if in.HarnessID == "" || len(in.ID) > 4096 || len(in.Revision) > 512 {
		return nil, errors.New("Skill 管理请求无效")
	}
	if method == "runtime.skills.change" {
		s.toolConfigMu.Lock()
		defer s.toolConfigMu.Unlock()
	}
	r, release, err := s.borrowCatalog(in.HarnessID)
	if err != nil {
		return nil, err
	}
	defer release()
	manager, ok := r.(harness.SkillManager)
	if !ok || !r.Readiness().Capabilities.SkillManagement {
		return nil, harness.Unsupported("skill-management")
	}
	launch := domain.LaunchContext{}
	if in.WorkspaceID != "" {
		roots, e := s.referenceRoots(protocol.GitQuery{WorkspaceID: in.WorkspaceID})
		if e != nil {
			return nil, e
		}
		launch.CWD, launch.AdditionalDirectories = roots[0], roots[1:]
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if method == "runtime.skills.list" {
		return manager.SkillInventory(ctx, launch)
	}
	catalog, err := manager.SkillInventory(ctx, launch)
	if err != nil {
		return nil, err
	}
	for _, entry := range catalog.Entries {
		if entry.ID != in.ID {
			continue
		}
		if method == "runtime.skills.read" {
			return manager.ReadSkill(ctx, launch, in.ID)
		}
		if !entry.CanEnable || (in.WorkspaceID != "" && entry.Scope != domain.SkillProject) || (in.WorkspaceID == "" && entry.Scope == domain.SkillProject) {
			return nil, errors.New("此 Skill 在当前入口只读，请在所属设备设置中管理")
		}
		if in.Revision == "" || in.Revision != catalog.Revision {
			return nil, errors.New("配置已变化，请刷新后重试")
		}
		return manager.ChangeSkill(ctx, launch, in.SkillChange)
	}
	return nil, errors.New("Skill 已不在当前目录中，请刷新")
}
