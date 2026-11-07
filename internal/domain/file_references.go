package domain

// WorkspaceFile is a device-owned path under an explicitly accepted root.
// The client returns this identity to that device, never opens it locally.
type WorkspaceFile struct {
	Root string `json:"root"`
	Path string `json:"path"`
}
type FileSearchResult struct {
	Files     []WorkspaceFile `json:"files"`
	Truncated bool            `json:"truncated,omitempty"`
}
