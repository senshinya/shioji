package app

import (
	"context"
	"shioji/internal/protocol"
	"testing"
)

func TestWorkspaceToolsRejectsOlderWorkersBeforeSending(t *testing.T) {
	_, err := executeJob(context.Background(), &protocol.Client{Minor: 25, Epoch: "epoch"}, Job{Method: "runtime.tools.inspect", epoch: "epoch", Input: protocol.ToolInspectionInput{WorkspaceID: "workspace", HarnessID: "custom"}})
	if err == nil {
		t.Fatal("old worker accepted scoped tools")
	}
	remote, ok := err.(*protocol.RemoteError)
	if !ok || remote.Code != "notSent" {
		t.Fatal("scoped query was sent to an old worker", err)
	}
}
