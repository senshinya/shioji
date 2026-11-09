package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestMigrationDoesNotSendWhenIntentCannotBeSaved(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "sent")
	binary := filepath.Join(dir, "helper")
	if e := os.WriteFile(binary, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	c := NewDeviceController(dir, "device", nil, binary, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	done := false
	c.Do(Job{Method: "$worker.migration", Input: WorkerMigrationAction{Action: "start", Target: WorkerMigrationTarget{Info: transport.WorkerInfo{Directory: dir, Worker: binary, DeviceID: "device"}, Input: domain.WorkerMigrationInput{ID: "operation", DeviceID: "device", OwnerRef: "owner"}}}, Prepare: func() error { return errors.New("disk unavailable") }, Apply: func(_ *Model, _ json.RawMessage, e error) {
		var remote *protocol.RemoteError
		if !errors.As(e, &remote) || remote.Code != "notSent" {
			t.Error("missing not-sent result", e)
		}
		done = true
	}})
	m := NewModel()
	deadline := time.After(3 * time.Second)
	for !done {
		select {
		case apply := <-c.Updates:
			apply(m)
		case <-deadline:
			t.Fatal("no result")
		}
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("helper executed before durable intent", e)
	}
}
