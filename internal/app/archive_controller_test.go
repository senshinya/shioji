package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestArchiveDeletionLeavesControllerCommandsAvailable(t *testing.T) {
	dir, err := os.MkdirTemp("", "sh-archive-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer close(release)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { conn.Close() })
				defer stop()
				codec, _, err := protocol.Welcome(conn, "device")
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
						result(codec, f, domain.Snapshot{DeviceID: "device", Generation: "g"})
					case "readiness":
						result(codec, f, domain.Readiness{State: "ready"})
					case "events.watch":
						<-ctx.Done()
						return
					case "events":
						result(codec, f, protocol.EventPage{Generation: "g"})
					case "session.delete":
						once.Do(func() { close(entered) })
						select {
						case <-ctx.Done():
							return
						case <-release:
						}
						result(codec, f, domain.ArchiveDeletion{DeviceID: "device", SessionIDs: []string{"archived"}})
					case "probe":
						result(codec, f, "responsive")
					default:
						result(codec, f, nil)
					}
				}
			}()
		}
	}()
	c := NewController(dir)
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	m := NewModel()
	deadline := time.After(3 * time.Second)
	for !m.Connected {
		select {
		case update := <-c.Updates:
			update(m)
		case <-deadline:
			t.Fatal("controller never connected")
		}
	}
	c.Do(Job{Method: "session.delete", Input: protocol.ArchiveDeleteInput{SessionID: "archived", Revision: 1, OperationID: "delete"}})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("deletion never reached its worker")
	}
	responsive := false
	c.Do(Job{Method: "probe", Apply: func(_ *Model, body json.RawMessage, err error) {
		responsive = err == nil && string(body) == `"responsive"`
	}})
	deadline = time.After(250 * time.Millisecond)
	for !responsive {
		select {
		case update := <-c.Updates:
			update(m)
		case <-deadline:
			t.Fatal("pending archive deletion blocked unrelated controller commands")
		}
	}
}

func TestArchiveDeletionDoesNotSendAfterConnectionChangesDuringDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewController("")
	c.epoch = "old"
	entered, release := make(chan struct{}), make(chan struct{})
	commands := make(chan protocol.Frame, 1)
	c.Dial = func(ctx context.Context) (net.Conn, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			codec, _, err := protocol.Welcome(b, "device")
			if err == nil {
				var f protocol.Frame
				if codec.Read(&f) == nil {
					commands <- f
				}
			}
		}()
		return a, nil
	}
	var receiptError error
	c.startArchiveDelete(ctx, &protocol.Client{Epoch: "old", DeviceID: "device", Minor: protocol.Minor}, Job{
		epoch: "old", Method: "session.delete", Input: protocol.ArchiveDeleteInput{SessionID: "old-session", Revision: 1},
		Apply: func(_ *Model, _ json.RawMessage, err error) { receiptError = err },
	})
	<-entered
	c.mu.Lock()
	c.epoch = "new"
	c.mu.Unlock()
	close(release)
	select {
	case update := <-c.Updates:
		update(NewModel())
	case <-time.After(time.Second):
		t.Fatal("stale deletion did not settle")
	}
	var remote *protocol.RemoteError
	if !errors.As(receiptError, &remote) || remote.Code != "notSent" {
		t.Fatal("stale deletion was not rejected before sending", receiptError)
	}
	select {
	case command := <-commands:
		t.Fatal("stale deletion reached the replacement connection", command.Method)
	default:
	}
}
