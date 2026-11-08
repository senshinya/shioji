package codex

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
	"sort"
	"strings"
)

func (r *adapterRuntime) Skills(ctx context.Context, launch domain.LaunchContext) (domain.SkillCatalog, error) {
	if !r.supports("skills/list") {
		return domain.SkillCatalog{}, harness.Unsupported("skills")
	}
	var response struct {
		Data []struct {
			CWD    string
			Skills []struct {
				Name, Path, Description string
				Enabled                 bool
				ShortDescription        *string
				Interface               *struct{ ShortDescription *string }
			}
			Errors []struct{ Message string }
		}
	}
	if err := r.call(ctx, "skills/list", map[string]any{"cwds": []string{launch.CWD}, "forceReload": true}, &response); err != nil {
		return domain.SkillCatalog{}, err
	}
	out := domain.SkillCatalog{Skills: []domain.Skill{}}
	seen := map[string]bool{}
	matched := false
	for _, entry := range response.Data {
		if sourceExecutionPath(entry.CWD) != launch.CWD {
			return out, errors.New("Skill 目录与执行目录不一致")
		}
		matched = true
		for _, problem := range entry.Errors {
			out.Warnings = append(out.Warnings, bounded(problem.Message))
		}
		for _, skill := range entry.Skills {
			if !skill.Enabled || skill.Name == "" || skill.Path == "" || seen[skill.Path] {
				continue
			}
			seen[skill.Path] = true
			description := skill.Description
			if skill.ShortDescription != nil {
				description = *skill.ShortDescription
			}
			if skill.Interface != nil && skill.Interface.ShortDescription != nil {
				description = *skill.Interface.ShortDescription
			}
			out.Skills = append(out.Skills, domain.Skill{SkillReference: domain.SkillReference{ID: skill.Path, Name: skill.Name}, Description: boundedTo(strings.TrimSpace(description), 1024)})
		}
	}
	if !matched {
		return out, errors.New("运行服务没有返回此目录的 Skill 列表")
	}
	sort.Slice(out.Skills, func(i, j int) bool {
		if out.Skills[i].Name == out.Skills[j].Name {
			return out.Skills[i].ID < out.Skills[j].ID
		}
		return out.Skills[i].Name < out.Skills[j].Name
	})
	return out, nil
}
func skillInputs(refs []domain.SkillReference) []wire.Input {
	inputs := make([]wire.Input, 0, len(refs))
	for _, ref := range refs {
		inputs = append(inputs, wire.Input{Type: "skill", Name: ref.Name, Path: ref.ID})
	}
	return inputs
}
