package domain

// HookScope describes the ownership of an automation definition, independently
// of its harness's configuration keys or source identifiers.
type HookScope string

const (
	HookProject HookScope = "project"
	HookUser    HookScope = "user"
	HookPlugin  HookScope = "plugin"
	HookManaged HookScope = "managed"
	HookUnknown HookScope = "unknown"
)

type HookTrust string

const (
	HookUntrusted HookTrust = "untrusted"
	HookModified  HookTrust = "modified"
	HookTrusted   HookTrust = "trusted"
	HookPolicy    HookTrust = "policy"
)

// IDs and definition revisions are opaque adapter references. Paths and the
// fingerprint are display metadata; shared code must never open or parse them.
type HookEntry struct {
	ID                 string    `json:"id"`
	DefinitionRevision string    `json:"definitionRevision"`
	Event              string    `json:"event"`
	Kind               string    `json:"kind"`
	Command            string    `json:"command,omitempty"`
	Service            string    `json:"service,omitempty"`
	Tool               string    `json:"tool,omitempty"`
	Scope              HookScope `json:"scope"`
	Source             string    `json:"source"`
	SourcePath         string    `json:"sourcePath"`
	Plugin             string    `json:"plugin,omitempty"`
	Matcher            string    `json:"matcher,omitempty"`
	StatusMessage      string    `json:"statusMessage,omitempty"`
	Fingerprint        string    `json:"fingerprint"`
	TimeoutSeconds     uint64    `json:"timeoutSeconds"`
	Async              bool      `json:"async"`
	Enabled            bool      `json:"enabled"`
	Trust              HookTrust `json:"trust"`
	CanEnable          bool      `json:"canEnable"`
	CanTrust           bool      `json:"canTrust"`
	ReadOnlyReason     string    `json:"readOnlyReason,omitempty"`
}

type HookCatalog struct {
	Revision     string      `json:"revision"`
	Directory    string      `json:"directory,omitempty"`
	RuntimeKnown bool        `json:"runtimeKnown"`
	Entries      []HookEntry `json:"entries"`
	Warnings     []string    `json:"warnings,omitempty"`
}

type HookAction string

const (
	HookSetEnabled      HookAction = "setEnabled"
	HookTrustDefinition HookAction = "trustDefinition"
)

type HookChange struct {
	OperationID        string     `json:"operationId"`
	ID                 string     `json:"id"`
	Revision           string     `json:"revision"`
	DefinitionRevision string     `json:"definitionRevision"`
	Action             HookAction `json:"action"`
	Enabled            bool       `json:"enabled"`
}

type HookChangeResult struct {
	Saved bool `json:"saved"`
}
