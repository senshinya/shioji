package domain

// SkillScope describes ownership, independently of the harness's wire names.
type SkillScope string

const (
	SkillProject SkillScope = "project"
	SkillUser    SkillScope = "user"
	SkillBuiltIn SkillScope = "builtIn"
	SkillManaged SkillScope = "managed"
	SkillUnknown SkillScope = "unknown"
)

// IDs and revisions belong to the owning adapter. Paths are display metadata,
// never filenames that shared code may open or use as configuration selectors.
type ManagedSkill struct {
	SkillReference
	Description    string     `json:"description"`
	Scope          SkillScope `json:"scope"`
	Directory      string     `json:"directory"`
	Path           string     `json:"path"`
	Enabled        bool       `json:"enabled"`
	CanEnable      bool       `json:"canEnable"`
	ReadOnlyReason string     `json:"readOnlyReason,omitempty"`
}
type SkillInventory struct {
	Revision string         `json:"revision"`
	Entries  []ManagedSkill `json:"entries"`
	Warnings []string       `json:"warnings,omitempty"`
}
type SkillDocument struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type SkillChange struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Enabled  bool   `json:"enabled"`
}
type SkillChangeResult struct {
	Saved   bool `json:"saved"`
	Enabled bool `json:"enabled"`
}
