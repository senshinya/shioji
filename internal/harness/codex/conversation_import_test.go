package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/testutil"
)

func TestConversationPrefixDetectsChangesBeyondDisplayedText(t *testing.T) {
	var a, b wire.Item
	for _, row := range []struct {
		item *wire.Item
		tail string
	}{{&a, "before"}, {&b, "after"}} {
		raw, _ := json.Marshal(map[string]string{"id": "i", "type": "agentMessage", "text": strings.Repeat("x", 128<<10) + row.tail})
		if err := json.Unmarshal(raw, row.item); err != nil {
			t.Fatal(err)
		}
	}
	if normalizeItem(a, true).Text != normalizeItem(b, true).Text {
		t.Fatal("fixture does not reach display bound")
	}
	left := []conversationTurn{{Turn: wire.Turn{ID: "t", Status: "completed"}, Items: []wire.Item{a}}}
	right := []conversationTurn{{Turn: wire.Turn{ID: "t", Status: "completed"}, Items: []wire.Item{b}}}
	if conversationSourceDigest(left) == conversationSourceDigest(right) {
		t.Fatal("undisplayed source change was accepted as the imported prefix")
	}
}

func TestCompleteImportReadsEverySourcePageAndRejectsUndisplayedChanges(t *testing.T) {
	changed, forks := false, 0
	r := &Runtime{protocol: &protocolSupport{methods: map[string]bool{"thread/read": true, "thread/turns/list": true, "thread/fork": true, "thread/unsubscribe": true, "thread/loaded/list": true}}, pending: map[string]chan wire.Message{}, done: make(chan struct{})}
	r.stdin = workInputWriter{write: func(b []byte) (int, error) {
		var q wire.Message
		json.Unmarshal(b, &q)
		var args struct {
			Cursor        string
			SortDirection string
		}
		json.Unmarshal(q.Params, &args)
		var result any
		switch q.Method {
		case "thread/read":
			result = map[string]any{"thread": map[string]any{"id": "source", "cwd": "/project", "preview": "Full history", "status": map[string]any{"type": "idle"}}}
		case "thread/turns/list":
			start, _ := strconv.Atoi(args.Cursor)
			var data []any
			for i := start; i < min(start+10, 60); i++ {
				index := i
				if args.SortDirection == "desc" {
					index = 59 - i
				}
				var items []any
				for j := range 5 {
					text := fmt.Sprintf("source-%d-%d ", index, j) + strings.Repeat("x", 4096)
					if changed && index == 0 && j == 0 {
						text += "CHANGED"
					}
					items = append(items, map[string]any{"id": fmt.Sprintf("i-%d-%d", index, j), "type": "agentMessage", "text": text})
				}
				data = append(data, map[string]any{"id": fmt.Sprintf("t-%d", index), "status": "completed", "itemsView": "full", "items": items})
			}
			page := map[string]any{"data": data}
			if start+10 < 60 {
				page["nextCursor"] = strconv.Itoa(start + 10)
			}
			result = page
		case "thread/fork":
			forks++
			result = map[string]any{"thread": map[string]any{"id": "private-copy", "forkedFromId": "source"}, "cwd": "/project"}
		case "thread/loaded/list":
			result = map[string]any{"data": []string{}}
		case "thread/unsubscribe":
			result = map[string]any{}
		default:
			t.Fatalf("unexpected source mutation: %s", q.Method)
		}
		encoded, _ := json.Marshal(result)
		r.mu.Lock()
		ch := r.pending[string(q.ID)]
		r.mu.Unlock()
		ch <- wire.Message{Result: encoded}
		return len(b), nil
	}}
	ctx := context.Background()
	launch := domain.LaunchContext{CWD: "/project"}
	preview, err := r.ReadConversation(ctx, launch, "source")
	if err != nil || !preview.Truncated || len(preview.Items) >= 300 {
		t.Fatal("preview no longer bounded", err)
	}
	full, err := r.ImportConversation(ctx, launch, "source")
	if err != nil || full.Truncated || full.ResumeRef == "" || len(full.Items) != 300 || len(full.Turns) != 60 || !strings.HasPrefix(full.Items[0].Text, "source-0-0 ") || forks != 0 {
		t.Fatal("full import lost paged context or executed source", len(full.Items), len(full.Turns), err)
	}
	s := domain.Session{Context: launch, Origin: &domain.ConversationOrigin{SourceRef: "source", ResumeRef: full.ResumeRef}}
	changed = true
	if _, err = r.PrepareConversation(ctx, s); err == nil || forks != 0 {
		t.Fatal("undisplayed source mutation was accepted", err)
	}
	changed = false
	if id, err := r.PrepareConversation(ctx, s); err != nil || id != "private-copy" || forks != 1 {
		t.Fatal("complete source prefix cannot continue", id, err)
	}
}

func TestConversationDiscoveryDoesNotRequireUnusedSourceDirectoryFilter(t *testing.T) {
	data, err := os.ReadFile("../../../design/protocol/codex-0.166.0/types.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema protocolSchema
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	schema.OneOf = schema.Definitions["ClientRequest"].OneOf
	shape := schema.Definitions["ThreadListParams"]
	if shape.Properties == nil {
		t.Fatal("baseline lacks history list shape")
	}
	delete(shape.Properties, "cwd")
	schema.Definitions["ThreadListParams"] = shape
	data, _ = json.Marshal(schema)
	support, err := protocolFromSchema(data)
	if err != nil || !support.methods["thread/list"] {
		t.Fatal("unused optional query field disabled discovery", err)
	}
}

func TestConversationImportKeepsReadableHistoryWhenOneTurnExceedsBudget(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		item := map[string]any{"type": "message", "id": "large", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": strings.Repeat("large reply ", 40000)}}}
		for _, event := range []map[string]any{{"type": "response.created", "response": map[string]any{"id": "large"}}, {"type": "response.output_item.done", "item": item}, {"type": "response.completed", "response": map[string]any{"id": "large", "usage": map[string]int{"input_tokens": 10, "output_tokens": 10, "total_tokens": 20}}}} {
			b, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], b)
		}
	}))
	defer model.Close()
	home := t.TempDir()
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n", model.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan struct{}, 1)
	r, err := Start(ctx, Options{Home: home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && e.Turn.Status == "completed" {
			done <- struct{}{}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	s := domain.Session{ID: "large", Context: domain.LaunchContext{CWD: dir}, Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Submit(ctx, s, "large-input", "Explain rotations"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	r.Close()
	reader, err := Start(ctx, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	p, err := reader.ReadConversation(ctx, s.Context, s.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated || p.ResumeRef != "" || len(p.Items) < 2 || p.Items[0].Text != "Explain rotations" {
		t.Fatal("oversized history became empty or silently continuable", p.Truncated, len(p.Items), p.Reason)
	}
	encoded, _ := json.Marshal(p)
	if len(encoded) > 512<<10 {
		t.Fatal("preview exceeds worker frame budget", len(encoded))
	}
	importer, ok := any(reader).(interface {
		ImportConversation(context.Context, domain.LaunchContext, string) (domain.ConversationPreview, error)
	})
	if !ok {
		t.Fatal("import still reuses the bounded, read-only preview")
	}
	full, err := importer.ImportConversation(ctx, s.Context, s.ThreadID)
	if err != nil || full.Truncated || full.ResumeRef == "" || len(full.Items) != 2 {
		t.Fatal("complete source context was lost to the preview budget", full.Reason, err)
	}
	s.Origin = &domain.ConversationOrigin{SourceRef: s.ThreadID, ResumeRef: full.ResumeRef}
	fork, err := reader.PrepareConversation(ctx, s)
	if err != nil || fork == "" || fork == s.ThreadID {
		t.Fatal("large source cannot continue independently", fork, err)
	}
}

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
