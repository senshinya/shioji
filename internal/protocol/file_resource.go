package protocol

type FileResourceQuery struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	Path        string `json:"path"`
	Version     string `json:"version,omitempty"`
	Offset      int64  `json:"offset,omitempty"`
}
