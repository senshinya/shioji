package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$|^[a-z0-9]$`)

type skillSnapshot struct {
	entry domain.ManagedSkill
	body  string
}

const skillSelections = "shioji-skill-disabled.json"

func (r *runtime) disabledSkills() (map[string]bool, error) {
	disabled := make(map[string]bool)
	raw, err := readConfigFile(filepath.Join(r.home, skillSelections))
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && json.Unmarshal(raw, &disabled) != nil || disabled == nil {
		return nil, errors.New("Claude Code Skill 配置无效")
	}
	return disabled, nil
}

func (r *runtime) skillSnapshots(ctx context.Context, launch domain.LaunchContext) ([]skillSnapshot, error) {
	type source struct {
		folder, relative string
		scope            domain.SkillScope
	}
	sources := []source{{r.home, "skills", domain.SkillUser}}
	for _, dir := range append([]string{launch.CWD}, launch.AdditionalDirectories...) {
		sources = append(sources, source{dir, ".claude/skills", domain.SkillProject})
	}
	var out []skillSnapshot
	seen := map[string]bool{}
	disabled, err := r.disabledSkills()
	if err != nil {
		return nil, err
	}
	for _, src := range sources {
		if !filepath.IsAbs(src.folder) {
			continue
		}
		base, err := os.OpenRoot(src.folder)
		if err != nil {
			continue
		}
		root, err := base.OpenRoot(src.relative)
		base.Close()
		if err != nil {
			continue
		}
		entries, err := fsReadDir(root)
		if err != nil {
			root.Close()
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				root.Close()
				return nil, err
			}
			name := entry.Name()
			if !entry.IsDir() || !skillName.MatchString(name) {
				continue
			}
			file, err := root.OpenFile(filepath.Join(name, "SKILL.md"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				continue
			}
			info, statErr := file.Stat()
			var raw []byte
			if statErr == nil && info.Mode().IsRegular() && info.Size() <= 128<<10 {
				raw, err = io.ReadAll(io.LimitReader(file, (128<<10)+1))
			}
			file.Close()
			if err != nil || len(raw) == 0 || len(raw) > 128<<10 || !utf8.Valid(raw) {
				continue
			}
			path := filepath.Join(src.folder, src.relative, name, "SKILL.md")
			digest := sha256.Sum256(append([]byte(path+"\x00"), raw...))
			id := hex.EncodeToString(digest[:])
			description, body, safe := skillBody(string(raw), r.config.Key)
			snapshot := skillSnapshot{entry: domain.ManagedSkill{
				SkillReference: domain.SkillReference{ID: id, Name: name}, Description: description,
				Scope: src.scope, Path: path, Directory: filepath.Dir(path),
				Enabled: safe && !seen[name] && !disabled[digestText(path)], CanEnable: safe && !seen[name],
			}, body: body}
			if !safe {
				snapshot.entry.ReadOnlyReason = "此 Skill 包含动态命令或原生执行设置"
			} else if seen[name] {
				snapshot.entry.ReadOnlyReason = "已有优先级更高的同名 Skill"
			}
			if snapshot.entry.Enabled {
				seen[name] = true
			}
			out = append(out, snapshot)
		}
		root.Close()
	}
	return out, nil
}

func fsReadDir(root *os.Root) ([]os.DirEntry, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	out, err := file.ReadDir(512)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	slices.SortFunc(out, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return out, err
}

func skillBody(raw, key string) (description, body string, safe bool) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) < 4 || strings.TrimSpace(lines[0]) != "---" || key != "" && strings.Contains(raw, key) {
		return "", "", false
	}
	for i, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			body = strings.TrimSpace(strings.Join(lines[i+2:], "\n"))
			return description, body, body != "" && !strings.Contains(body, "!`") && !strings.Contains(body, "!\n") &&
				!strings.Contains(body, "$(") && !strings.ContainsRune(body, 0)
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "description":
			description = strings.Trim(strings.TrimSpace(value), "\"'")
		case "name", "argument-hint":
		default:
			// The private plugin must not inherit hooks, shell, model,
			// multiple execution contexts or automatic tool authorization.
			return description, "", false
		}
	}
	return description, "", false
}

func (r *runtime) Skills(ctx context.Context, launch domain.LaunchContext) (domain.SkillCatalog, error) {
	snapshots, err := r.skillSnapshots(ctx, launch)
	out := domain.SkillCatalog{Skills: []domain.Skill{}}
	for _, s := range snapshots {
		if s.entry.Enabled {
			out.Skills = append(out.Skills, domain.Skill{SkillReference: s.entry.SkillReference, Description: s.entry.Description})
		}
	}
	return out, err
}

func (r *runtime) SkillInventory(ctx context.Context, launch domain.LaunchContext) (domain.SkillInventory, error) {
	snapshots, err := r.skillSnapshots(ctx, launch)
	out := domain.SkillInventory{Entries: []domain.ManagedSkill{}}
	for _, s := range snapshots {
		out.Entries = append(out.Entries, s.entry)
	}
	raw, _ := json.Marshal(out.Entries)
	digest := sha256.Sum256(raw)
	out.Revision = hex.EncodeToString(digest[:])
	return out, err
}

func (r *runtime) ReadSkill(ctx context.Context, launch domain.LaunchContext, ref string) (domain.SkillDocument, error) {
	snapshots, err := r.skillSnapshots(ctx, launch)
	if err != nil {
		return domain.SkillDocument{}, err
	}
	for _, s := range snapshots {
		if s.entry.ID == ref && s.body != "" {
			return domain.SkillDocument{ID: ref, Text: s.body}, nil
		}
	}
	return domain.SkillDocument{}, errors.New("找不到可读取的 Claude Code Skill")
}

func (r *runtime) ChangeSkill(ctx context.Context, launch domain.LaunchContext, change domain.SkillChange) (domain.SkillChangeResult, error) {
	var out domain.SkillChangeResult
	inventory, err := r.SkillInventory(ctx, launch)
	if err != nil {
		return out, err
	}
	if inventory.Revision != change.Revision {
		return out, errors.New("Claude Code Skill 已变化，请刷新后重试")
	}
	for _, entry := range inventory.Entries {
		if entry.ID != change.ID || !entry.CanEnable {
			continue
		}
		disabled, err := r.disabledSkills()
		if err != nil {
			return out, err
		}
		id := digestText(entry.Path)
		if change.Enabled {
			delete(disabled, id)
		} else {
			disabled[id] = true
		}
		raw, _ := json.Marshal(disabled)
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := savePrivate(filepath.Join(r.home, skillSelections), raw); err != nil {
			return out, err
		}
		return domain.SkillChangeResult{Saved: true, Enabled: change.Enabled}, nil
	}
	return out, errors.New("所选 Claude Code Skill 不可启停")
}

type preparedInput struct {
	Blocks  []map[string]any
	Plugins []map[string]string
	Command string
	Goal    *domain.GoalProgress
	GoalKey string
}

func addSkillCommand(message map[string]any, input preparedInput) {
	if input.Command != "" {
		message["skill_plugins"], message["skill_command"] = input.Plugins, input.Command
	}
}

func (r *runtime) prepareInput(ctx context.Context, session domain.Session, in harness.TurnInput) (preparedInput, error) {
	if in.Goal != nil {
		if session.Config.Plan || strings.TrimSpace(in.Objective) == "" || len(in.Skills) != 0 || len(in.Attachments) != 0 {
			return preparedInput{}, errors.New("目标需要独立正文和执行模式")
		}
		goal, key := *in.Goal, domain.NewID()
		text := "Advance this Shioji goal: " + in.Objective + "\nBefore ending this step, call the Shioji Goal tool report_goal with key " + key +
			" and outcome complete only if verified, blocked if user input is required, or continue if there is more work."
		return preparedInput{Goal: &goal, GoalKey: key, Blocks: []map[string]any{{"type": "text", "text": text}}}, nil
	}
	if len(in.Skills) == 0 {
		blocks, err := inputBlocks(in, r.attachmentDirectory(session))
		return preparedInput{Blocks: blocks}, err
	}
	result := preparedInput{}
	if strings.TrimSpace(in.Text) != "" || len(in.Attachments) != 0 || in.ForkContext != nil || in.Goal != nil || in.QuestionReply != nil {
		body := in
		body.Skills = nil
		var err error
		result.Blocks, err = inputBlocks(body, r.attachmentDirectory(session))
		if err != nil {
			return result, err
		}
	}
	skills, err := r.skillSnapshots(ctx, session.Context)
	if err != nil {
		return result, err
	}
	byID := make(map[string]skillSnapshot)
	for _, skill := range skills {
		if skill.entry.Enabled {
			byID[skill.entry.ID] = skill
		}
	}
	for i, ref := range in.Skills {
		skill, ok := byID[ref.ID]
		if !ok || skill.entry.SkillReference != ref {
			return result, errors.New("所选 Claude Code Skill 已变化或不可用")
		}
		if i != len(in.Skills)-1 {
			result.Blocks = append(result.Blocks, map[string]any{"type": "text", "text": "User-selected Skill " + ref.Name + "\n" + skill.body})
			continue
		}
		name := "shioji-" + ref.ID[:20]
		folder := filepath.Join(r.home, "shioji-skill-plugins", name)
		manifest, _ := json.Marshal(map[string]string{"name": name})
		if err := savePrivate(filepath.Join(folder, ".claude-plugin", "plugin.json"), manifest); err != nil {
			return result, err
		}
		doc := "---\nname: " + ref.Name + "\n---\n" + skill.body + "\n"
		if err := savePrivate(filepath.Join(folder, "skills", ref.Name, "SKILL.md"), []byte(doc)); err != nil {
			return result, err
		}
		result.Command = name + ":" + ref.Name
		result.Plugins = []map[string]string{{"type": "local", "path": folder}}
		result.Blocks = append(result.Blocks, map[string]any{"type": "text", "text": "/" + result.Command})
	}
	return result, nil
}

var _ harness.SkillProvider = (*runtime)(nil)
var _ harness.SkillManager = (*runtime)(nil)
