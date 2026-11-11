package domain

// CodeSelection uses half-open UTF-8 byte offsets into its original source
// side. Line/column metadata is one-based, computed by the owning worker.
type CodeSelection struct {
	Side        string `json:"side"` // base or target
	Start       int    `json:"start"`
	End         int    `json:"end"`
	StartLine   int    `json:"startLine"`
	EndLine     int    `json:"endLine"`
	StartColumn int    `json:"startColumn"`
	EndColumn   int    `json:"endColumn"`
	Text        string `json:"text"`
}

// CodeCommentContent is all the code context an adapter receives. Complete
// files used to restore the UI are separate resources, never part of this type.
type CodeCommentContent struct {
	FileSnapshot string          `json:"fileSnapshot,omitempty"`
	Comparison   DiffComparison  `json:"comparison"`
	Path         string          `json:"path"`
	Comment      string          `json:"comment"`
	Fragments    []CodeSelection `json:"fragments"`
}
type DiffCommentRecord struct {
	Content  CodeCommentContent `json:"content"`
	Snapshot Attachment         `json:"snapshot"`
}
