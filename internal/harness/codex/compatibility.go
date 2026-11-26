package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

type protocolSupport struct {
	scopedToolStatus       bool
	scopedHooks            bool
	indexedThreadList      bool
	browserLogin           bool
	deviceLogin            bool
	collaborationMode      bool
	serviceTier            bool
	serviceTierForTurn     bool
	threadStartServiceTier bool
	methods                map[string]bool
}
type requestShape struct {
	OneOf      []requestShape             `json:"oneOf,omitempty"`
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
	Ref        string                     `json:"$ref"`
}
type protocolSchema struct {
	OneOf       []requestShape          `json:"oneOf"`
	Definitions map[string]requestShape `json:"definitions"`
}

// These are the fields Shioji actually sends and relies on. Additive fields
// remain compatible; removed security/context fields or new required inputs do
// not silently fall back to the source's defaults.
var requestContracts = map[string][]string{
	"experimentalFeature/list": {"cursor", "limit", "threadId"},
	"thread/goal/get":          {"threadId"},
	"thread/goal/set":          {"threadId", "objective", "status", "origin"},
	"thread/goal/clear":        {"threadId", "origin"},
	"mcpServerStatus/list":     {"limit", "cursor"},
	"mcpServer/oauth/login":    {"name", "timeoutSecs"},
	"config/mcpServer/reload":  {},
	"initialize":               {"clientInfo", "capabilities"},
	"model/list":               {"cursor", "limit", "includeHidden"},
	"config/read":              {"cwd", "includeLayers"},
	"config/batchWrite":        {"edits", "filePath", "expectedVersion", "reloadUserConfig"},
	"account/read":             {"refreshToken"},
	"account/rateLimits/read":  {},
	"account/login/cancel":     {"loginId"},
	"configRequirements/read":  {},
	"environment/add":          {"environmentId", "execServerUrl", "authBearerToken"},
	"thread/start":             {"cwd", "model", "approvalPolicy", "sandbox", "config", "runtimeWorkspaceRoots", "environments"},
	"thread/resume":            {"threadId", "excludeTurns", "config"},
	"turn/start":               {"threadId", "input", "clientUserMessageId", "model", "effort", "approvalPolicy", "sandboxPolicy", "environments"},
	"turn/interrupt":           {"threadId", "turnId"},
	"turn/steer":               {"threadId", "expectedTurnId", "clientUserMessageId", "input"},
	"skills/list":              {"cwds", "forceReload"},
	"hooks/list":               {"cwds"},
	"collaborationMode/list":   {},
	"permissionProfile/list":   {"limit"},
	"thread/read":              {"threadId"},
	"thread/list":              {"limit", "cursor", "sortKey", "modelProviders", "sourceKinds"},
	"thread/compact/start":     {"threadId"},
	"thread/turns/list":        {"threadId", "limit", "cursor", "sortDirection", "itemsView"},
	"thread/fork":              {"threadId", "excludeTurns", "deferGoalContinuation", "config", "beforeTurnId", "lastTurnId"},
	"thread/unsubscribe":       {"threadId"},
	"thread/loaded/list":       {},
}
var requiredMethods = []string{"initialize", "model/list", "config/read", "configRequirements/read", "environment/add", "thread/start", "thread/resume", "turn/start", "turn/interrupt"}

func protocolFromSchema(data []byte) (*protocolSupport, error) {
	var schema protocolSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	support := &protocolSupport{methods: map[string]bool{}}
	for _, variant := range schema.OneOf {
		var method struct {
			Enum []string `json:"enum"`
		}
		if json.Unmarshal(variant.Properties["method"], &method) != nil || len(method.Enum) != 1 {
			continue
		}
		if method.Enum[0] == "account/login/start" {
			var params requestShape
			json.Unmarshal(variant.Properties["params"], &params)
			if params.Ref != "" {
				params = schema.Definitions[strings.TrimPrefix(params.Ref, "#/definitions/")]
			}
			for _, choice := range params.OneOf {
				if choice.Ref != "" {
					choice = schema.Definitions[strings.TrimPrefix(choice.Ref, "#/definitions/")]
				}
				var kind struct {
					Enum []string `json:"enum"`
				}
				json.Unmarshal(choice.Properties["type"], &kind)
				compatible := len(kind.Enum) == 1
				for _, required := range choice.Required {
					if required != "type" {
						compatible = false
					}
				}
				if compatible {
					if kind.Enum[0] == "chatgpt" {
						support.browserLogin = true
					}
					if kind.Enum[0] == "chatgptDeviceCode" {
						support.deviceLogin = true
					}
				}
			}
			continue
		}
		fields, used := requestContracts[method.Enum[0]]
		if !used {
			continue
		}
		var params requestShape
		if raw := variant.Properties["params"]; len(raw) > 0 {
			if json.Unmarshal(raw, &params) != nil {
				continue
			}
			if params.Ref != "" {
				var exists bool
				params, exists = schema.Definitions[strings.TrimPrefix(params.Ref, "#/definitions/")]
				if !exists {
					continue
				}
			}
		}
		compatible := true
		known := map[string]bool{}
		for _, field := range fields {
			known[field] = true
			if _, ok := params.Properties[field]; !ok {
				compatible = false
			}
		}
		for _, field := range params.Required {
			if !known[field] {
				compatible = false
			}
		}
		support.methods[method.Enum[0]] = compatible
		if method.Enum[0] == "mcpServerStatus/list" && compatible {
			_, support.scopedToolStatus = params.Properties["threadId"]
		}
		if method.Enum[0] == "hooks/list" && compatible {
			_, support.scopedHooks = params.Properties["threadId"]
		}
		if method.Enum[0] == "thread/list" && compatible {
			var field struct{ Type string }
			support.indexedThreadList = json.Unmarshal(params.Properties["useStateDbOnly"], &field) == nil && field.Type == "boolean"
		}
		if method.Enum[0] == "thread/start" {
			_, support.threadStartServiceTier = params.Properties["serviceTier"]
		}
		if method.Enum[0] == "turn/start" {
			_, support.collaborationMode = params.Properties["collaborationMode"]
			_, support.serviceTier = params.Properties["serviceTier"]
			_, support.serviceTierForTurn = params.Properties["serviceTierForTurn"]
		}
	}
	for _, method := range requiredMethods {
		if !support.methods[method] {
			return nil, &harness.SetupError{Issue: domain.RuntimeIncompatible, Message: fmt.Sprintf("Codex 协议缺少兼容的 %s，无法安全接入", method)}
		}
	}
	if !support.methods["account/login/cancel"] || !support.methods["account/read"] {
		support.browserLogin, support.deviceLogin = false, false
	}
	return support, nil
}

type executableImage struct{ path, version string }

func installedExecutable(binary string, environments ...[]string) (executableImage, error) {
	if binary == "" {
		binary = "codex"
	}
	env := os.Environ()
	if len(environments) > 0 {
		env = environments[0]
	}
	path, err := lookPathEnvironment(binary, env)
	if err != nil && binary == "codex" {
		home := environmentValue(env, "HOME")
		for _, candidate := range []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex", filepath.Join(home, ".npm-global/bin/codex"), filepath.Join(home, ".npm-global/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex"), "/Applications/Codex.app/Contents/Resources/codex"} {
			if resolved, e := lookPathEnvironment(candidate, env); e == nil {
				path, err = resolved, nil
				break
			}
		}
	}
	if err != nil {
		return executableImage{}, &harness.SetupError{Issue: domain.RuntimeMissing, Message: "未找到 Codex，请配置可执行文件或检查该设备的 Shell PATH"}
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return executableImage{}, err
	}
	st, err := os.Stat(real)
	if err != nil {
		return executableImage{}, err
	}
	stat := st.Sys().(*syscall.Stat_t)
	identity := fmt.Sprintf("%s\x00%s\x00%d:%d:%d:%d:%d", path, real, stat.Dev, stat.Ino, st.Size(), st.ModTime().UnixNano(), st.Mode())
	if interpreter := nodeInterpreter(path); interpreter != "" {
		identity += "\x00node:missing"
		if node, e := lookPathEnvironment(interpreter, env); e == nil {
			if realNode, e := filepath.EvalSymlinks(node); e == nil {
				if info, e := os.Stat(realNode); e == nil {
					ns := info.Sys().(*syscall.Stat_t)
					identity += fmt.Sprintf("%s:%d:%d:%d:%d", realNode, ns.Dev, ns.Ino, info.Size(), info.ModTime().UnixNano())
				}
			}
		}
	}
	return executableImage{path: path, version: fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))}, nil
}

var installedProtocol struct {
	sync.Mutex
	version string
	support *protocolSupport
}

// Preserve the invoked name for version-manager shims; real-file identity is
// tracked separately so replacement is still detected across startup.
func compatibleExecutable(ctx context.Context, binary string, environments ...[]string) (string, *protocolSupport, error) {
	env := []string(nil)
	if len(environments) > 0 {
		env = environments[0]
	} else {
		env, _ = codexEnvironment(ctx, "", nil)
	}
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	image, err := installedExecutable(binary, env)
	if err != nil {
		return "", nil, err
	}
	path := image.path
	if interpreter := nodeInterpreter(path); interpreter != "" {
		if _, err := lookPathEnvironment(interpreter, env); err != nil {
			return "", nil, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: "已找到 Codex 启动器，但当前环境找不到 Node.js；请检查该设备的 Shell 配置或 Agent PATH"}
		}
	}
	sorted := append([]string(nil), env...)
	slices.Sort(sorted)
	key := image.version + fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(sorted, "\x00"))))
	installedProtocol.Lock()
	defer installedProtocol.Unlock()
	if installedProtocol.version == key && installedProtocol.support != nil {
		return path, installedProtocol.support, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err = runDetectionCommand(probeCtx, path, env, "--version"); err != nil {
		if probeCtx.Err() != nil {
			return "", nil, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: "检测 Codex 版本超时，请检查启动器和 Shell 配置"}
		}
		return "", nil, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: "Codex 启动器无法运行，请检查执行权限、运行依赖或安装完整性"}
	}
	dir, err := os.MkdirTemp("", "shioji-protocol-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	if err = runDetectionCommand(probeCtx, path, env, "app-server", "generate-json-schema", "--experimental", "--out", dir); err != nil {
		if probeCtx.Err() != nil {
			return "", nil, &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: "读取 Codex 协议超时，请检查启动器和运行依赖"}
		}
		return "", nil, &harness.SetupError{Issue: domain.RuntimeIncompatible, Message: "该 Codex 未提供可检查的 App Server 协议，请更新该设备上的安装"}
	}
	f, err := os.Open(filepath.Join(dir, "ClientRequest.json"))
	if err != nil {
		return "", nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 16<<20))
	f.Close()
	if err != nil {
		return "", nil, err
	}
	support, err := protocolFromSchema(data)
	if err != nil {
		return "", nil, err
	}
	after, err := installedExecutable(path, env)
	if err != nil || after.version != image.version {
		return "", nil, errors.New("Codex 安装正在变化，请稍后重试")
	}
	installedProtocol.version, installedProtocol.support = key, support
	return path, support, nil
}
func (r *Runtime) supports(method string) bool {
	return r.protocol != nil && r.protocol.methods[method]
}

func nodeInterpreter(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b := make([]byte, 512)
	n, _ := f.Read(b)
	line, _, _ := strings.Cut(string(b[:n]), "\n")
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return ""
	}
	if filepath.Base(fields[0]) == "node" {
		return fields[0]
	}
	if filepath.Base(fields[0]) == "env" {
		for _, field := range fields[1:] {
			if field == "node" {
				return field
			}
		}
	}
	return ""
}

func runDetectionCommand(ctx context.Context, path string, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 200 * time.Millisecond
	var out systemexec.BoundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err == nil && out.Overflow() {
		return errors.New("Codex 检测输出过大")
	}
	return err
}
