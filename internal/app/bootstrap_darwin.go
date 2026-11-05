package app

import (
	"context"
	"shioji/internal/transport"
)

// EnsureWorker starts the bundled developer worker only when no local owner is
// reachable. Release builds use the runtime service host.
func EnsureWorker(ctx context.Context, directory, binary string) error {
	_, err := transport.EnsureLocalWorker(ctx, directory, binary, "")
	return err
}
