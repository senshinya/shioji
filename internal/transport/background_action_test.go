package transport

import (
	"context"
	"testing"
)

func TestRuntimeHelperStartFailureIsKnownUnstarted(t *testing.T) {
	result, err := runRuntimeHelper(context.Background(), "/nonexistent/shioji-test-helper", "unregister")
	if err == nil || !result.NotStarted {
		t.Fatal("failed exec was not distinguished from an unknown helper outcome", result, err)
	}
}

func TestRuntimeHelperInvalidReplyDoesNotClaimUnstarted(t *testing.T) {
	result, err := runRuntimeHelper(context.Background(), "/usr/bin/false", "unregister")
	if err == nil || result.NotStarted {
		t.Fatal("started helper with no receipt was declared unstarted", result, err)
	}
}
