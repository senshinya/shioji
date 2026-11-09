package app

import (
	"context"
	"errors"
	"shioji/internal/protocol"
	"testing"
)

func TestDirectoryBrowsingRequiresSupportingWorkerBeforeSending(t *testing.T) {
	client := &protocol.Client{Epoch: "owner", Minor: 22}
	_, err := executeJob(context.Background(), client, Job{epoch: "owner", Method: "device.directories", Input: protocol.DirectoryQuery{Path: "/Users/remote"}})
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) || remote.Code != "notSent" {
		t.Fatal("old worker received directory browsing", err)
	}
}
