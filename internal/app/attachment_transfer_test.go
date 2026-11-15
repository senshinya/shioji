package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/portforward"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

// The transcript connection already resolves a private Tailnet bridge. Media
// and storage streams must use that same device routing boundary, rather than
// dialing the saved SSH settings (whose transient ProxySocket is never saved).
func TestDeviceResourceStreamsUseLiveRoutes(t *testing.T) {
	data := []byte("immutable attachment from owning device")
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/download.txt" {
			t.Error("wrong owning-device URL", r.URL)
		}
		w.Write(data)
	}))
	defer httpServer.Close()
	a, err := attachments.Describe("input.txt", data)
	if err != nil {
		t.Fatal(err)
	}
	connection := DeviceConnection{ID: "remote", DeviceID: "owner", SSH: transport.SSH{
		Target: "user@100.64.0.9", TailnetPeer: "peer", Worker: "/worker",
	}}.Normalized()
	c := NewDeviceController("/unused", "local-device", []DeviceConnection{connection}, "", nil)
	source := c.fleet.sources["remote"]
	// Keep business subscription setup out of this resource-transfer test.
	source.controller.Dial = func(context.Context) (net.Conn, error) { return nil, errDevicePathsWaiting }
	source.controller.paths.open = func(ctx context.Context, ssh transport.SSH) (net.Conn, error) {
		if (ssh.Target != "user@100.64.0.9" || ssh.TailnetPeer != "peer") && (ssh.Target != "user@100.64.0.10" || ssh.TailnetPeer != "next-peer") {
			t.Error("resource used a different device route", ssh.Target)
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			stop := context.AfterFunc(ctx, func() { server.Close() })
			defer stop()
			codec, _, err := protocol.Welcome(server, "owner")
			if err != nil {
				return
			}
			for {
				var frame protocol.Frame
				if codec.Read(&frame) != nil {
					return
				}
				switch frame.Method {
				case "device.ping":
					result(codec, frame, map[string]string{"deviceId": "owner"})
				case "attachment.get":
					var query protocol.AttachmentGet
					if json.Unmarshal(frame.Body, &query) != nil || query.WorkspaceID != "workspace" || query.ID != a.ID || query.Offset != 0 {
						t.Error("wrong attachment query")
						return
					}
					result(codec, frame, attachments.Receipt{Attachment: a, Data: data, Offset: int64(len(data)), Complete: true})
				case "storage.usage":
					bytes := int64(42)
					if ssh.Target == "user@100.64.0.10" {
						bytes = 84
					}
					result(codec, frame, domain.StorageReport{Usage: domain.StorageUsage{PreviewBytes: bytes}})
				case protocol.TCPStreamMethod:
					var query portforward.Target
					if json.Unmarshal(frame.Body, &query) != nil || query.Host != "owning-device.invalid" || query.Port != 5173 {
						t.Error("download bypassed the owning device URL", query)
						return
					}
					peer, err := (&net.Dialer{}).DialContext(ctx, "tcp", httpServer.Listener.Addr().String())
					if err != nil {
						t.Error(err)
						return
					}
					defer peer.Close()
					result(codec, frame, nil)
					portforward.Relay(ctx, server, peer)
					return
				default:
					t.Error("unexpected resource method", frame.Method)
					return
				}
			}
		}()
		return client, nil
	}
	source.controller.paths.record(connection.ActiveRouteID, time.Millisecond, time.Now(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() { cancel(); c.deviceWG.Wait() }()
	c.startDevice(ctx, "remote", source)
	m := NewModel()
	m.LocalDeviceID = "local-device"
	m.Connections = []DeviceConnection{connection}
	select {
	case update := <-c.Updates:
		update(m)
	case <-ctx.Done():
		t.Fatal("device routing was not attached")
	}
	m.DeviceStates["remote"].Connected = true
	m.DeviceStates["remote"].Snapshot = domain.Snapshot{DeviceID: "owner", Workspaces: []domain.Workspace{{ID: "workspace", DeviceID: "owner"}}}
	m.RefreshDevices()
	t.Run("attachment", func(t *testing.T) {
		target, err := m.AttachmentTarget("/unused", "workspace")
		if err != nil {
			t.Fatal(err)
		}
		path, err := target.Download(ctx, attachments.New(t.TempDir()), a)
		if err != nil {
			t.Fatal("connected transcript could not download attachment:", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(data) {
			t.Fatal("attachment content lost", err)
		}
	})
	t.Run("web download", func(t *testing.T) {
		target, err := m.AttachmentTarget("/unused", "workspace")
		if err != nil {
			t.Fatal(err)
		}
		file, err := target.FetchURL(ctx, t.TempDir(), "http://owning-device.invalid:5173/download.txt", nil)
		if err != nil {
			t.Fatal("connected device could not download URL:", err)
		}
		got, err := os.ReadFile(file.Path)
		if err != nil || string(got) != string(data) {
			t.Fatal("owning-device web download lost content", err)
		}
	})
	t.Run("storage", func(t *testing.T) {
		target, err := m.StorageTarget("/unused", "remote")
		if err != nil {
			t.Fatal(err)
		}
		report, err := target.Read(ctx, false)
		if err != nil || report.Usage.PreviewBytes != 42 {
			t.Fatal("storage did not use live device routing", report, err)
		}
	})
	t.Run("authenticated owner", func(t *testing.T) {
		target, err := m.AttachmentTarget("/unused", "workspace")
		if err != nil {
			t.Fatal(err)
		}
		target.DeviceID = "different-owner"
		if client, err := target.connect(ctx); err == nil {
			client.Close()
			t.Fatal("resource stream accepted a different worker identity")
		}
	})
	t.Run("local owner", func(t *testing.T) {
		local, _ := filePeer(t, "owner", nil, false)
		m.LocalDeviceID = "owner"
		defer func() { m.LocalDeviceID = "local-device" }()
		target, err := m.AttachmentTarget(local.Directory, "workspace")
		if err != nil {
			t.Fatal(err)
		}
		client, err := target.connect(ctx)
		if err != nil {
			t.Fatal("local owner incorrectly depended on remote networking", err)
		}
		client.Close()
	})
	t.Run("route changed after target capture", func(t *testing.T) {
		target, err := m.StorageTarget("/unused", "remote")
		if err != nil {
			t.Fatal(err)
		}
		next := deviceRoute(transport.SSH{Target: "user@100.64.0.10", TailnetPeer: "next-peer", Worker: "/worker"})
		connection.Routes = append(connection.Routes, next)
		source.controller.paths.configure(connection)
		source.controller.paths.record(next.ID, time.Millisecond, time.Now(), nil)
		source.controller.paths.activate(next.ID, time.Now())
		report, err := target.Read(ctx, false)
		if err != nil || report.Usage.PreviewBytes != 84 {
			t.Fatal("resource stream retained the old route", report, err)
		}
	})
	t.Run("unavailable route", func(t *testing.T) {
		// Stop probes before withdrawing their last healthy measurement.
		cancel()
		c.deviceWG.Wait()
		for _, route := range connection.Routes {
			source.controller.paths.record(route.ID, 0, time.Now(), net.ErrClosed)
		}
		target, err := m.AttachmentTarget("/unused", "workspace")
		if err != nil {
			t.Fatal(err)
		}
		_, err = target.connect(context.Background())
		if !errors.Is(err, errDevicePathsWaiting) {
			t.Fatal("unavailable route bypassed the device connection manager", err)
		}
		_, err = target.FetchURL(context.Background(), t.TempDir(), "http://owning-device.invalid:5173/download.txt", nil)
		if !errors.Is(err, errDevicePathsWaiting) {
			t.Fatal("web download bypassed unavailable device routing", err)
		}
	})
}
