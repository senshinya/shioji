package transport

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/protocol"
	"testing"
)

func legacyInspectionFixture(t *testing.T, state string, version ...int) string {
	minor := 3
	if len(version) > 0 {
		minor = version[0]
	}
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "shioji-legacy-inspect-")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(filepath.Join(dir, "worker.sock"), 0600)
	t.Cleanup(func() { listener.Close(); os.RemoveAll(dir) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				codec := protocol.NewCodec(conn, conn)
				var h protocol.Frame
				if codec.Read(&h) != nil || h.Major != 1 {
					return
				}
				codec.Write(protocol.Frame{Type: "welcome", Major: 1, Minor: minor, Nonce: h.Nonce, DeviceID: "device", Epoch: "epoch"})
				var q protocol.Frame
				if codec.Read(&q) != nil {
					return
				}
				if q.Method != "snapshot" {
					t.Error("non-snapshot probe", q.Method)
					return
				}
				body := []byte(`{"deviceId":"device","sessions":[{"id":"s","status":"` + state + `"}],"operations":[]}`)
				codec.Write(protocol.Frame{Type: "result", ID: q.ID, DeviceID: "device", Epoch: "epoch", Body: body})
				var detail protocol.Frame
				if codec.Read(&detail) == nil {
					if detail.Method != "session" {
						t.Error("unexpected legacy operation", detail.Method)
						return
					}
					codec.Write(protocol.Frame{Type: "result", ID: detail.ID, DeviceID: "device", Epoch: "epoch", Body: []byte(`{"session":{"id":"s"},"requests":[],"queue":{"items":[]}}`)})
				}
			}()
		}
	}()
	return dir
}
func TestLegacyInspectionBindsSourceOwnerAndKnownState(t *testing.T) {
	for _, state := range []string{"completed", "running", "unknown"} {
		t.Run(state, func(t *testing.T) {
			dir := legacyInspectionFixture(t, state)
			first, err := InspectLegacyWorker(context.Background(), dir, "device")
			if err != nil {
				t.Fatal(err)
			}
			again, err := InspectLegacyWorker(context.Background(), dir, "device")
			if err != nil {
				t.Fatal(err)
			}
			if first.Info.OwnerRef == "" || first.Info.OwnerRef != again.Info.OwnerRef || first.Owner.PID != os.Getpid() {
				t.Fatal("missing stable kernel owner identity", first)
			}
			if first.Info.Busy != (state == "running") || first.Info.Uncertain != (state == "unknown") {
				t.Fatal("lost execution uncertainty", first.Info)
			}
			if _, err = InspectLegacyWorker(context.Background(), dir, "foreign-device"); err == nil {
				t.Fatal("accepted foreign identity")
			}
		})
	}
}

func TestLegacyMigrationOnlySupportsHistoricallyVerifiedProtocol(t *testing.T) {
	dir := legacyInspectionFixture(t, "completed", 4)
	info, e := InspectLegacyWorker(context.Background(), dir, "device")
	if e != nil {
		t.Fatal(e)
	}
	if info.Info.Supported || info.Info.Message != "此旧版协议尚未验证，无法自动迁移" {
		t.Fatal("unverified legacy version offered migration", info.Info)
	}
}
