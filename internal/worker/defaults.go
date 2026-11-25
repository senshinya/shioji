package worker

// Composition boundary only. Shared worker orchestration never imports a
// concrete adapter. Additional built-ins are registered here or by the CLI.
import (
	"shioji/internal/harness"
	"shioji/internal/harness/antigravity"
	"shioji/internal/harness/codex"
)

// SystemAdapters provides device-local harnesses for all worker entry points.
func SystemAdapters(codexExecutable string) []harness.Adapter {
	return []harness.Adapter{codex.Adapter{Executable: codexExecutable}, antigravity.Adapter{}, codex.NewDeepSeekAdapter(codexExecutable)}
}

// Prior single-harness worker versions kept Codex's catalog in the root.
func legacyCatalogHarnessID() string { return codex.Adapter{}.Descriptor().ID }
