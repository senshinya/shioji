package app

import (
	"context"
	"net"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// Older workers hydrate through `session`, but the presentation still needs a
// bounded pending/error lifecycle instead of an indefinite initial spinner.
func TestLegacyHistoryPublishesReadLifecycle(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release := make(chan struct{}, 1), make(chan struct{})
			c := NewController("")
			c.Dial = func(lifetime context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					stop := context.AfterFunc(lifetime, func() { server.Close() })
					defer stop()
					codec := protocol.NewCodec(server, server)
					var hello protocol.Frame
					if codec.Read(&hello) != nil || codec.Write(protocol.Frame{Type: "welcome", Major: protocol.Major, Minor: 36, Nonce: hello.Nonce, DeviceID: "legacy-device", Epoch: "legacy-epoch"}) != nil {
						return
					}
					for {
						var f protocol.Frame
						if codec.Read(&f) != nil {
							return
						}
						session := domain.Session{ID: "s", WorkspaceID: "w", ThreadID: "source", Revision: 1}
						switch f.Method {
						case "snapshot":
							result(codec, f, domain.Snapshot{DeviceID: "legacy-device", Generation: "g", Sessions: []domain.Session{session}})
						case "readiness":
							result(codec, f, domain.Readiness{State: domain.RuntimeReady})
						case "events.watch":
							<-lifetime.Done()
							return
						case "session":
							started <- struct{}{}
							select {
							case <-release:
							case <-lifetime.Done():
								return
							}
							if fail {
								codec.Write(protocol.Frame{Type: "error", ID: f.ID, Epoch: f.Epoch, DeviceID: f.DeviceID, ErrorCode: "unavailable", Error: "history unavailable"})
							} else {
								result(codec, f, domain.SessionDetail{Session: session})
							}
						default:
							result(codec, f, struct{}{})
						}
					}
				}()
				return client, nil
			}
			c.Select("s")
			done := make(chan struct{})
			go func() { defer close(done); c.Run(ctx) }()
			defer func() { cancel(); <-done }()
			m := NewModel()
			pump := func(ready func() bool) {
				t.Helper()
				timer := time.NewTimer(2 * time.Second)
				defer timer.Stop()
				for !ready() {
					select {
					case update := <-c.Updates:
						update(m)
					case <-timer.C:
						t.Fatal("legacy history did not publish its current lifecycle", m.HistoryReads)
					}
				}
			}
			pump(func() bool { return m.HistoryReads["s"] != nil && m.HistoryReads["s"].Pending })
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("legacy history never reached the worker")
			}
			close(release)
			if fail {
				pump(func() bool {
					r := m.HistoryReads["s"]
					return r != nil && !r.Pending && r.Error == "history unavailable"
				})
			} else {
				pump(func() bool { return m.Details["s"].Session.ID == "s" && m.HistoryReads["s"] == nil })
			}
		})
	}
}
