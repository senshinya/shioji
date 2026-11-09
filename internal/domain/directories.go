package domain

// Directory paths belong to the responding device, before a workspace exists.
type DirectoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type DirectoryPage struct {
	Path    string           `json:"path"`
	Parent  string           `json:"parent"`
	Home    string           `json:"home"`
	Entries []DirectoryEntry `json:"entries"`
	Next    int              `json:"next"`
	More    bool             `json:"more"`
}
