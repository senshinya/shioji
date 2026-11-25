package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

const hookTrustFile = "shioji-hook-trust.json"

type hookSetting struct {
	Matcher string            `json:"matcher,omitempty"`
	Hooks   []json.RawMessage `json:"hooks"`
}

type hookApproval struct {
	Revision string
	Enabled  bool
}

type managedHook struct {
	entry domain.HookEntry
	raw   json.RawMessage
}

const claudeConfigOpenFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

func readConfigFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, claudeConfigOpenFlags, 0)
	return readOpenedConfig(file, err)
}

func readProjectConfigFile(base, relative string) ([]byte, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(relative, claudeConfigOpenFlags, 0)
	return readOpenedConfig(file, err)
}

func readOpenedConfig(file *os.File, err error) ([]byte, error) {
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("Claude Code 配置文件无法读取")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("Claude Code 配置文件无法读取")
	}
	return raw, nil
}

func digestText(chunks ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(chunks, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (r *runtime) hookTrust() (map[string]hookApproval, error) {
	approved := make(map[string]hookApproval)
	raw, err := readConfigFile(filepath.Join(r.home, hookTrustFile))
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && json.Unmarshal(raw, &approved) != nil {
		return nil, errors.New("Claude Code Hooks 授权记录无效")
	}
	if approved == nil {
		approved = make(map[string]hookApproval)
	}
	return approved, nil
}

func (r *runtime) hookDefinitions(path string, scope domain.HookScope, approved map[string]hookApproval) ([]managedHook, error) {
	raw, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}
	return r.decodeHooks(raw, path, scope, approved)
}

func (r *runtime) decodeHooks(raw []byte, path string, scope domain.HookScope, approved map[string]hookApproval) ([]managedHook, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var settings struct {
		DisableAllHooks bool
		Hooks           map[string][]hookSetting
	}
	if json.Unmarshal(raw, &settings) != nil {
		return nil, errors.New("Claude Code Hooks 配置无效")
	}
	var list []managedHook
	for _, event := range slices.Sorted(maps.Keys(settings.Hooks)) {
		for group, bucket := range settings.Hooks[event] {
			for index, raw := range bucket.Hooks {
				var h struct {
					Type, Command, StatusMessage string
					Async                        bool
					Timeout                      uint64
				}
				if json.Unmarshal(raw, &h) != nil || h.Type == "" {
					continue
				}
				id := digestText(path, event, bucket.Matcher, strconv.Itoa(group), strconv.Itoa(index))
				fingerprint := digestText(event, bucket.Matcher, string(raw))
				entry := domain.HookEntry{
					ID: id, DefinitionRevision: fingerprint, Fingerprint: fingerprint, Event: event, Kind: h.Type,
					Command: r.redact(h.Command), StatusMessage: r.redact(h.StatusMessage), Scope: scope, Source: "Claude Code",
					SourcePath: path, Matcher: bucket.Matcher, Async: h.Async, TimeoutSeconds: h.Timeout, Trust: domain.HookUntrusted,
				}
				switch {
				case scope == domain.HookProject:
					entry.ReadOnlyReason = "独立 API Profile 不执行项目 Hooks"
				case settings.DisableAllHooks:
					entry.ReadOnlyReason = "Claude Code 配置禁用了 Hooks"
				case h.Type != "command" || h.Command == "" || r.config.Key != "" && strings.Contains(string(raw), r.config.Key):
					entry.ReadOnlyReason = "仅可授权明文可核对的命令型 Hook"
				case approved[id].Revision == fingerprint:
					entry.Trust, entry.Enabled, entry.CanEnable = domain.HookTrusted, approved[id].Enabled, true
				case approved[id].Revision != "":
					entry.Trust, entry.CanTrust = domain.HookModified, true
				default:
					entry.CanTrust = true
				}
				list = append(list, managedHook{entry: entry, raw: raw})
			}
		}
	}
	return list, nil
}

func (r *runtime) Hooks(ctx context.Context, launch domain.LaunchContext, source string) (domain.HookCatalog, error) {
	out := domain.HookCatalog{Directory: launch.CWD, Entries: []domain.HookEntry{}}
	approved, err := r.hookTrust()
	if err != nil {
		return out, err
	}
	entries, err := r.hookDefinitions(filepath.Join(r.home, "settings.json"), domain.HookUser, approved)
	if err != nil {
		return out, err
	}
	for _, root := range append([]string{launch.CWD}, launch.AdditionalDirectories...) {
		if !filepath.IsAbs(root) {
			continue
		}
		for _, name := range []string{"settings.json", "settings.local.json"} {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			rel := filepath.Join(".claude", name)
			raw, err := readProjectConfigFile(root, rel)
			var found []managedHook
			if err == nil {
				found, err = r.decodeHooks(raw, filepath.Join(root, rel), domain.HookProject, nil)
			}
			if err != nil {
				out.Warnings = append(out.Warnings, "无法读取项目 Claude Code Hooks")
				continue
			}
			entries = append(entries, found...)
		}
	}
	for _, hook := range entries {
		out.Entries = append(out.Entries, hook.entry)
	}
	raw, _ := json.Marshal(out.Entries)
	out.Revision = digestText(string(raw))
	if source != "" {
		r.mu.Lock()
		matches := !r.Stopped() && r.session.ThreadID == source && r.session.Context.CWD == launch.CWD
		r.mu.Unlock()
		if !matches {
			return out, errors.New("Claude Code 会话上下文已改变")
		}
		version, err := profileVersion(r.home)
		out.RuntimeKnown = err == nil && version == r.version
		if !out.RuntimeKnown {
			out.Warnings = append(out.Warnings, "保存的 Hooks 将在空闲会话重新加载后生效")
		}
	}
	return out, nil
}

func (r *runtime) ChangeHook(ctx context.Context, launch domain.LaunchContext, change domain.HookChange) (domain.HookChangeResult, error) {
	var out domain.HookChangeResult
	catalog, err := r.Hooks(ctx, launch, "")
	if err != nil {
		return out, err
	}
	if !change.Valid() || change.Revision != catalog.Revision {
		return out, errors.New("Claude Code Hooks 已变化，请刷新后重试")
	}
	entries := change.MatchingEntries(catalog)
	if len(entries) == 0 {
		return out, errors.New("无法确认所选 Claude Code Hook 版本")
	}
	approved, err := r.hookTrust()
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if !change.Allowed(entry) {
			return out, errors.New("此 Claude Code Hook 不可修改")
		}
		approved[entry.ID] = hookApproval{Revision: entry.DefinitionRevision, Enabled: change.Action != domain.HookSetEnabled || change.Enabled}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	raw, _ := json.Marshal(approved)
	if err := savePrivate(filepath.Join(r.home, hookTrustFile), raw); err != nil {
		return out, err
	}
	out.Saved = true
	out.Catalog, err = r.Hooks(ctx, launch, "")
	return out, err
}

func (r *runtime) executionHooks() (map[string][]hookSetting, error) {
	out := map[string][]hookSetting{}
	if r.title || r.home == "" {
		return out, nil
	}
	approved, err := r.hookTrust()
	if err != nil || len(approved) == 0 {
		return out, err
	}
	definitions, err := r.hookDefinitions(filepath.Join(r.home, "settings.json"), domain.HookUser, approved)
	if err != nil {
		return out, err
	}
	for _, definition := range definitions {
		if entry := definition.entry; entry.Enabled {
			out[entry.Event] = append(out[entry.Event], hookSetting{Matcher: entry.Matcher, Hooks: []json.RawMessage{definition.raw}})
		}
	}
	return out, nil
}

func (r *runtime) RefreshHooks(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	bound := r.process != nil && !r.Stopped()
	r.mu.Unlock()
	if !bound {
		return nil
	}
	current, err := profileVersion(r.home)
	if err != nil {
		return err
	}
	if r.version != current {
		return errors.New("Claude Code Hooks 将在空闲会话重新加载后生效")
	}
	return nil
}

func profileVersion(home string) (string, error) {
	var inputs []string
	for _, name := range []string{"settings-api.json", "settings.json", hookTrustFile} {
		data, err := readConfigFile(filepath.Join(home, name))
		if err != nil {
			return "", err
		}
		inputs = append(inputs, string(data))
	}
	return digestText(inputs...), nil
}

var _ harness.HookProvider = (*runtime)(nil)
