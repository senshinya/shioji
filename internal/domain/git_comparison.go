package domain

// DiffEndpoint is a read-only repository version. Commit pins a resolved ref;
// names remain display data and never imply a checkout.
type DiffEndpoint struct {
	Kind   string `json:"kind"` // head, worktree, branch, remote, empty
	Name   string `json:"name,omitempty"`
	Commit string `json:"commit,omitempty"`
	Branch string `json:"branch,omitempty"`
}
type DiffComparison struct {
	Repository string       `json:"repository,omitempty"`
	Base       DiffEndpoint `json:"base"`
	Target     DiffEndpoint `json:"target"`
}

func DefaultDiffComparison() DiffComparison {
	return DiffComparison{Base: DiffEndpoint{Kind: "head"}, Target: DiffEndpoint{Kind: "worktree"}}
}

type ComparisonResult struct {
	Versions   *FileVersions  `json:"versions,omitempty"`
	Comparison DiffComparison `json:"comparison"`
	Files      []ChangedFile  `json:"files"`
}
type DiffRefCatalog struct {
	Refs []DiffEndpoint `json:"refs"`
}
