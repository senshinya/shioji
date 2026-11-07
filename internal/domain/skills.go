package domain

// ID is an opaque harness-owned reference, returned unchanged to that adapter.
type SkillReference struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Skill struct {
	SkillReference
	Description string `json:"description"`
}
type SkillCatalog struct {
	Skills   []Skill  `json:"skills"`
	Warnings []string `json:"warnings,omitempty"`
}
