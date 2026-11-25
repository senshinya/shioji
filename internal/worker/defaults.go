package worker

// Composition boundary only. Shared worker orchestration never imports a
// concrete adapter. Additional built-ins are registered here or by the CLI.
import (
	"shioji/internal/harness"
	"shioji/internal/harness/antigravity"
	"shioji/internal/harness/claudecode"
	"shioji/internal/harness/codex"
)

// SystemAdapters provides device-local harnesses for all worker entry points.
func SystemAdapters(codexExecutable string, stateDirectory ...string) []harness.Adapter {
	state := ""
	if len(stateDirectory) > 0 {
		state = stateDirectory[0]
	}
	return []harness.Adapter{codex.Adapter{Executable: codexExecutable}, antigravity.Adapter{}, codex.NewDeepSeekAdapter(codexExecutable),
		codex.NewResponsesAdapter(codexExecutable, state), claudecode.Adapter{State: state}}
}

// Prior single-harness worker versions kept Codex's catalog in the root.
func legacyCatalogHarnessID() string { return codex.Adapter{}.Descriptor().ID }
