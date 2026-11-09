package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type signInMemory struct {
	configurableMemory
	starts   *atomic.Int32
	finish   chan bool
	failure  error
	canceled *atomic.Bool
}

func (a signInMemory) WithExecutable(p string) (harness.Adapter, error) {
	a.executable = p
	return a, nil
}
func (a signInMemory) Installation() domain.RuntimeInstallation {
	return domain.RuntimeInstallation{SignInMethods: []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode}, Configurable: true}
}
func (a signInMemory) BeginSignIn(context.Context, domain.SignInMethod) (harness.SignIn, domain.SignInChallenge, error) {
	a.starts.Add(1)
	return &memorySignIn{finish: a.finish, canceled: a.canceled, failure: a.failure}, domain.SignInChallenge{URL: "https://example.test/authorize", Code: "PRIVATE-ONE-TIME"}, nil
}

type memorySignIn struct {
	finish   chan bool
	failure  error
	canceled *atomic.Bool
}

func (l *memorySignIn) Wait(ctx context.Context) (domain.Readiness, error) {
	select {
	case <-l.finish:
		if l.failure != nil {
			return domain.Readiness{}, l.failure
		}
		return domain.Readiness{State: domain.RuntimeReady, Account: &domain.RuntimeAccount{State: "authenticated", Label: "Profile", Identity: "dev@example.test"}}, nil
	case <-ctx.Done():
		return domain.Readiness{}, ctx.Err()
	}
}
func (l *memorySignIn) Cancel(context.Context) error { l.canceled.Store(true); return nil }
func signInWorker(t *testing.T) (*Server, *signInMemory) {
	t.Helper()
	a := &signInMemory{configurableMemory: configurableMemory{memoryHarness: memoryHarness{id: "custom", hold: true}}, starts: &atomic.Int32{}, finish: make(chan bool), canceled: &atomic.Bool{}}
	s, _ := memoryWorker(t, a)
	return s, a
}
func awaitSignIn(t *testing.T, s *Server, id string, phase domain.SignInPhase) domain.SignInStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, err := s.signInCommand("runtime.signin.status", protocol.SignInInput{ID: id, HarnessID: "custom"})
		if err != nil {
			t.Fatal(err)
		}
		if v.Phase == phase {
			return v
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("sign-in phase not reached", phase)
	return domain.SignInStatus{}
}
func TestWorkerSignInIsIdempotentTransientAndIndependentOfConnection(t *testing.T) {
	s, a := signInWorker(t)
	in := protocol.SignInInput{ID: "auth-1", HarnessID: "custom", Method: domain.SignInDeviceCode}
	if _, err := s.signInCommand("runtime.signin.start", in); err != nil {
		t.Fatal(err)
	}
	status := awaitSignIn(t, s, in.ID, domain.SignInWaiting)
	if status.Challenge.Code != "PRIVATE-ONE-TIME" {
		t.Fatal("challenge lost")
	}
	if _, err := s.signInCommand("runtime.signin.start", in); err != nil {
		t.Fatal(err)
	}
	if a.starts.Load() != 1 {
		t.Fatal("start replayed", a.starts.Load())
	}
	unknown, err := s.signInCommand("runtime.signin.status", protocol.SignInInput{ID: "unknown", HarnessID: "custom"})
	if err != nil || unknown.Phase != domain.SignInExpired || a.starts.Load() != 1 {
		t.Fatal("unknown query started authorization", unknown, err)
	}
	close(a.finish)
	done := awaitSignIn(t, s, in.ID, domain.SignInCompleted)
	if done.Challenge.URL != "" || done.Challenge.Code != "" || done.Profile == nil || done.Profile.Account.Identity != "dev@example.test" {
		t.Fatal("completion did not replace sensitive challenge with verified metadata", done)
	}
	filepath.WalkDir(s.config.Directory, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			if bytes.Contains(b, []byte("PRIVATE-ONE-TIME")) {
				t.Fatal("challenge persisted", p)
			}
		}
		return nil
	})

}
func TestWorkerCancelDoesNotStopConversationAndRejectsWrongHarness(t *testing.T) {
	s, a := signInWorker(t)
	_, err := s.signInCommand("runtime.signin.start", protocol.SignInInput{ID: "auth-cancel", HarnessID: "custom", Method: domain.SignInBrowser})
	if err != nil {
		t.Fatal(err)
	}
	awaitSignIn(t, s, "auth-cancel", domain.SignInWaiting)
	if _, err = s.signInCommand("runtime.signin.cancel", protocol.SignInInput{ID: "auth-cancel", HarnessID: "other"}); err == nil {
		t.Fatal("mismatched harness accepted")
	}
	result, err := s.signInCommand("runtime.signin.cancel", protocol.SignInInput{ID: "auth-cancel", HarnessID: "custom"})
	if err != nil || result.Phase != domain.SignInCanceled || !a.canceled.Load() {
		t.Fatal(result, err)
	}
	if !s.catalogRuntime("custom").ExecutionReady() {
		t.Fatal("cancel stopped unrelated runtime")
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "PRIVATE-ONE-TIME") {
		t.Fatal("terminal challenge retained")
	}
}

func TestWorkerSignInBlocksManagedServiceStopAndUpdate(t *testing.T) {
	s, _ := signInWorker(t)
	s.build.Managed = true
	stopped := false
	s.config.DisableBackground = func(context.Context) (domain.BackgroundService, error) {
		stopped = true
		return domain.BackgroundService{State: "notRegistered"}, nil
	}
	s.signInCommand("runtime.signin.start", protocol.SignInInput{ID: "active-auth", HarnessID: "custom", Method: domain.SignInBrowser})
	awaitSignIn(t, s, "active-auth", domain.SignInWaiting)
	result, err := s.disableBackground()
	if err != nil || stopped || result.Message == "" {
		t.Fatal("service stop interrupted login", result, err)
	}
	s.config.ReplaceWorker = func(domain.WorkerReplacement) error { t.Fatal("update interrupted login"); return nil }
	s.build.BuildID = "old"
	update, err := s.prepareWorkerUpdate(domain.WorkerReplacement{ExpectedBuildID: "old", BuildID: "new"})
	if err != nil || update.Ready || update.Reason == "" {
		t.Fatal("update admitted during login", update, err)
	}
}

func TestWorkerSignInSurvivesRealClientDisconnectAndReconnect(t *testing.T) {
	s, a := signInWorker(t)
	go s.Serve()
	dial := func() *protocol.Client {
		t.Helper()
		conn, err := net.Dial("unix", filepath.Join(s.config.Directory, "worker.sock"))
		if err != nil {
			t.Fatal(err)
		}
		client, err := protocol.Connect(context.Background(), conn, s.store.DeviceID())
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	client := dial()
	in := protocol.SignInInput{ID: "across-connections", HarnessID: "custom", Method: domain.SignInDeviceCode}
	var result domain.SignInStatus
	if err := client.Call(context.Background(), "runtime.signin.start", in, &result); err != nil {
		t.Fatal(err)
	}
	client.Close()
	awaitSignIn(t, s, in.ID, domain.SignInWaiting)
	other := dial()
	defer other.Close()
	if err := other.Call(context.Background(), "runtime.signin.status", in, &result); err != nil || result.Phase != domain.SignInWaiting || a.starts.Load() != 1 {
		t.Fatal("connection loss canceled or replayed authorization", result, err)
	}
	if err := other.Call(context.Background(), "runtime.signin.cancel", in, &result); err != nil || result.Phase != domain.SignInCanceled {
		t.Fatal("reconnected cancellation failed", result, err)
	}
}

type apiSetupMemory struct{ signInMemory }

func (a apiSetupMemory) ConfigureAPI(ctx context.Context, input domain.APIConfiguration) (domain.Readiness, error) {
	a.starts.Add(1)
	return domain.Readiness{State: domain.RuntimeReady, Account: &domain.RuntimeAccount{Mode: "api", State: "authenticated", Label: "Custom API", Endpoint: input.Endpoint, Model: input.Model, Credential: "configured"}}, nil
}
func TestWorkerAPISetupIsTransientAndNotReplayedByStatus(t *testing.T) {
	a := apiSetupMemory{signInMemory: signInMemory{configurableMemory: configurableMemory{memoryHarness: memoryHarness{id: "custom", hold: true}}, starts: &atomic.Int32{}}}
	s, _ := memoryWorker(t, a)
	input := protocol.SignInInput{ID: "api-setup", HarnessID: "custom", Method: domain.SignInAPI, API: &domain.APIConfiguration{Endpoint: "https://api.example/v1", Model: "custom-model", Credential: "never-persist-this-key"}}
	if _, err := s.signInCommand("runtime.signin.start", input); err != nil {
		t.Fatal(err)
	}
	status := awaitSignIn(t, s, input.ID, domain.SignInCompleted)
	if status.Profile == nil || status.Profile.Account.Endpoint != "https://api.example/v1" {
		t.Fatal("public setup result lost", status)
	}
	s.signInCommand("runtime.signin.start", input)
	s.signInCommand("runtime.signin.status", protocol.SignInInput{ID: "unknown", HarnessID: "custom"})
	if a.starts.Load() != 1 {
		t.Fatal("setup replayed")
	}
	body, _ := json.Marshal(status)
	if bytes.Contains(body, []byte("never-persist-this-key")) {
		t.Fatal("secret in status")
	}
	filepath.WalkDir(s.config.Directory, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			data, _ := os.ReadFile(path)
			if bytes.Contains(data, []byte("never-persist-this-key")) {
				t.Error("secret persisted", path)
			}
		}
		return nil
	})
}

func TestCompletedAuthorizationRetainsAccountWhenModelsFail(t *testing.T) {
	s, a := signInWorker(t)
	a.failure = &harness.SetupError{Issue: domain.RuntimeUnavailableIssue, Message: "模型读取失败，请重试", Account: &domain.RuntimeAccount{Mode: "account", State: "authenticated", Label: "Profile", Subscription: "Pro"}}
	in := protocol.SignInInput{ID: "auth-model-failure", HarnessID: "custom", Method: domain.SignInDeviceCode}
	if _, err := s.signInCommand("runtime.signin.start", in); err != nil {
		t.Fatal(err)
	}
	awaitSignIn(t, s, in.ID, domain.SignInWaiting)
	close(a.finish)
	done := awaitSignIn(t, s, in.ID, domain.SignInCompleted)
	if done.Profile == nil || done.Profile.Account == nil || done.Profile.Account.Subscription != "Pro" || done.Profile.State != domain.RuntimeUnavailable || len(done.Profile.Models) != 0 {
		t.Fatalf("authorization/model states conflated: %+v", done)
	}
}
