// Package wire is private to the Codex adapter's pinned 0.166.0 protocol.
package wire

import "encoding/json"

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type Item struct {
	Fields           map[string]json.RawMessage `json:"-"`
	ID               string                     `json:"id"`
	Type             string                     `json:"type"`
	Text             string                     `json:"text"`
	Command          string                     `json:"command"`
	AggregatedOutput string                     `json:"aggregatedOutput"`
	Status           string                     `json:"status"`
	ExitCode         *int                       `json:"exitCode"`
	Content          json.RawMessage            `json:"content"`
}

func (i *Item) UnmarshalJSON(data []byte) error {
	type plain Item
	if err := json.Unmarshal(data, (*plain)(i)); err != nil {
		return err
	}
	return json.Unmarshal(data, &i.Fields)
}

type Turn struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	StartedAt   *int64 `json:"startedAt"`
	CompletedAt *int64 `json:"completedAt"`
	DurationMS  *int64 `json:"durationMs"`
	Error       *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type Counters struct {
	Input  *int64 `json:"inputTokens"`
	Output *int64 `json:"outputTokens"`
	Cached *int64 `json:"cachedInputTokens"`
}
type Params struct {
	ThreadName  *string `json:"threadName"`
	Explanation string  `json:"explanation"`
	Plan        []struct {
		Step   string `json:"step"`
		Status string `json:"status"`
	} `json:"plan"`
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	ItemID    string          `json:"itemId"`
	Delta     string          `json:"delta"`
	Turn      Turn            `json:"turn"`
	Item      Item            `json:"item"`
	RequestID json.RawMessage `json:"requestId"`
	Command   string          `json:"command"`
	CWD       string          `json:"cwd"`
	Reason    string          `json:"reason"`
	Questions []struct {
		ID       string `json:"id"`
		Header   string `json:"header"`
		Question string `json:"question"`
		IsOther  bool   `json:"isOther"`
		IsSecret bool   `json:"isSecret"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
	TokenUsage struct {
		Total Counters `json:"total"`
		Last  Counters `json:"last"`
	} `json:"tokenUsage"`
}
type Model struct {
	Model                     string `json:"model"`
	DisplayName               string `json:"displayName"`
	IsDefault                 bool   `json:"isDefault"`
	DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
	ServiceTiers []struct {
		ID string `json:"id"`
	} `json:"serviceTiers"`
}
type ThreadParams struct {
	Environments          []ExecutionEnvironment `json:"environments,omitempty"`
	RuntimeWorkspaceRoots []string               `json:"runtimeWorkspaceRoots,omitempty"`
	CWD                   string                 `json:"cwd"`
	Model                 string                 `json:"model"`
	ApprovalPolicy        string                 `json:"approvalPolicy"`
	Sandbox               string                 `json:"sandbox"`
	Config                map[string]any         `json:"config,omitempty"`
}
type Input struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
type SandboxPolicy struct {
	Type          string   `json:"type"`
	WritableRoots []string `json:"writableRoots,omitempty"`
	NetworkAccess bool     `json:"networkAccess,omitempty"`
}
type TurnParams struct {
	Environments        []ExecutionEnvironment `json:"environments,omitempty"`
	ThreadID            string                 `json:"threadId"`
	Input               []Input                `json:"input"`
	ClientUserMessageID string                 `json:"clientUserMessageId"`
	Model               string                 `json:"model"`
	Effort              string                 `json:"effort,omitempty"`
	ApprovalPolicy      string                 `json:"approvalPolicy"`
	SandboxPolicy       SandboxPolicy          `json:"sandboxPolicy"`
	ServiceTier         *string                `json:"serviceTier"`
	CollaborationMode   *CollaborationMode     `json:"collaborationMode,omitempty"`
}

type ExecutionEnvironment struct {
	EnvironmentID         string   `json:"environmentId"`
	CWD                   string   `json:"cwd"`
	RuntimeWorkspaceRoots []string `json:"runtimeWorkspaceRoots,omitempty"`
}
type CollaborationMode struct {
	Mode     string       `json:"mode"`
	Settings ModeSettings `json:"settings"`
}
type ModeSettings struct {
	Model                 string  `json:"model"`
	Effort                *string `json:"reasoning_effort"`
	DeveloperInstructions *string `json:"developer_instructions"`
}
