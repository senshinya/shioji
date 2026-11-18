package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestDeviceSearchReportsPartialAvailability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := NewDeviceController("/unused", "A", []DeviceConnection{{ID: "air", DeviceID: "B"}}, "", nil)
	m := NewModel()
	m.DeviceStates["local"] = &DeviceState{Snapshot: domain.Snapshot{Workspaces: []domain.Workspace{{ID: "wA", DeviceID: "A"}}}}
	var progress SearchProgress
	c.searchDevices(ctx, Job{Input: protocol.SearchQuery{Query: "term"}, SearchProgress: func(_ *Model, next SearchProgress) { progress = next }})
	reply := func(key string, body json.RawMessage, err error) {
		t.Helper()
		source := c.fleet.sources[key]
		select {
		case job := <-source.controller.Jobs:
			job.Apply(m, body, err)
		case <-ctx.Done():
			t.Fatal("missing delegated search")
		}
		select {
		case update := <-c.Updates:
			update(m)
		case <-ctx.Done():
			t.Fatal("missing aggregate update")
		}
	}
	reply("local", json.RawMessage(`[{"sessionId":"s","workspaceId":"wA","title":"Local"}]`), nil)
	if progress.Pending != 1 || progress.Failed != 0 || len(progress.Results) != 1 {
		t.Fatalf("first result waits on another device: %+v", progress)
	}
	reply("air", nil, errors.New("offline"))
	if progress.Pending != 0 || progress.Failed != 1 || progress.Error != nil || len(progress.Results) != 1 {
		t.Fatalf("partial failure lost the successful device: %+v", progress)
	}
}

func TestDeviceControllerRoutesCommandsAndKeepsBothSubscriptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewDeviceController("/unused", "A", []DeviceConnection{{ID: "air", Name: "Air", DeviceID: "B", SSH: transport.SSH{Target: "air", Worker: "/worker"}}}, "", nil)
	for key, source := range c.fleet.sources {
		device := map[string]string{"local": "A", "air": "B"}[key]
		source.controller.Dial = func(ctx context.Context) (net.Conn, error) {
			a, b := net.Pipe()
			go func() {
				defer b.Close()
				stop := context.AfterFunc(ctx, func() { b.Close() })
				defer stop()
				codec, _, err := protocol.Welcome(b, device)
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
						result(codec, f, domain.Snapshot{DeviceID: device, Generation: device, Workspaces: []domain.Workspace{{ID: "w" + device, DeviceID: device}}})
					case "readiness":
						result(codec, f, domain.Readiness{State: "ready"})
					case "events.watch":
						<-ctx.Done()
						return
					case "session.search":
						result(codec, f, []domain.SearchResult{{SessionID: "s" + device, WorkspaceID: "w" + device, Title: device}})
					case "probe":
						result(codec, f, device)
					}
				}
			}()
			return a, nil
		}
	}
	go c.Run(ctx)
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	deadline := time.After(3 * time.Second)
	for len(m.DeviceStates) != 2 || !m.DeviceStates["local"].Connected || !m.DeviceStates["air"].Connected {
		select {
		case u := <-c.Updates:
			u(m)
		case <-deadline:
			t.Fatal("did not connect both devices", m.DeviceStates)
		}
	}
	c.Do(Job{Method: "$device.cancel"})
	received := ""
	c.Do(Job{ConnectionID: "air", ConnectionEpoch: m.DeviceStates["air"].ConnectionEpoch, Method: "probe", Apply: func(_ *Model, b json.RawMessage, err error) {
		if err != nil {
			t.Error(err)
		}
		received = string(b)
	}})
	for received == "" {
		select {
		case u := <-c.Updates:
			u(m)
		case <-deadline:
			t.Fatal("remote command blocked by watch")
		}
	}
	if received != `"B"` {
		t.Fatal("command went to wrong device", received)
	}
	var results []domain.SearchResult
	c.Do(Job{ConnectionID: "air", Method: "session.search", Input: protocol.SearchQuery{Query: "term"}, Apply: func(_ *Model, b json.RawMessage, err error) {
		if err != nil {
			t.Error(err)
		}
		json.Unmarshal(b, &results)
	}})
	for len(results) < 2 {
		select {
		case u := <-c.Updates:
			u(m)
		case <-deadline:
			t.Fatal("global search stalled")
		}
	}
	if len(results) != 2 {
		t.Fatal("global search did not include both devices", results)
	}
	if len(m.Snapshot.Workspaces) != 2 {
		t.Fatal("device catalogs not aggregated", m.Snapshot)
	}
}
