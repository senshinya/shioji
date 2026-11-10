package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

type nativeManagedSkill struct {
	Name, Path, Description, Scope string
	Enabled                        bool
	PluginID                       *string
	ShortDescription               *string
	Interface                      *struct{ ShortDescription *string }
}

func skillID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}
func (r *Runtime) skillConfiguration(ctx context.Context, cwd string) (nativeConfiguration, error) {
	var config nativeConfiguration
	var directory *string
	if cwd != "" {
		directory = &cwd
	}
	err := r.call(ctx, "config/read", map[string]any{"includeLayers": true, "cwd": directory}, &config)
	return config, err
}
func skillUserConfiguration(c nativeConfiguration) (string, string, []map[string]json.RawMessage) {
	for _, layer := range c.Layers {
		if layer.Name.Type != "user" || layer.Name.Profile != "" || layer.DisabledReason != nil {
			continue
		}
		var content struct {
			Skills struct {
				Config []map[string]json.RawMessage `json:"config"`
			} `json:"skills"`
		}
		if json.Unmarshal(layer.Config, &content) != nil {
			return "", "", nil
		}
		return layer.Name.File, layer.Version, content.Skills.Config
	}
	return "", "", nil
}
func skillScope(source nativeManagedSkill) domain.SkillScope {
	if source.PluginID != nil && *source.PluginID != "" {
		return domain.SkillManaged
	}
	switch source.Scope {
	case "repo":
		return domain.SkillProject
	case "user":
		return domain.SkillUser
	case "system":
		return domain.SkillBuiltIn
	case "admin":
		return domain.SkillManaged
	}
	return domain.SkillUnknown
}
func skillConfigurationReadOnly(c nativeConfiguration) bool {
	for _, layer := range c.Layers {
		if layer.DisabledReason != nil || layer.Name.Type == "user" && layer.Name.Profile == "" {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(layer.Config, &fields) == nil && len(fields["skills"]) > 0 {
			return true
		}
	}
	return false
}
func (r *Runtime) managedSkills(ctx context.Context, launch domain.LaunchContext) (domain.SkillInventory, nativeConfiguration, error) {
	out := domain.SkillInventory{Entries: []domain.ManagedSkill{}}
	if !r.supports("skills/list") {
		return out, nativeConfiguration{}, harness.Unsupported("skill-management")
	}
	cwd := launch.CWD
	if cwd == "" {
		cwd = environmentValue(r.cmd.Env, "HOME")
		if cwd == "" {
			return out, nativeConfiguration{}, errors.New("设备未提供用户目录")
		}
	}
	var response struct {
		Data []struct {
			CWD    string
			Skills []nativeManagedSkill
			Errors []struct{ Path, Message string }
		}
	}
	if err := r.call(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true}, &response); err != nil {
		return out, nativeConfiguration{}, errors.New("无法读取 Skill 列表，请重试")
	}
	config, err := r.skillConfiguration(ctx, launch.CWD)
	if err != nil {
		return out, config, errors.New("无法读取 Skill 配置，请重试")
	}
	file, version, _ := skillUserConfiguration(config)
	writable := file != "" && version != "" && r.supports("config/batchWrite") && !skillConfigurationReadOnly(config)
	out.Revision = toolConfigurationRevision(config)
	seen := map[string]bool{}
	matched := false
	for _, result := range response.Data {
		if sourceExecutionPath(result.CWD) != sourceExecutionPath(cwd) {
			return out, config, errors.New("Skill 列表与执行目录不一致")
		}
		matched = true
		for _, problem := range result.Errors {
			if launch.CWD != "" {
				out.Warnings = append(out.Warnings, bounded(problem.Path+": "+problem.Message))
			}
		}
		for _, source := range result.Skills {
			scope := skillScope(source)
			if launch.CWD == "" && scope == domain.SkillProject {
				continue
			}
			if source.Name == "" || !filepath.IsAbs(source.Path) || seen[source.Path] {
				continue
			}
			seen[source.Path] = true
			description := source.Description
			if source.ShortDescription != nil {
				description = *source.ShortDescription
			}
			if source.Interface != nil && source.Interface.ShortDescription != nil {
				description = *source.Interface.ShortDescription
			}
			entry := domain.ManagedSkill{SkillReference: domain.SkillReference{ID: skillID(source.Path), Name: source.Name}, Description: boundedTo(strings.TrimSpace(description), 1024), Scope: scope, Path: source.Path, Directory: filepath.Dir(filepath.Dir(source.Path)), Enabled: source.Enabled}
			entry.CanEnable = writable && (scope == domain.SkillUser || scope == domain.SkillProject) && (launch.CWD == "" || scope == domain.SkillProject)
			if !entry.CanEnable {
				entry.ReadOnlyReason = "此来源只读"
				if launch.CWD != "" && scope != domain.SkillProject {
					entry.ReadOnlyReason = "在设备设置中管理"
				}
			}
			out.Entries = append(out.Entries, entry)
		}
	}
	if !matched {
		return out, config, errors.New("运行服务没有返回此目录的 Skill 列表")
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Directory != b.Directory {
			return a.Directory < b.Directory
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	return out, config, nil
}
func (r *Runtime) SkillInventory(ctx context.Context, launch domain.LaunchContext) (domain.SkillInventory, error) {
	out, _, err := r.managedSkills(ctx, launch)
	return out, err
}
func (r *Runtime) ReadSkill(ctx context.Context, launch domain.LaunchContext, id string) (domain.SkillDocument, error) {
	catalog, err := r.SkillInventory(ctx, launch)
	if err != nil {
		return domain.SkillDocument{}, err
	}
	for _, entry := range catalog.Entries {
		if entry.ID != id {
			continue
		}
		f, err := os.OpenFile(entry.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return domain.SkillDocument{}, errors.New("无法读取 Skill 文件，请刷新后重试")
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return domain.SkillDocument{}, errors.New("Skill 来源不是普通文件")
		}
		data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
		if err != nil || len(data) > 1<<20 || !utf8.Valid(data) {
			return domain.SkillDocument{}, errors.New("Skill 内容过大或不是 UTF-8 文本")
		}
		return domain.SkillDocument{ID: id, Text: string(data)}, nil
	}
	return domain.SkillDocument{}, errors.New("Skill 已不可用，请刷新")
}
func (r *Runtime) ChangeSkill(ctx context.Context, launch domain.LaunchContext, in domain.SkillChange) (domain.SkillChangeResult, error) {
	catalog, config, err := r.managedSkills(ctx, launch)
	if err != nil {
		return domain.SkillChangeResult{}, err
	}
	if in.Revision == "" || in.Revision != catalog.Revision {
		return domain.SkillChangeResult{}, errors.New("配置已变化，请刷新后重试")
	}
	var target *domain.ManagedSkill
	for i := range catalog.Entries {
		if catalog.Entries[i].ID == in.ID {
			target = &catalog.Entries[i]
			break
		}
	}
	if target == nil || !target.CanEnable {
		return domain.SkillChangeResult{}, errors.New("此 Skill 在当前入口无法修改")
	}
	file, version, entries := skillUserConfiguration(config)
	updated := false
	enabled, _ := json.Marshal(in.Enabled)
	for _, entry := range entries {
		var path string
		json.Unmarshal(entry["path"], &path)
		if path == target.Path {
			entry["enabled"] = enabled
			updated = true
		}
	}
	if !updated {
		path, _ := json.Marshal(target.Path)
		entries = append(entries, map[string]json.RawMessage{"path": path, "enabled": enabled})
	}
	var result struct {
		Status     string
		Overridden json.RawMessage `json:"overriddenMetadata"`
	}
	err = r.call(ctx, "config/batchWrite", map[string]any{"filePath": file, "expectedVersion": version, "reloadUserConfig": true, "edits": []map[string]any{{"keyPath": "skills.config", "value": entries, "mergeStrategy": "replace"}}}, &result)
	if err != nil {
		return domain.SkillChangeResult{}, errors.New("保存结果未确认，请刷新后核对")
	}
	if result.Status != "ok" || len(result.Overridden) > 0 && string(result.Overridden) != "null" {
		return domain.SkillChangeResult{}, errors.New("配置受到其他配置层限制，请刷新核对")
	}
	return domain.SkillChangeResult{Saved: true, Enabled: in.Enabled}, nil
}

var _ harness.SkillManager = (*adapterRuntime)(nil)
