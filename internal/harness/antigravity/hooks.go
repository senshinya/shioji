package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

const userHooksFile = "config/hooks.json"
const projectHooksName = "hooks.json"

var hookEvents = []string{"SessionStart", "PreToolUse", "PostToolUse", "PreInvocation", "PostInvocation", "Stop"}

type nativeHookFile struct {
	base, file string
	scope      domain.HookScope
	doc        nativeConfigDocument
}

type nativeHook struct {
	Type    string
	Command string
	Timeout uint64
}

func (r *runtime) hookFiles(ctx context.Context, launch domain.LaunchContext) ([]nativeHookFile, error) {
	if !filepath.IsAbs(r.skillProfile) {
		return nil, harness.Unsupported("hook-management")
	}
	all := []nativeHookFile{{base: r.skillProfile, file: userHooksFile, scope: domain.HookUser}}
	for _, base := range projectNativeConfigBases(launch) {
		for _, configDir := range projectNativeConfigDirectories {
			all = append(all, nativeHookFile{base: base, file: filepath.Join(configDir, projectHooksName), scope: domain.HookProject})
		}
	}
	for i := range all {
		f := &all[i]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		doc, err := readNativeConfig(f.base, f.file)
		if err != nil {
			return nil, err
		}
		f.doc = doc
	}
	return all, nil
}

func (r *runtime) Hooks(ctx context.Context, launch domain.LaunchContext, source string) (domain.HookCatalog, error) {
	if source != "" {
		r.mu.Lock()
		matches := source == r.session.ThreadID && launch.CWD == r.session.Context.CWD
		r.mu.Unlock()
		if !matches {
			return domain.HookCatalog{}, errors.New("会话上下文已变化，请重新核对")
		}
	}
	files, err := r.hookFiles(ctx, launch)
	if err != nil {
		return domain.HookCatalog{}, err
	}
	out := projectHooks(launch, files)
	if source != "" {
		out.Warnings = append(out.Warnings, "当前会话的 Hook 生效状态未知")
	}
	return out, nil
}

func projectHooks(launch domain.LaunchContext, files []nativeHookFile) domain.HookCatalog {
	out := domain.HookCatalog{Directory: launch.CWD, Entries: []domain.HookEntry{}}
	revision := sha256.New()
	for _, f := range files {
		path := filepath.Join(f.base, f.file)
		_, _ = revision.Write([]byte(path + "\x00" + f.doc.revision + "\x00"))
		names := make([]string, 0, len(f.doc.fields))
		for name := range f.doc.fields {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if hook, ok := f.definition(name); ok {
				out.Entries = append(out.Entries, hook)
			}
		}
	}
	out.Revision = hex.EncodeToString(revision.Sum(nil))
	return out
}

func (f nativeHookFile) definition(name string) (domain.HookEntry, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(f.doc.fields[name], &fields) != nil || fields == nil {
		return domain.HookEntry{}, false
	}
	enabled := true
	if value := fields["enabled"]; value != nil && json.Unmarshal(value, &enabled) != nil {
		return domain.HookEntry{}, false
	}
	var events, matchers, definitions, previews []string
	var timeout uint64
	complete := true
	for _, event := range hookEvents {
		if fields[event] == nil {
			continue
		}
		var handlers []struct {
			nativeHook
			Matcher string
			Hooks   []nativeHook
		}
		if json.Unmarshal(fields[event], &handlers) != nil {
			complete = false
			continue
		}
		if len(handlers) == 0 {
			continue
		}
		events = append(events, event)
		for _, handler := range handlers {
			if handler.Matcher != "" && !slices.Contains(matchers, handler.Matcher) {
				matchers = append(matchers, handler.Matcher)
			}
			native := handler.Hooks
			if len(native) == 0 {
				native = []nativeHook{handler.nativeHook}
			}
			for _, h := range native {
				if h.Command == "" || h.Type != "" && h.Type != "command" {
					complete = false
					continue
				}
				definitions = append(definitions, h.Command)
				label := event
				if handler.Matcher != "" {
					label += " · " + handler.Matcher
				}
				previews = append(previews, label+"\n"+h.Command)
				timeout = h.Timeout
			}
		}
	}
	if len(events) == 0 || len(definitions) == 0 {
		return domain.HookEntry{}, false
	}
	file := filepath.Join(f.base, f.file)
	id := sha256.Sum256([]byte(file + "\x00" + name))
	delete(fields, "enabled")
	raw, _ := json.Marshal(fields)
	digest := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(digest[:])
	source := "设备"
	if f.scope == domain.HookProject {
		source = "项目"
	}
	command := definitions[0]
	if len(definitions) != 1 {
		timeout = 0
		command = strings.Join(previews, "\n\n")
	}
	matcher := strings.Join(matchers, "、")
	complete = complete && len(name) <= 256 && len(command) <= 32<<10 && len(matcher) <= 4096
	reason := ""
	if !complete {
		reason = "完整定义请在 Antigravity 原生配置中检查"
	}
	return domain.HookEntry{
		ID: hex.EncodeToString(id[:]), DefinitionRevision: fingerprint, Fingerprint: fingerprint,
		Name: clip(name, 256), Event: strings.Join(events, "、"), Kind: "command", Command: clip(command, 32<<10),
		Matcher: clip(matcher, 4096), TimeoutSeconds: timeout,
		Source: source, SourcePath: file, Scope: f.scope, Enabled: enabled, CanEnable: complete, ReadOnlyReason: reason, Trust: domain.HookNativeManaged,
	}, true
}

func (r *runtime) ChangeHook(ctx context.Context, launch domain.LaunchContext, change domain.HookChange) (domain.HookChangeResult, error) {
	none := domain.HookChangeResult{}
	if !change.Valid() || change.Action != domain.HookSetEnabled {
		return none, harness.Unsupported("hook-configuration-action")
	}
	files, err := r.hookFiles(ctx, launch)
	if err != nil {
		return none, err
	}
	catalog := projectHooks(launch, files)
	targets := change.MatchingEntries(catalog)
	if change.Revision != catalog.Revision || len(targets) != 1 {
		return none, errors.New("Hook 配置已变化，请刷新后重新核对")
	}
	target := targets[0]
	if !change.Allowed(target) {
		return none, errors.New(target.ReadOnlyReason)
	}
	if launch.CWD == "" && target.Scope != domain.HookUser || launch.CWD != "" && target.Scope != domain.HookProject {
		return none, errors.New("请在 Hook 所属位置修改配置")
	}
	for i, f := range files {
		if filepath.Join(f.base, f.file) != target.SourcePath {
			continue
		}
		for name := range f.doc.fields {
			entry, ok := f.definition(name)
			if !ok || entry.ID != target.ID {
				continue
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(f.doc.fields[name], &fields); err != nil {
				return none, errors.New("无法读取 Hook 定义")
			}
			fields["enabled"], _ = json.Marshal(change.Enabled)
			f.doc.fields[name], _ = json.Marshal(fields)
			root, err := os.OpenRoot(f.base)
			if err != nil {
				return none, errors.New("无法打开 Hook 配置目录")
			}
			defer root.Close()
			if err := writeNativeConfig(ctx, root, f.file, f.doc, 0777); err != nil {
				return none, err
			}
			updated, err := readNativeConfigFile(root, f.file)
			if err != nil {
				// The owning worker can reconcile the persisted operation by
				// observing its original definition and the requested state.
				return none, &harness.DeliveryError{Uncertain: true, Message: "Hook 写入已完成，当前配置待核对"}
			}
			files[i].doc = updated
			return domain.HookChangeResult{Saved: true, Catalog: projectHooks(launch, files)}, nil
		}
	}
	return none, errors.New("找不到已核对的 Hook 配置")
}

func (*runtime) RefreshHooks(context.Context) error { return harness.Unsupported("hook-live-refresh") }
