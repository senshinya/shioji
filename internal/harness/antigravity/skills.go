package antigravity

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
)

type skillDirectory struct {
	base, relative string
	scope          domain.SkillScope
	priority       bool // CLI-only and shared skills outrank the other locations.
}

func skillProfileDirectory(env []string) string {
	if p := systemexec.EnvironmentValue(env, "GEMINI_HOME"); filepath.IsAbs(p) {
		return p
	}
	if home := systemexec.EnvironmentValue(env, "HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".gemini")
	}
	return ""
}

func (r *runtime) skillDirectories(launch domain.LaunchContext) []skillDirectory {
	var dirs []skillDirectory
	if r.skillProfile != "" {
		for _, sub := range []string{"antigravity-cli/skills", "skills"} {
			dirs = append(dirs, skillDirectory{r.skillProfile, sub, domain.SkillUser, true})
		}
	}
	for _, project := range append([]string{launch.CWD}, launch.AdditionalDirectories...) {
		if !filepath.IsAbs(project) {
			continue
		}
		for _, sub := range []string{".agents/skills", ".agent/skills"} {
			dirs = append(dirs, skillDirectory{project, sub, domain.SkillProject, false})
		}
	}
	if r.skillProfile != "" {
		dirs = append(dirs, skillDirectory{r.skillProfile, "config/skills", domain.SkillUser, false},
			skillDirectory{r.skillProfile, "antigravity-cli/builtin/skills", domain.SkillBuiltIn, false})
	}
	return dirs
}

type discoveredSkills struct {
	inventory domain.SkillInventory
	bodies    map[string]string
}

func (r *runtime) discoverSkills(ctx context.Context, launch domain.LaunchContext, wanted []string) (discoveredSkills, error) {
	out := discoveredSkills{bodies: map[string]string{}}
	var excluded []string
	if r.skillProfile != "" {
		readSkillExclusions(r.skillProfile, "config/skills.json", &out.inventory, &excluded)
	}
	for _, project := range append([]string{launch.CWD}, launch.AdditionalDirectories...) {
		readSkillExclusions(project, ".agents/skills.json", &out.inventory, &excluded)
	}
	found := make(map[string]bool)
	for _, dir := range r.skillDirectories(launch) {
		if err := ctx.Err(); err != nil {
			return discoveredSkills{}, err
		}
		err := scanSkillDirectory(dir, func(entry domain.ManagedSkill, data []byte, folder string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			switch {
			case !dir.priority && (slices.Contains(excluded, entry.Name) || slices.Contains(excluded, folder)):
				entry.ReadOnlyReason = "原生配置排除此 Skill"
			case found[entry.Name]:
				entry.ReadOnlyReason = "同名 Skill 由优先来源提供"
			default:
				entry.Enabled = true
				found[entry.Name] = true
			}
			out.inventory.Entries = append(out.inventory.Entries, entry)
			if slices.Contains(wanted, entry.ID) {
				out.bodies[entry.ID] = string(data)
			}
			return nil
		})
		if err != nil {
			return discoveredSkills{}, err
		}
	}
	return out, nil
}

func readSkillExclusions(base, rel string, inv *domain.SkillInventory, excluded *[]string) {
	if !filepath.IsAbs(base) {
		return
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return
	}
	defer root.Close()
	data, err := readSkillFile(root, rel)
	if err != nil {
		return
	}
	var manifest struct {
		Exclude  []string
		Entries  []json.RawMessage
		Inherits []json.RawMessage
	}
	if json.Unmarshal(data, &manifest) != nil {
		inv.Warnings = append(inv.Warnings, "部分 Antigravity Skill 清单无法读取")
		return
	}
	*excluded = append(*excluded, manifest.Exclude...)
	if len(manifest.Entries)+len(manifest.Inherits) != 0 && !slices.Contains(inv.Warnings, "清单附加来源由 Antigravity CLI 管理") {
		inv.Warnings = append(inv.Warnings, "清单附加来源由 Antigravity CLI 管理")
	}
}

func scanSkillDirectory(dir skillDirectory, visit func(domain.ManagedSkill, []byte, string) error) error {
	base, err := os.OpenRoot(dir.base)
	if err != nil {
		return nil
	}
	defer base.Close()
	root, err := base.OpenRoot(filepath.FromSlash(dir.relative))
	if err != nil {
		return nil
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return nil
	}
	entries, err := f.ReadDir(512)
	f.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })
	for _, folder := range entries {
		if !folder.IsDir() {
			continue
		}
		rel := filepath.Join(folder.Name(), "SKILL.md")
		data, err := readSkillFile(root, rel)
		if err != nil {
			continue
		}
		name, description := skillFrontmatter(string(data))
		if name == "" {
			continue
		}
		path := filepath.Join(dir.base, filepath.FromSlash(dir.relative), rel)
		hash := sha256.New()
		_, _ = io.WriteString(hash, path+"\x00")
		_, _ = hash.Write(data)
		entry := domain.ManagedSkill{
			SkillReference: domain.SkillReference{ID: fmt.Sprintf("%x", hash.Sum(nil)), Name: name},
			Description:    description, Scope: dir.scope, Directory: filepath.Dir(path), Path: path,
			ReadOnlyReason: "由 Antigravity CLI 管理",
		}
		if err := visit(entry, data, folder.Name()); err != nil {
			return err
		}
	}
	return nil
}

func readSkillFile(root *os.Root, path string) ([]byte, error) {
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return nil, errors.New("无效的 Skill 文档")
	}
	data, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	if err != nil || len(data) > 128<<10 || !utf8.Valid(data) {
		return nil, errors.New("无法读取 Skill 文档")
	}
	return data, nil
}

func skillFrontmatter(body string) (name, description string) {
	first, rest, _ := strings.Cut(strings.TrimPrefix(body, "\ufeff"), "\n")
	if strings.TrimSpace(first) != "---" {
		return "", ""
	}
	for line := range strings.SplitSeq(rest, "\n") {
		if strings.TrimSpace(line) == "---" {
			for _, ch := range name {
				if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
					return "", ""
				}
			}
			if len(name) > 64 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
				return "", ""
			}
			return name, clip(description, 1024)
		}
		key, value, _ := strings.Cut(line, ":")
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		switch key {
		case "name":
			name = value
		case "description":
			if value != ">" && value != "|" {
				description = value
			}
		}
	}
	return "", ""
}

func (r *runtime) SkillInventory(ctx context.Context, launch domain.LaunchContext) (domain.SkillInventory, error) {
	got, err := r.discoverSkills(ctx, launch, nil)
	return got.inventory, err
}

func (r *runtime) Skills(ctx context.Context, launch domain.LaunchContext) (domain.SkillCatalog, error) {
	found, err := r.SkillInventory(ctx, launch)
	out := domain.SkillCatalog{Warnings: found.Warnings}
	for _, entry := range found.Entries {
		if entry.Enabled {
			out.Skills = append(out.Skills, domain.Skill{SkillReference: entry.SkillReference, Description: entry.Description})
		}
	}
	return out, err
}

func (r *runtime) ReadSkill(ctx context.Context, launch domain.LaunchContext, id string) (domain.SkillDocument, error) {
	got, err := r.discoverSkills(ctx, launch, []string{id})
	if err != nil {
		return domain.SkillDocument{}, err
	}
	if text, ok := got.bodies[id]; ok {
		return domain.SkillDocument{ID: id, Text: text}, nil
	}
	return domain.SkillDocument{}, errors.New("所选 Skill 已不可用，请重新选择")
}

func (*runtime) ChangeSkill(context.Context, domain.LaunchContext, domain.SkillChange) (domain.SkillChangeResult, error) {
	return domain.SkillChangeResult{}, harness.Unsupported("skill-management-write")
}

func (r *runtime) skillBlocks(ctx context.Context, selected []domain.SkillReference) ([]contentBlock, error) {
	r.mu.Lock()
	launch := r.session.Context
	r.mu.Unlock()
	ids := make([]string, 0, len(selected))
	for _, ref := range selected {
		ids = append(ids, ref.ID)
	}
	found, err := r.discoverSkills(ctx, launch, ids)
	if err != nil {
		return nil, err
	}
	var blocks []contentBlock
	size := 0
	for _, ref := range selected {
		index := slices.IndexFunc(found.inventory.Entries, func(entry domain.ManagedSkill) bool {
			return entry.Enabled && entry.SkillReference == ref
		})
		if index < 0 {
			return nil, errors.New("所选 Skill 已不可用，请重新选择")
		}
		entry := found.inventory.Entries[index]
		text := "Selected skill: " + ref.Name + "\nSource: " + entry.Path + "\n\n" + found.bodies[ref.ID]
		size += len(text)
		if size > 128<<10 {
			return nil, errors.New("Skill 内容合计不能超过 128 KiB")
		}
		if r.native.AgentCapabilities.PromptCapabilities.EmbeddedContext {
			blocks = append(blocks, contentBlock{Type: "resource", Resource: &textResource{URI: "shioji://skill/" + ref.ID, MIMEType: "text/plain", Text: text}})
		} else {
			blocks = append(blocks, contentBlock{Type: "text", Text: text})
		}
	}
	return blocks, nil
}
