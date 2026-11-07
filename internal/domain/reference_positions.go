package domain

// ReferencePosition retains inline placement in an editable input. Position is
// one-based in the text with one object rune per reference. It carries no
// harness syntax and is not passed to the execution adapter.
type ReferencePosition struct {
	Position     int    `json:"position"`
	AttachmentID string `json:"attachmentId,omitempty"`
	SkillID      string `json:"skillId,omitempty"`
}
