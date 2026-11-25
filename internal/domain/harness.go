package domain

// These are Shioji operations. Adapters report actual support; absent support
// never authorizes a substitute operation or a guessed configuration.
type HarnessCapabilities struct {
	ExclusivePlan         bool `json:"exclusivePlan,omitempty"`
	WorkerGoals           bool `json:"workerGoals,omitempty"`
	Goals                 bool `json:"goals,omitempty"`
	PathAttachments       bool `json:"pathAttachments,omitempty"`
	Reload                bool `json:"reload,omitempty"`
	ConversationImport    bool `json:"conversationImport,omitempty"`
	Compact               bool `json:"compact,omitempty"`
	CompactNeedsCheck     bool `json:"compactNeedsCheck,omitempty"`
	ToolServiceInspection bool `json:"toolServiceInspection,omitempty"`
	ToolServices          bool `json:"toolServices,omitempty"`
	Skills                bool `json:"skills,omitempty"`
	SkillManagement       bool `json:"skillManagement,omitempty"`
	HookManagement        bool `json:"hookManagement,omitempty"`
	HookInspection        bool `json:"hookInspection,omitempty"`
	Steer                 bool `json:"steer"`
	Interrupt             bool `json:"interrupt"`
	Recovery              bool `json:"recovery"`
	Resume                bool `json:"resume,omitempty"`
	Continue              bool `json:"continue"`
	PortableFork          bool `json:"portableFork,omitempty"`
	Fork                  bool `json:"fork"`
	Restart               bool `json:"restart"`
}

// CanRequestCompaction includes a worker that can verify compaction on demand
// when an idle session has no observed runtime.
func (c HarnessCapabilities) CanRequestCompaction() bool { return c.Compact || c.CompactNeedsCheck }

// CanReopenIdle is independent of recovery of an unfinished input.
func (c HarnessCapabilities) CanReopenIdle() bool { return c.Resume || c.Continue }

type HarnessDescriptor struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon,omitempty"`
	ProfileOnly bool   `json:"profileOnly,omitempty"`
}
type HarnessInfo struct {
	Profile           *AgentProfile        `json:"profile,omitempty"`
	SystemProfile     bool                 `json:"systemProfile,omitempty"`
	ProfileModes      []AgentProfileMode   `json:"profileModes,omitempty"`
	Account           *RuntimeAccount      `json:"account,omitempty"`
	Configured        bool                 `json:"configured"`
	Installation      *RuntimeInstallation `json:"installation,omitempty"`
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Icon              string               `json:"icon,omitempty"`
	State             ReadinessState       `json:"state"`
	Error             string               `json:"error,omitempty"`
	Models            []Model              `json:"models,omitempty"`
	Permissions       []string             `json:"permissions,omitempty"`
	PermissionChoices []OptionChoice       `json:"permissionChoices,omitempty"`
	Plan              bool                 `json:"plan"`
	Capabilities      HarnessCapabilities  `json:"capabilities"`
}

// HasSystemProfile also recognizes native configurations reported by workers
// that predate the persisted system Profile catalog.
func (h HarnessInfo) HasSystemProfile() bool {
	if h.ID == "" || h.Profile != nil {
		return false
	}
	return h.SystemProfile || h.State == RuntimeReady ||
		h.Account != nil && (h.Account.Mode == "api" || h.Account.State == "authenticated" || h.Account.State == "configured") ||
		h.Installation != nil && h.Installation.Issue == RuntimeAuthenticationExpired
}

const (
	AgentResponses = "responses"
	AgentAnthropic = "anthropic"
)

func IsCustomAgent(id string) bool { return id == AgentResponses || id == AgentAnthropic }

type AgentProfileMode string

const (
	AgentProfileAccount AgentProfileMode = "account"
	AgentProfileAPI     AgentProfileMode = "api"
)

// AgentProfile identifies an isolated configuration on the execution device.
// ID is also its catalog and session routing key. The original installation
// keeps using its existing harness ID and native configuration.
type AgentProfile struct {
	ID         string           `json:"id"`
	HarnessID  string           `json:"harnessId"`
	Name       string           `json:"name"`
	Mode       AgentProfileMode `json:"mode"`
	Executable string           `json:"executable,omitempty"`
	Disabled   bool             `json:"disabled,omitempty"`
	Authorized bool             `json:"authorized,omitempty"`
	Icon       string           `json:"icon,omitempty"`
}
type OptionChoice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Live        bool   `json:"live,omitempty"`
}

func (r Readiness) LivePermissionChange(from, to string) bool {
	if from != to {
		for _, choice := range r.PermissionChoices {
			if choice.Live && (choice.ID == from || choice.ID == to) {
				return true
			}
		}
	}
	return false
}

// ForHarness keeps each catalog separate. The top-level projection remains the
// default catalog for older clients; explicit unknown IDs never use it.
func (r Readiness) ForHarness(id string) (Readiness, bool) {
	if id == "" {
		id = r.HarnessID
	}
	if len(r.Harnesses) == 0 {
		return r, id == "" || id == r.HarnessID
	}
	for _, h := range r.Harnesses {
		if h.ID == id {
			return Readiness{Account: h.Account, Sessions: r.Sessions, HarnessID: h.ID, Harnesses: r.Harnesses, State: h.State, Error: h.Error, Models: h.Models, Permissions: h.Permissions, PermissionChoices: h.PermissionChoices, Plan: h.Plan, Capabilities: h.Capabilities}, true
		}
	}
	return Readiness{}, false
}

// HarnessKind resolves a profile selector to the adapter's type for presentation.
func (r Readiness) HarnessKind(id string) string {
	for _, info := range r.Harnesses {
		if info.ID == id && info.Profile != nil {
			return info.Profile.HarnessID
		}
	}
	for _, info := range r.Sessions {
		if info.ID == id && info.Profile != nil {
			return info.Profile.HarnessID
		}
	}
	return id
}
func (r Readiness) Descriptor() HarnessDescriptor {
	for _, h := range r.Harnesses {
		if h.ID == r.HarnessID {
			return HarnessDescriptor{ID: h.ID, Name: h.Name, Icon: h.Icon}
		}
	}
	return HarnessDescriptor{ID: r.HarnessID, Name: "Agent"}
}

// An incomplete catalog cannot establish that a model was removed.
func (r Readiness) ModelUnavailable(id string) bool {
	if r.State != RuntimeReady || id == "" {
		return false
	}
	for _, model := range r.Models {
		if model.ID == id {
			return false
		}
	}
	return true
}

const ModelUnavailableMessage = "当前模型已不可用，请更换模型"
