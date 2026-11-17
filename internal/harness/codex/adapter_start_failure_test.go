package codex

import (
	"context"
	"shioji/internal/harness"
	"testing"
)

func TestAdapterStartupFailureReturnsNoRuntime(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "missing-executable"
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			name = "canceled"
			cancel()
		}
		t.Run(name, func(t *testing.T) {
			runtime, err := (Adapter{Executable: "/nonexistent/shioji-codex-test"}).Start(ctx, harness.StartOptions{})
			if err == nil {
				t.Fatal("fixture startup unexpectedly succeeded")
			}
			if runtime != nil {
				t.Fatal("failed native start returned a nonnil Runtime interface; canceled cleanup would call Close on a nil adapter")
			}
		})
		cancel()
	}
}
