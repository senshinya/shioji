package codex

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/testutil"
)

func TestConversationDiscoveryReadsHistoryWithoutResumingOrSending(t *testing.T) {
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	completed := make(chan string, 4)
	r, err := Start(ctx, Options{Home: p.Home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && e.Turn.Status == "completed" {
			completed <- e.Turn.ID
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s := domain.Session{ID: "source", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}}
	s.Context.CWD, err = filepath.EvalSymlinks(s.Context.CWD)
	if err != nil {
		t.Fatal(err)
	}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Submit(ctx, s, "source-prompt", "Explain red-black tree rotations"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A CLI history source is persisted after its owning process exits. Unlike a
	// prepared fork, an ordinary loaded thread has a nonzero unload grace period.
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := Start(ctx, Options{Home: p.Home})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	page, err := reader.Conversations(ctx, []string{s.Context.CWD}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SourceRef != s.ThreadID {
		t.Fatalf("discovery lost its directory scope: %+v", page)
	}
	other, err := reader.Conversations(ctx, []string{t.TempDir()}, "")
	if err != nil || len(other.Items) != 0 {
		t.Fatalf("foreign directory history leaked: %+v %v", other, err)
	}
	preview, err := reader.ReadConversation(ctx, s.Context, s.ThreadID)
	if err != nil {
		t.Fatalf("preview: %+v context: %+v source: %q: %v", preview.Candidate, s.Context, s.ThreadID, err)
	}
	if len(preview.Items) != 2 || preview.Items[0].Kind != "user" || preview.Items[0].Text != "Explain red-black tree rotations" || preview.Items[1].Kind != "assistant" || !strings.Contains(preview.Items[1].Text, "SHIOJI_FAST_DONE") {
		t.Fatalf("wrong imported transcript: %+v", preview.Items)
	}
	if preview.ResumeRef == "" || preview.Truncated {
		t.Fatalf("complete stored history cannot continue: %+v", preview)
	}
	if len(p.Inputs()) != 1 || reader.Observes(s) {
		t.Fatal("preview resumed or executed the source")
	}
	if _, err = reader.ReadConversation(ctx, domain.LaunchContext{CWD: t.TempDir()}, s.ThreadID); err == nil {
		t.Fatal("read accepted foreign source directory")
	}

	s.Origin = &domain.ConversationOrigin{SourceRef: s.ThreadID, ResumeRef: preview.ResumeRef}
	fork, err := reader.PrepareConversation(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if fork == "" || fork == s.ThreadID || len(p.Inputs()) != 1 {
		t.Fatal("continuation took over or executed external source", fork)
	}
	if reader.Observes(s) {
		t.Fatal("preparation bound the external session")
	}
	s.Origin.ResumeRef = "invalid"
	if _, err = reader.PrepareConversation(ctx, s); err == nil {
		t.Fatal("invalid prefix silently created empty history")
	}
}
