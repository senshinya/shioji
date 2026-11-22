package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestLocalControllerStartsWorkerOnLaunchAndAfterDisconnect(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "shioji-start-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	worker := filepath.Join(dir, "shioji-worker")
	// Stand in for the executable only. The controller launches the process
	// and connects over its normal Unix socket.
	if err := os.WriteFile(worker, []byte("#!/bin/sh\nprintf 'started\\n' >> \"$2/launches\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	controller := NewDeviceController(dir, "device", nil, worker, nil).fleet.sources["local"].controller
	done := make(chan struct{})
	go func() { defer close(done); controller.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	model := NewModel()
	wait := func(description string, ready func() bool) {
		t.Helper()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for !ready() {
			select {
			case update := <-controller.Updates:
				update(model)
			case <-tick.C:
			case <-ctx.Done():
				t.Fatalf("%s: %s", description, model.ConnectionError)
			}
		}
	}
	launches := func() string {
		data, _ := os.ReadFile(filepath.Join(dir, "launches"))
		return string(data)
	}
	serve := func(generation string) func() {
		t.Helper()
		l, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
		if err != nil {
			t.Fatal(err)
		}
		lifetime, stop := context.WithCancel(ctx)
		closeWorker := func() { l.Close(); stop() }
		t.Cleanup(closeWorker)
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				go func() {
					defer conn.Close()
					defer context.AfterFunc(lifetime, func() { conn.Close() })()
					codec, _, err := protocol.WelcomeWorker(conn, "device", generation, domain.WorkerBuild{})
					if err != nil {
						return
					}
					for {
						var f protocol.Frame
						if codec.Read(&f) != nil {
							return
						}
						switch f.Method {
						case "snapshot":
							result(codec, f, domain.Snapshot{DeviceID: "device", Generation: generation})
						case "readiness":
							result(codec, f, domain.Readiness{State: "ready"})
						case "events.watch":
							<-lifetime.Done()
							return
						}
					}
				}()
			}
		}()
		return closeWorker
	}
	wait("client did not start the missing worker", func() bool { return launches() == "started\n" })
	closeFirst := serve("first")
	wait("client did not connect to the started worker", func() bool { return model.Connected && model.Snapshot.Generation == "first" })
	closeFirst()
	wait("client did not restart the worker after disconnect", func() bool { return launches() == "started\nstarted\n" })
	serve("second")
	wait("client did not reconnect to the same device", func() bool { return model.Connected && model.Snapshot.Generation == "second" })
}
