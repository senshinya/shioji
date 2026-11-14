package domain

// FileResource is a read-only file on its owning device. Version is opaque and
// binds every chunk to the same filesystem identity and modification revision.
type FileResource struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	MIME    string `json:"mime"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
}
type FileResourceChunk struct {
	Resource FileResource `json:"resource"`
	Data     []byte       `json:"data"`
	Offset   int64        `json:"offset"`
	Complete bool         `json:"complete"`
}
