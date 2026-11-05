package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestRealAppServerNameNotificationAndResume(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("pinned Codex not installed")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("naming synchronization must not make a model request")
		w.WriteHeader(500)
	}))
	defer provider.Close()
	home := t.TempDir()
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n", provider.URL)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := make(chan domain.Event, 100)
	opts := Options{Executable: binary, Home: home, OnEvent: func(e domain.Event) error { events <- e; return nil }}
	r, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	s := domain.Session{ID: "naming", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err = r.call(ctx, "thread/name/set", map[string]string{"threadId": s.ThreadID, "name": "任务侧栏"}, nil); err != nil {
		r.Close()
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Type != "session.title" || e.Text != "任务侧栏" || e.SessionID != s.ID {
			t.Fatal("name update lost", e)
		}
	case <-ctx.Done():
		t.Fatal("name notification missing")
	}
	r.Close()
	events = make(chan domain.Event, 100)
	r, err = Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.Resume(ctx, s); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Type != "session.title" || e.Text != "任务侧栏" || e.SessionID != s.ID {
			t.Fatal("resumed name lost", e)
		}
	case <-ctx.Done():
		t.Fatal("resume did not restore name")
	}
}
