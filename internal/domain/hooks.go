package domain

import "slices"

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
	HookSetEnabled       HookAction = "setEnabled"
	HookTrustDefinition  HookAction = "trustDefinition"
	HookTrustDefinitions HookAction = "trustDefinitions"
)

type HookDefinitionRef struct {
	ID                 string `json:"id"`
	DefinitionRevision string `json:"definitionRevision"`
}

type HookChange struct {
	OperationID        string              `json:"operationId"`
	ID                 string              `json:"id,omitempty"`
	Revision           string              `json:"revision"`
	DefinitionRevision string              `json:"definitionRevision,omitempty"`
	Action             HookAction          `json:"action"`
	Enabled            bool                `json:"enabled"`
	Definitions        []HookDefinitionRef `json:"definitions,omitempty"`
}

func (in HookChange) Valid() bool {
	if in.Revision == "" || len(in.Revision) > 512 {
		return false
	}
	switch in.Action {
	case HookSetEnabled, HookTrustDefinition:
		return len(in.Definitions) == 0 && (in.Action == HookSetEnabled || !in.Enabled) &&
			in.ID != "" && len(in.ID) <= 512 && in.DefinitionRevision != "" && len(in.DefinitionRevision) <= 512
	case HookTrustDefinitions:
		if in.ID != "" || in.DefinitionRevision != "" || in.Enabled || len(in.Definitions) == 0 {
			return false
		}
		seen := make(map[string]bool, len(in.Definitions))
		for _, target := range in.Definitions {
			if target.ID == "" || len(target.ID) > 512 || target.DefinitionRevision == "" || len(target.DefinitionRevision) > 512 || seen[target.ID] {
				return false
			}
			seen[target.ID] = true
		}
		return true
	default:
		return false
	}
}

// MatchingEntries resolves all explicitly reviewed versions in request order.
// Callers separately check the catalog revision before a write.
func (in HookChange) MatchingEntries(catalog HookCatalog) []HookEntry {
	targets := in.Definitions
	if in.Action != HookTrustDefinitions {
		targets = []HookDefinitionRef{{ID: in.ID, DefinitionRevision: in.DefinitionRevision}}
	}
	byID := make(map[string]HookEntry, len(catalog.Entries))
	for _, h := range catalog.Entries {
		byID[h.ID] = h
	}
	matches := make([]HookEntry, 0, len(targets))
	for _, target := range targets {
		h, ok := byID[target.ID]
		if !ok || h.DefinitionRevision != target.DefinitionRevision {
			return nil
		}
		matches = append(matches, h)
	}
	return matches
}

func (in HookChange) Allowed(h HookEntry) bool {
	switch in.Action {
	case HookSetEnabled:
		return h.CanEnable
	case HookTrustDefinition, HookTrustDefinitions:
		return h.CanTrust
	default:
		return false
	}
}

func (in HookChange) Observed(catalog HookCatalog) bool {
	entries := in.MatchingEntries(catalog)
	for _, h := range entries {
		if in.Action == HookSetEnabled && h.Enabled != in.Enabled || in.Action != HookSetEnabled && h.Trust != HookTrusted {
			return false
		}
	}
	return len(entries) != 0
}

type HookChangeResult struct {
	Saved   bool
	Catalog HookCatalog
}

// A management context belongs to the owning worker. Empty workspace/session
// identifiers mean device-global management, not a client-supplied path.
type HookRequest struct {
	HarnessID   string `json:"harnessId"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
	HookChange
}

func (in HookRequest) Equal(other HookRequest) bool {
	return in.HarnessID == other.HarnessID && in.WorkspaceID == other.WorkspaceID && in.SessionID == other.SessionID &&
		in.OperationID == other.OperationID && in.ID == other.ID && in.Revision == other.Revision &&
		in.DefinitionRevision == other.DefinitionRevision && in.Action == other.Action && in.Enabled == other.Enabled &&
		slices.Equal(in.Definitions, other.Definitions)
}

type HookOperation struct {
	Input           HookRequest    `json:"input"`
	State           OperationState `json:"state"`
	Saved           bool           `json:"saved"`
	AdoptionPending bool           `json:"adoptionPending,omitempty"`
	Message         string         `json:"message,omitempty"`
	// Catalog accompanies a fresh save response. Durable receipts retain
	// the operation identity and outcome, not a potentially stale catalog.
	Catalog *HookCatalog `json:"catalog,omitempty"`
}
