package domain

// WorkerBuild identifies the running service independently of harness readiness
// and protocol compatibility. BuildID is captured before replacing any file.
type WorkerBuild struct {
	BuildID      string `json:"buildId"`
	Revision     string `json:"revision,omitempty"`
	BuiltAt      int64  `json:"builtAt,omitempty"`
	Schema       int    `json:"schema"`
	SafeUpdate   bool   `json:"safeUpdate"`
	ContextUsage bool   `json:"contextUsage"`
	OutputFiles  bool   `json:"outputFiles"`
}

type WorkerReplacement struct {
	ExpectedBuildID string `json:"expectedBuildId"`
	BuildID         string `json:"buildId"`
}

type WorkerUpdateResult struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
	Backup string `json:"backup,omitempty"`
}
