package domain

// ForkContext is the read-only transcript passed to the independent runtime
// on a portable fork's first input. Entries are oldest to newest.
type ForkContext struct {
	Entries []ForkContextEntry `json:"entries"`
	Omitted bool               `json:"omitted,omitempty"`
}

type ForkContextEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
}
