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
	linkPath       string
	scope          domain.SkillScope
	priority       bool // CLI-only and shared skills outrank the other locations.
	explicit       bool
	filter         skillFilter
	filterBases    []string
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

const sharedSkillPath = ".agents/skills"

func (r *runtime) sharedSkillRoot() string {
	if !filepath.IsAbs(r.skillHome) {
		return ""
	}
	return filepath.Join(r.skillHome, sharedSkillPath)
}

func (r *runtime) skillDirectories(projects []string) []skillDirectory {
	var dirs []skillDirectory
	shared := r.sharedSkillRoot()
	var sharedTarget string
	if shared != "" {
		sharedTarget, _ = filepath.EvalSymlinks(shared)
	}
	addUserDirectory := func(path string, priority bool) {
		dir := skillDirectory{base: r.skillProfile, relative: path, scope: domain.SkillUser, priority: priority}
		configured := filepath.Join(dir.base, dir.relative)
		// Require the link on the AGY side. The shared path can also be an alias for a normal AGY directory.
		if _, err := os.Readlink(configured); sharedTarget != "" && err == nil {
			if target, err := filepath.EvalSymlinks(configured); err == nil && target == sharedTarget {
				dir.linkPath = configured
				dir.base, dir.relative = shared, "."
			}
		}
		dirs = append(dirs, dir)
	}
	if r.skillProfile != "" {
		for _, sub := range []string{"antigravity-cli/skills", "skills"} {
			addUserDirectory(sub, true)
		}
	}
	for _, project := range projects {
		for _, sub := range projectNativeConfigDirectories {
			dirs = append(dirs, skillDirectory{base: project, relative: filepath.Join(sub, "skills"), scope: domain.SkillProject})
		}
	}
	if r.skillProfile != "" {
		addUserDirectory("config/skills", false)
		dirs = append(dirs, skillDirectory{base: r.skillProfile, relative: "antigravity-cli/builtin/skills", scope: domain.SkillBuiltIn})
	}
	return dirs
}

type discoveredSkills struct {
	inventory domain.SkillInventory
	bodies    map[string]string
}

func (r *runtime) discoverSkills(ctx context.Context, launch domain.LaunchContext, wanted []string) (discoveredSkills, error) {
	out := discoveredSkills{bodies: map[string]string{}}
	projects := projectNativeConfigBases(launch)
	extra, excluded := r.manifestSkills(launch, projects, &out.inventory)
	dirs := append(r.skillDirectories(projects), extra...)
	dirs = append(dirs, r.pluginSkills()...)
	rank := func(dir skillDirectory) int {
		if dir.priority {
			return 0
		}
		switch dir.scope {
		case domain.SkillProject:
			return 1
		case domain.SkillUser:
			return 2
		case domain.SkillManaged:
			return 3
		default:
			return 4
		}
	}
	slices.SortStableFunc(dirs, func(a, b skillDirectory) int { return cmp.Compare(rank(a), rank(b)) })
	// Keep existing selections when a legacy project directory adds a duplicate.
	for _, project := range projects {
		dirs = append(dirs, skillDirectory{base: project, relative: ".gemini/skills", scope: domain.SkillProject})
	}
	found := make(map[string]bool)
	observed := make(map[string]bool)
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return discoveredSkills{}, err
		}
		err := r.scanSkillDirectory(dir, func(entry domain.ManagedSkill, data []byte, folder string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if observed[entry.ID] {
				return nil
			}
			observed[entry.ID] = true
			switch {
			case !dir.priority && excludedNativeSkill(excluded, entry, folder, dir.linkPath):
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
	projected, _ := json.Marshal(out.inventory.Entries)
	out.inventory.Revision = fmt.Sprintf("%x", sha256.Sum256(projected))
	return out, nil
}

func (r *runtime) openSkillRoot(path string) (*os.Root, error) {
	shared := r.sharedSkillRoot()
	if shared == "" || path != shared {
		return os.OpenRoot(path)
	}
	home, err := os.OpenRoot(r.skillHome)
	if err != nil {
		return nil, err
	}
	defer home.Close()
	return home.OpenRoot(sharedSkillPath)
}

func (r *runtime) scanSkillDirectory(dir skillDirectory, visit func(domain.ManagedSkill, []byte, string) error) error {
	base, err := r.openSkillRoot(dir.base)
	if err != nil {
		return nil
	}
	defer base.Close()
	add := func(rel, folder string, data []byte) error {
		name, description := skillFrontmatter(string(data))
		path := filepath.Join(dir.base, rel)
		aliases := append(skillAliases(path, dir.filterBases...), name, folder)
		if name == "" || !dir.filter.matches(aliases...) {
			return nil
		}
		hash := sha256.New()
		_, _ = io.WriteString(hash, path+"\x00")
		_, _ = hash.Write(data)
		entry := domain.ManagedSkill{
			SkillReference: domain.SkillReference{ID: fmt.Sprintf("%x", hash.Sum(nil)), Name: name},
			Description:    description, Scope: dir.scope, Directory: filepath.Dir(path), Path: path,
			ReadOnlyReason: "由 Antigravity CLI 管理",
		}
		return visit(entry, data, folder)
	}
	if dir.explicit {
		rel := dir.relative
		if !strings.EqualFold(filepath.Base(rel), "SKILL.md") {
			rel = filepath.Join(rel, "SKILL.md")
		}
		if data, err := readSkillFile(base, rel); err == nil {
			return add(rel, filepath.Base(filepath.Dir(rel)), data)
		}
	}
	root, err := base.OpenRoot(dir.relative)
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
		if err := add(filepath.Join(dir.relative, rel), folder.Name(), data); err != nil {
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
