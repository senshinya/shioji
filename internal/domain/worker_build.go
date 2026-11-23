package domain

const RuntimeHostRegistrationRefresh = 2

// WorkerBuild identifies the running service independently of harness readiness
// and protocol compatibility. BuildID is captured before replacing any file.
type WorkerBuild struct {
	RuntimeHost    int    `json:"runtimeHost,omitempty"`
	RuntimeHosted  bool   `json:"runtimeHosted,omitempty"`
	PrivateControl int    `json:"privateControl,omitempty"`
	Lifecycle      int    `json:"lifecycle,omitempty"`
	RecoveryHelper bool   `json:"recoveryHelper,omitempty"`
	ForceRecovery  bool   `json:"forceRecovery,omitempty"`
	Maintenance    bool   `json:"maintenance,omitempty"`
	Managed        bool   `json:"managed,omitempty"`
	BuildID        string `json:"buildId"`
	Revision       string `json:"revision,omitempty"`
	BuiltAt        int64  `json:"builtAt,omitempty"`
	Schema         int    `json:"schema"`
	MigrationFrom  int    `json:"migrationFrom,omitempty"`
	SafeUpdate     bool   `json:"safeUpdate"`
	ContextUsage   bool   `json:"contextUsage"`
	OutputFiles    bool   `json:"outputFiles"`
}

type WorkerReplacement struct {
	ID              string `json:"id,omitempty"`
	Policy          string `json:"policy,omitempty"`
	Explicit        bool   `json:"explicit,omitempty"`
	ExpectedBuildID string `json:"expectedBuildId"`
	BuildID         string `json:"buildId"`
	SchemaMigration bool   `json:"schemaMigration,omitempty"`
	RuntimeHandoff  bool   `json:"runtimeHandoff,omitempty"`
}

func (b *WorkerBuild) MigratesSchema(from int) bool {
	return b != nil && b.MigrationFrom > 0 && from >= b.MigrationFrom && from < b.Schema
}

type WorkerUpdateResult struct {
	OperationID string `json:"operationId,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Ready       bool   `json:"ready"`
	Reason      string `json:"reason,omitempty"`
	Backup      string `json:"backup,omitempty"`
}
