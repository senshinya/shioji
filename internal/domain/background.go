package domain

// Registration describes system authorization, separately from the currently
// connected worker's Managed flag. Enabled is not a claim of active ownership.
type BackgroundService struct {
	Format  int    `json:"format"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}
