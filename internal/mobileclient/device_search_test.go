package mobileclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestDeviceSearchUsesVerifiedAlternateWhenTheFirstStreamCloses(t *testing.T) {
	client := testSigner(t)
	first, firstDone := deviceSearchPathFixture(t, client, true)
	second, secondDone := deviceSearchPathFixture(t, client, false)
	second.TailnetPeerID = "private-peer"
	device := SavedDevice{Endpoint: first, Routes: []Endpoint{first, second}}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	route := func(ctx context.Context, peer, address string) (net.Conn, error) {
		if peer != second.TailnetPeerID || address != second.Address {
			return nil, fmt.Errorf("unexpected private route %s", peer)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	hits, err := SearchDevice(ctx, device, first, client, route, "cursor")
	if err != nil || len(hits) != 1 || hits[0].DeviceAddress != second.Address || hits[0].DeviceID != first.DeviceID {
		t.Fatalf("search did not use the remaining verified path: %+v, %v", hits, err)
	}
	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("search left a worker stream open")
		}
	}
}

func deviceSearchPathFixture(t *testing.T, client ssh.Signer, closeStream bool) (Endpoint, <-chan error) {
	t.Helper()
	host := testSigner(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	endpoint := Endpoint{Address: listener.Addr().String(), DeviceID: "worker", User: "owner", WorkerPath: DefaultWorkerPath,
		HostFingerprint: ssh.FingerprintSHA256(host.PublicKey())}
	config := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() != endpoint.User || ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(client.PublicKey()) {
			return nil, fmt.Errorf("untrusted client")
		}
		return nil, nil
	}}
	config.AddHostKey(host)
	done := make(chan error, 1)
	go func() {
		done <- serveBridgeFixture(listener, config, endpoint.command(), func(channel ssh.Channel) error {
			codec := protocol.NewCodec(channel, channel)
			var hello protocol.Frame
			if err := codec.Read(&hello); err != nil {
				return err
			}
			const epoch = "epoch"
			if err := codec.Write(protocol.Frame{Type: "welcome", Major: protocol.Major, Minor: protocol.Minor,
				Nonce: hello.Nonce, DeviceID: "worker", Epoch: epoch}); err != nil {
				return err
			}
			if closeStream {
				var search protocol.Frame
				if err := codec.Read(&search); err != nil {
					return err
				}
				if search.Method != "session.search" {
					return fmt.Errorf("unexpected call %s", search.Method)
				}
				return channel.Close()
			}
			results := []domain.SearchResult{{SessionID: "session", WorkspaceID: "project", ItemID: "result"}}
			if err := writeSearchFixture(codec, epoch, "session.search", results); err != nil {
				return err
			}
			return writeSearchFixture(codec, epoch, "snapshot", domain.Snapshot{DeviceID: "worker",
				Workspaces: []domain.Workspace{{ID: "project", DeviceID: "worker"}},
				Sessions:   []domain.Session{{ID: "session", WorkspaceID: "project"}},
			})
		})
	}()
	return endpoint, done
}

func TestDeviceSearchOnlyReturnsResultsOwnedByItsWorker(t *testing.T) {
	result := func(session, workspace string) domain.SearchResult {
		return domain.SearchResult{SessionID: session, WorkspaceID: workspace, ItemID: "match"}
	}
	catalog := domain.Snapshot{
		DeviceID: "worker",
		Workspaces: []domain.Workspace{
			{ID: "primary", DeviceID: "worker", Name: "Project", Archived: true},
			{ID: "other", DeviceID: "other-worker", Name: "Other project"},
		},
		Sessions: []domain.Session{
			{ID: "visible", WorkspaceID: "primary", Title: "Verified title", ThreadID: "thread"},
			{ID: "foreign", WorkspaceID: "other"},
			{ID: "wrong-execution-context", WorkspaceID: "primary", Context: domain.LaunchContext{DeviceID: "other-worker"}},
		},
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	server := make(chan error, 1)
	go func() {
		codec, epoch, err := protocol.Welcome(right, "worker")
		if err == nil {
			err = writeSearchFixture(codec, epoch, "session.search", []domain.SearchResult{
				result("visible", "primary"), result("foreign", "other"), result("wrong-execution-context", "primary"),
				result("gone", "primary"), result("visible", "other"),
			})
		}
		if err == nil {
			err = writeSearchFixture(codec, epoch, "snapshot", catalog)
		}
		server <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := protocol.Connect(ctx, left, "worker")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := searchConnectedDevice(ctx, client, "127.0.0.1:2222", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("returned extra or missing results: %+v", hits)
	}
	hit := hits[0]
	if hit.SessionID != "visible" || hit.ItemID != "match" || hit.DeviceID != "worker" ||
		hit.ThreadID != "thread" || hit.WorkspaceName != "Project" || hit.Title != "Verified title" || !hit.Archived {
		t.Fatalf("wrong search location: %+v", hit)
	}
}

func writeSearchFixture(codec *protocol.Codec, epoch, method string, value any) error {
	var req protocol.Frame
	if err := codec.Read(&req); err != nil {
		return err
	}
	if req.Method != method {
		return fmt.Errorf("method = %s, want %s", req.Method, method)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return codec.Write(protocol.Frame{Type: "result", ID: req.ID, DeviceID: "worker", Epoch: epoch, Body: body})
}
