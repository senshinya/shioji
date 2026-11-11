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
	"strings"
	"sync"
	"syscall"
	"time"
)

type protocolSupport struct {
	scopedToolStatus  bool
	browserLogin      bool
	deviceLogin       bool
	collaborationMode bool
	serviceTier       bool
	methods           map[string]bool
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
	"mcpServerStatus/list":    {"limit", "cursor"},
	"mcpServer/oauth/login":   {"name", "timeoutSecs"},
	"config/mcpServer/reload": {},
	"initialize":              {"clientInfo", "capabilities"},
	"model/list":              {"cursor", "limit", "includeHidden"},
	"config/read":             {"cwd", "includeLayers"},
	"config/batchWrite":       {"edits", "filePath", "expectedVersion", "reloadUserConfig"},
	"account/read":            {"refreshToken"},
	"account/login/cancel":    {"loginId"},
	"configRequirements/read": {},
	"environment/add":         {"environmentId", "execServerUrl", "authBearerToken"},
	"thread/start":            {"cwd", "model", "approvalPolicy", "sandbox", "config", "runtimeWorkspaceRoots", "environments"},
	"thread/resume":           {"threadId", "excludeTurns", "config"},
	"turn/start":              {"threadId", "input", "clientUserMessageId", "model", "effort", "approvalPolicy", "sandboxPolicy", "environments"},
	"turn/interrupt":          {"threadId", "turnId"},
	"turn/steer":              {"threadId", "expectedTurnId", "clientUserMessageId", "input"},
	"skills/list":             {"cwds", "forceReload"},
	"collaborationMode/list":  {},
	"permissionProfile/list":  {"limit"},
	"thread/read":             {"threadId"},
	"thread/list":             {"cwd", "limit", "cursor", "sortKey", "modelProviders", "sourceKinds"},
	"thread/compact/start":    {"threadId"},
	"thread/turns/list":       {"threadId", "limit", "cursor", "sortDirection", "itemsView"},
	"thread/fork":             {"threadId", "excludeTurns", "deferGoalContinuation", "config", "beforeTurnId", "lastTurnId"},
	"thread/unsubscribe":      {"threadId"},
	"thread/loaded/list":      {},
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
		if method.Enum[0] == "turn/start" {
			_, support.collaborationMode = params.Properties["collaborationMode"]
			_, support.serviceTier = params.Properties["serviceTier"]
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

func installedExecutable(binary string) (executableImage, error) {
	if binary == "" {
		binary = "codex"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return executableImage{}, &harness.SetupError{Issue: domain.RuntimeMissing, Message: "未找到 Codex，请配置可执行文件或在该设备安装"}
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return executableImage{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return executableImage{}, err
	}
	stat := st.Sys().(*syscall.Stat_t)
	identity := fmt.Sprintf("%s\x00%d:%d:%d:%d:%d", path, stat.Dev, stat.Ino, st.Size(), st.ModTime().UnixNano(), st.Mode())
	return executableImage{path: path, version: fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))}, nil
}

var installedProtocol struct {
	sync.Mutex
	version string
	support *protocolSupport
}

// Resolve once per adapter start so the executor and app-server use the same
// installed image even if Homebrew changes its public symlink during startup.
func compatibleExecutable(ctx context.Context, binary string) (string, *protocolSupport, error) {
	image, err := installedExecutable(binary)
	if err != nil {
		return "", nil, err
	}
	path := image.path
	installedProtocol.Lock()
	defer installedProtocol.Unlock()
	if installedProtocol.version == image.version && installedProtocol.support != nil {
		return path, installedProtocol.support, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	version, err := exec.CommandContext(probeCtx, path, "--version").Output()
	if err != nil {
		return "", nil, errors.New("无法读取 Codex 运行信息")
	}
	dir, err := os.MkdirTemp("", "shioji-protocol-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	if err = exec.CommandContext(probeCtx, path, "app-server", "generate-json-schema", "--experimental", "--out", dir).Run(); err != nil {
		return "", nil, fmt.Errorf("%s 无法提供可检查的 App Server 协议", strings.TrimSpace(string(version)))
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
	after, err := installedExecutable(path)
	if err != nil || after.version != image.version {
		return "", nil, errors.New("Codex 安装正在变化，请稍后重试")
	}
	installedProtocol.version, installedProtocol.support = image.version, support
	return path, support, nil
}
func (r *Runtime) supports(method string) bool {
	return r.protocol != nil && r.protocol.methods[method]
}
