package domain

// Registration describes system authorization, separately from the currently
// connected worker's Managed flag. Enabled is not a claim of active ownership.
type BackgroundService struct {
	Format  int    `json:"format"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	// NotStarted certifies that the requested helper action never launched.
	// Its zero value leaves an unsuccessful operation's outcome unconfirmed.
	NotStarted bool `json:"notStarted,omitempty"`
}
