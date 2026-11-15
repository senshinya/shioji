package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
)

type unavailableResourceNetwork struct{ storageUnavailableNetwork }

func (unavailableResourceNetwork) Run(ctx context.Context, _ func(tailnet.Snapshot)) { <-ctx.Done() }
func (unavailableResourceNetwork) Do(tailnet.Action) bool                            { return false }

// A saved Tailnet route must not gate a view once the device controller owns
// routing. With no healthy sample, that controller asks the view to wait; the
// old UI preflight instead fails on the unrelated saved Tailnet configuration.
func TestResourceViewsUseManagedRoutesWithoutTailnetPreflight(t *testing.T) {
	connection := app.DeviceConnection{ID: "remote", DeviceID: "owner", SSH: transport.SSH{
		Target: "user@100.64.0.9", TailnetPeer: "peer", Worker: "/worker",
	}}
	m := app.NewModel()
	m.LocalDeviceID = "local"
	m.Connections = []app.DeviceConnection{connection}
	owner := app.NewDeviceController(t.TempDir(), "local", m.Connections, "", nil)
	owner.SetWorkerAutoUpdate(false)
	owner.AttachTailnet(unavailableResourceNetwork{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); owner.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for m.DeviceStates["remote"] == nil {
		select {
		case update := <-owner.Updates:
			update(m)
		case <-ctx.Done():
			t.Fatal("device stream routing was not attached")
		}
	}
	state := m.DeviceStates["remote"]
	state.Connected, state.ConnectionEpoch = true, "epoch"
	session := domain.Session{ID: "s", WorkspaceID: "w"}
	m.Details["s"] = domain.SessionDetail{Session: session}
	state.Snapshot = domain.Snapshot{DeviceID: "owner", Workspaces: []domain.Workspace{{ID: "w", DeviceID: "owner"}}, Sessions: []domain.Session{session}}
	m.WorkspaceID, m.SessionID = "w", "s"
	m.ActiveConnection = "remote"
	m.RefreshDevices()
	// The view's task queue is independent of probe/status updates so this
	// fixture holds the exact handoff state while exercising real view methods.
	controller := app.NewController("")
	controller.AttachTailnet(storageUnavailableNetwork{})
	v := New(m, controller, filepath.Join(t.TempDir(), "state.json"))
	defer v.Saver.Close()
	defer v.cancelFileLink()
	for _, kind := range []string{"storage", "file", "web"} {
		t.Run(kind, func(t *testing.T) {
			var storage *deviceStorageState
			if kind == "storage" {
				storage = &deviceStorageState{epoch: "epoch"}
				v.deviceStorageStates = map[string]*deviceStorageState{"remote": storage}
				v.readDeviceStorage("remote", storage, false)
			} else {
				path := "/device/report.txt"
				if kind == "web" {
					path = "http://owning-device.invalid/report.txt"
				}
				v.startLinkResource(session, path, n.Services{}, fileLinkPreview)
				if !v.fileLink.loading {
					t.Fatalf("resource fixture was not connected: %s", v.fileLink.error)
				}
			}
			select {
			case update := <-controller.Updates:
				update(m)
			case <-ctx.Done():
				t.Fatal("resource view did not complete")
			}
			err := ""
			if storage != nil {
				err = storage.error
			} else {
				err = v.fileLink.error
			}
			want := "正在检查连接路径"
			if kind == "web" {
				want = `Get "http://owning-device.invalid/report.txt": 正在检查连接路径`
			}
			if err != want {
				t.Fatalf("view bypassed the owning device's live route manager: %q", err)
			}
		})
	}
}
