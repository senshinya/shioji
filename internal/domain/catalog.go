package domain

// CatalogCursor belongs to the pinned read-only directory snapshot.
type CatalogCursor struct {
	Section int   `json:"section"`
	Row     int64 `json:"row"`
}
