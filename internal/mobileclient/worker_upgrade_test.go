package mobileclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
)

func TestMobileWorkerUpgradeKeepsWaitingReceiptAcrossDisconnect(t *testing.T) {
	baseline, candidate := os.Getenv("SHIOJI_MOBILE_BOOTSTRAP_ASSET"), os.Getenv("SHIOJI_MOBILE_UPDATE_ASSET")
	testMobileWorkerUpgrade(t, baseline, candidate, false, false)
}

func TestMobileWorkerSchemaUpgradePreservesOwningDevice(t *testing.T) {
	testMobileWorkerUpgrade(t, os.Getenv("SHIOJI_MOBILE_SCHEMA15_ASSET"), os.Getenv("SHIOJI_MOBILE_UPDATE_ASSET"), true, false)
}

func TestMobileWorkerImmediateUpgradeRequiresItsWaitingReceipt(t *testing.T) {
	testMobileWorkerUpgrade(t, os.Getenv("SHIOJI_MOBILE_BOOTSTRAP_ASSET"), os.Getenv("SHIOJI_MOBILE_UPDATE_ASSET"), false, true)
}

func testMobileWorkerUpgrade(t *testing.T, baseline, candidate string, migrate, immediate bool) {
	t.Helper()
	if baseline == "" || candidate == "" {
		t.Skip("set both signed macOS worker package paths")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatal(err)
	}
	build, err := InspectWorkerArtifact(candidate)
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp("/tmp", "shioji-mobile-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopBootstrapFixture(t, home); os.RemoveAll(home) })
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Second)
	defer cancel()
	provider := testutil.NewProvider(t)
	t.Setenv("CODEX_HOME", provider.Home)
	release := sync.OnceFunc(func() { close(provider.Release) })
	defer release()

	host, user := testSigner(t), testSigner(t)
	server := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if metadata.User() != "fixture" || ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(user.PublicKey()) {
			return nil, errors.New("SSH login not authorized")
		}
		return nil, nil
	}}
	server.AddHostKey(host)
	endpoint := Endpoint{Address: bootstrapFixtureSSH(t, ctx, server, home), User: "fixture",
		HostFingerprint: ssh.FingerprintSHA256(host.PublicKey()), WorkerPath: filepath.Join(home, "selected", "shioji-worker"), StateDirectory: filepath.Join(home, "state")}
	installed, info, err := endpoint.Bootstrap(ctx, user, nil, func(context.Context) (io.ReadCloser, string, error) {
		return OpenWorkerArtifact(baseline)
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Build == nil || info.Build.BuildID == build.BuildID || build.BuiltAt <= info.Build.BuiltAt {
		t.Fatalf("test needs an older build: running %+v, candidate %+v", info.Build, build)
	}
	if migrate && !build.MigratesSchema(info.Build.Schema) || !migrate && info.Build.Schema != build.Schema {
		t.Fatalf("incompatible fixture builds: running %+v, candidate %+v", info.Build, build)
	}
	if !migrate {
		installed.TailnetPeerID = "fixture-tailnet-peer"
	}
	route := func(ctx context.Context, peer, address string) (net.Conn, error) {
		if peer != "fixture-tailnet-peer" || address != installed.Address {
			return nil, errors.New("unexpected saved path")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	var businessOffline atomic.Bool
	dial := func(ctx context.Context) (net.Conn, error) {
		if businessOffline.Load() {
			return nil, net.ErrClosed
		}
		return installed.DialRoute(ctx, user, route)
	}
	client, err := protocol.Dial(ctx, dial, installed.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var workspace domain.Workspace
	if err := client.Call(ctx, "workspace.create", protocol.WorkspaceInput{Name: "Update continuity", Folders: []string{home}}, &workspace); err != nil {
		t.Fatal(err)
	}
	var ready domain.Readiness
	for ctx.Err() == nil && len(ready.Models) == 0 {
		if err := client.Call(ctx, "readiness", nil, &ready); err != nil {
			t.Fatal(err)
		}
		if len(ready.Models) == 0 {
			time.Sleep(30 * time.Millisecond)
		}
	}
	if len(ready.Models) == 0 {
		t.Fatal("fixture model not found")
	}
	var accepted domain.Operation
	if err := client.Call(ctx, "session.submit", domain.Submission{OperationID: "original-input", WorkspaceID: workspace.ID,
		Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &accepted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.Started:
	case <-ctx.Done():
		t.Fatal("fixture turn did not start")
	}
	client.Close()

	controllingDirectory := t.TempDir()
	remote := installed.WorkerSSH(user, route)
	var controller *app.Controller
	var model *app.Model
	var uploads atomic.Int32
	start := func() func() {
		c := app.NewAuthenticatedDeviceController(controllingDirectory, installed.DeviceID, installed.RouteID(), []app.AuthenticatedDevicePath{
			{ID: installed.RouteID(), Dial: dial, Worker: remote},
		})
		c.AuthenticatedWorkerBuild = &build
		c.AuthenticatedWorkerArtifact = func(context.Context) (io.ReadCloser, string, error) {
			uploads.Add(1)
			return OpenWorkerArtifact(candidate)
		}
		controller, model = c, app.NewModel()
		run, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); c.Run(run) }()
		return sync.OnceFunc(func() { stop(); <-done })
	}
	wait := func(label string, condition func(*app.WorkerService) bool) {
		t.Helper()
		for model.WorkerService == nil || !condition(model.WorkerService) {
			select {
			case apply := <-controller.Updates:
				apply(model)
			case <-ctx.Done():
				t.Fatalf("%s: connected %v, state %+v, error %s", label, model.Connected, model.WorkerService, model.Error)
			}
		}
	}
	request := func(method string, input any) {
		t.Helper()
		if !controller.Do(app.Job{Method: method, ConnectionEpoch: model.ConnectionEpoch, Input: input,
			Apply: func(_ *app.Model, _ json.RawMessage, err error) {
				if err != nil {
					t.Errorf("%s: %v", method, err)
				}
			}}) {
			t.Fatalf("%s was not queued", method)
		}
	}
	stop := start()
	defer stop()
	wait("available", func(s *app.WorkerService) bool { return model.Connected && s.Phase == app.WorkerAvailable })
	if uploads.Load() != 0 {
		t.Fatal("connection automatically uploaded a worker")
	}
	input := app.AuthenticatedWorkerConfirmation{SourceBuild: info.Build.BuildID, TargetBuild: build.BuildID}
	method := "$worker.update"
	if migrate {
		method = "$worker.update.migrate"
	}
	request(method, input)
	wait("waiting behind the active turn", func(s *app.WorkerService) bool { return s.OperationPhase == "waiting" })
	firstID := model.WorkerService.OperationID
	if firstID == "" || uploads.Load() != 1 {
		t.Fatal("missing first accepted update or duplicate upload")
	}
	t.Log("Verified explicit staging and a durable waiting receipt while a real turn ran.")

	stop()
	businessOffline.Store(true)
	stop = start()
	defer stop()
	wait("detached supervisor receipt", func(s *app.WorkerService) bool {
		return !model.Connected && s.OperationID == firstID && s.OperationPhase == "waiting"
	})
	request("$worker.check", nil)
	request("$worker.update.cancel", firstID)
	wait("cancelled while business connection was down", func(s *app.WorkerService) bool {
		return !model.Connected && s.OperationID == firstID && s.OperationPhase == "failed"
	})
	if uploads.Load() != 1 {
		t.Fatal("status lookup or cancellation uploaded a candidate")
	}
	t.Log("Verified app restart, status and cancellation of the same receipt through enrolled SSH with the business connection down.")

	businessOffline.Store(false)
	request("$worker.check", nil)
	wait("connected again", func(s *app.WorkerService) bool {
		return model.Connected && s.Build != nil && s.Build.BuildID == info.Build.BuildID
	})
	request(method, input)
	wait("new wait after explicit retry", func(s *app.WorkerService) bool { return s.OperationPhase == "waiting" && s.OperationID != firstID })
	if immediate {
		// Cancellation is still scoped to the exact original waiting receipt.
		input.OperationID = model.WorkerService.OperationID
		request("$worker.update.immediate", input)
	} else {
		release()
	}
	wait("new build reopened acceptance", func(s *app.WorkerService) bool {
		return model.Connected && s.Phase == app.WorkerCurrent && s.OperationPhase == "applied" && s.Build != nil && s.Build.BuildID == build.BuildID
	})
	if model.Snapshot.DeviceID != installed.DeviceID || len(model.Snapshot.Workspaces) != 1 ||
		model.Snapshot.Workspaces[0].ID != workspace.ID || len(model.Snapshot.Sessions) != 1 ||
		model.Snapshot.Sessions[0].ID != accepted.SessionID || uploads.Load() != 2 {
		t.Fatal("update did not retain device, workspace and session identity, or re-uploaded without a request")
	}
	current, err := protocol.Dial(ctx, dial, installed.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var history domain.SessionDetail
	if err := current.Call(ctx, "session", protocol.SessionQuery{ID: accepted.SessionID}, &history); err != nil {
		t.Fatal(err)
	}
	var foundPrompt, foundAnswer bool
	for _, item := range history.Items {
		foundPrompt = foundPrompt || item.Kind == "user" && item.Text == "SHIOJI_SLOW"
		foundAnswer = foundAnswer || item.Kind == "assistant" && item.Text == "SHIOJI_SLOW_DONE"
	}
	if !foundPrompt || !immediate && (!foundAnswer || history.Session.Status != "completed") {
		t.Fatalf("lost prior messages across upgrade: prompt %v, reply %v, status %s", foundPrompt, foundAnswer, history.Session.Status)
	}
	if immediate {
		t.Log("Verified the confirmed immediate upgrade switched while the model response was still held, without replacing the device, workspace or session.")
	} else {
		t.Log("Verified the worker switched after the turn completed and reopened the same device, workspace and session.")
	}
}
