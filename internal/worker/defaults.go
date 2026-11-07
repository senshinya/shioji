package worker

// Composition boundary only. Shared worker orchestration never imports a
// concrete adapter. Additional built-ins are registered here or by the CLI.
import (
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
)

func defaultAdapters() []harness.Adapter { return []harness.Adapter{codex.Adapter{}} }

// Prior single-harness worker versions kept Codex's catalog in the root.
func legacyCatalogHarnessID() string { return codex.Adapter{}.Descriptor().ID }
