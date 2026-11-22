package mobileclient

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/worker"
)

type offlineAgent struct{}

func (offlineAgent) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "offline", Name: "Offline fixture"}
}
func (offlineAgent) Start(context.Context, harness.StartOptions) (harness.Runtime, error) {
	return nil, errors.New("offline fixture")
}

func TestControllerLoadsOwningWorkerSnapshotViaSSH(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "mobile-worker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	service, err := worker.New(ctx, worker.Config{Directory: dir, Harnesses: []harness.Adapter{offlineAgent{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	go service.Serve()
	workerDial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
	}
	local, err := protocol.Dial(ctx, workerDial, "")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var workspace domain.Workspace
	if err := local.Call(ctx, "workspace.create", protocol.WorkspaceInput{Name: "Device-owned workspace", Folders: []string{t.TempDir()}}, &workspace); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	host, client := testSigner(t), testSigner(t)
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if ssh.FingerprintSHA256(client.PublicKey()) != ssh.FingerprintSHA256(key) {
			return nil, errors.New("unauthorized")
		}
		return nil, nil
	}}
	config.AddHostKey(host)
	endpoint := Endpoint{Address: listener.Addr().String(), User: "fixture", WorkerPath: DefaultWorkerPath, HostFingerprint: ssh.FingerprintSHA256(host.PublicKey())}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- serveBridgeFixture(listener, config, endpoint.command(), func(channel ssh.Channel) error {
			conn, err := workerDial(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()
			go func() {
				_, _ = io.Copy(conn, channel)
				conn.Close()
			}()
			_, err = io.Copy(channel, conn)
			return err
		})
	}()

	model := app.NewModel()
	controller := app.NewController(t.TempDir())
	controller.ExpectedDeviceID = workspace.DeviceID
	controller.Dial = func(ctx context.Context) (net.Conn, error) { return endpoint.Dial(ctx, client) }
	controllerDone := make(chan struct{})
	go func() {
		defer close(controllerDone)
		controller.Run(ctx)
	}()
	defer func() {
		cancel()
		<-controllerDone
	}()

	for !model.Connected || model.ConnectionEpoch == "" {
		select {
		case update := <-controller.Updates:
			update(model)
		case err := <-serveErrors:
			t.Fatalf("bridge closed before snapshot: %v", err)
		case <-ctx.Done():
			t.Fatalf("controller did not open the Worker snapshot: %v, %s", ctx.Err(), model.ConnectionError)
		}
	}
	if model.Snapshot.DeviceID != workspace.DeviceID || len(model.Snapshot.Workspaces) != 1 ||
		model.Snapshot.Workspaces[0].ID != workspace.ID {
		t.Fatalf("unexpected snapshot %+v", model.Snapshot)
	}
}
