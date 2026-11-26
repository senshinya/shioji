package antigravity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

const fixtureHistoryID = "2544f711-8879-4358-a53a-43e27cc9d701"

func TestAGYHistoryUsesAnIsolatedReplay(t *testing.T) {
	a, log := fixtureAdapter(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Setenv("GEMINI_HOME", root)
	one := historyFixture{Root: root, CWD: cwd}
	raw, _ := json.Marshal(one)
	if err := os.WriteFile(log+".history", raw, 0600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(root, "antigravity-cli", "acp_token.json")
	if err := os.WriteFile(token, []byte("fixture login state"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, fixtureHistoryID+".db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE fixture (note TEXT); INSERT INTO fixture VALUES ('opaque history')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	rt, err := a.Start(ctx, harness.StartOptions{CatalogOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	if !rt.Readiness().Capabilities.ConversationImport {
		t.Fatal("AGY did not advertise compatible history support")
	}
	reader, ok := rt.(harness.ConversationReader)
	if !ok {
		t.Fatal("AGY does not expose the native history reader")
	}
	list, err := reader.Conversations(ctx, []string{cwd}, "")
	if err != nil || len(list.Items) != 1 || list.Items[0].SourceRef != fixtureHistoryID {
		t.Fatalf("AGY returned the wrong workspace history: %+v, %v", list, err)
	}
	if _, err := reader.ReadConversation(ctx, domain.LaunchContext{CWD: root}, fixtureHistoryID); err == nil {
		t.Fatal("preview accepted a source from another workspace")
	}
	importer, ok := rt.(harness.ConversationImporter)
	if !ok {
		t.Fatal("AGY does not expose complete import")
	}
	out, err := importer.ImportConversation(ctx, domain.LaunchContext{CWD: cwd}, fixtureHistoryID)
	if err != nil || len(out.Turns) != 2 || len(out.Items) != 5 || out.Truncated {
		t.Fatalf("incomplete AGY replay: %+v, %v", out, err)
	}
	if out.Items[0].Kind != "user" || out.Items[0].Text != "first question" ||
		out.Items[1].Kind != "tool" || out.Items[1].Text != "verified" ||
		out.Items[2].Text != "first answer" || out.Items[4].Text != "second answer" ||
		out.Items[1].TurnID != out.Items[0].TurnID || out.Items[4].TurnID == out.Items[0].TurnID {
		t.Fatalf("AGY history was grouped incorrectly: %+v", out.Items)
	}
	login, err := os.ReadFile(token)
	if err != nil || string(login) != "fixture login state" {
		t.Fatal("history replay changed the native login state", err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(original, after) || hasMethod(log, "session/resume") || hasMethod(log, "session/prompt") {
		t.Fatal("history import changed or executed the source", err)
	}
}

type historyFixture struct{ Root, CWD string }

func TestAGYPreviewStaysBelowTheWorkerFrameLimit(t *testing.T) {
	p := domain.ConversationPreview{Turns: []domain.Turn{{ID: "turn"}}}
	for i := range 200 {
		p.Items = append(p.Items, domain.Item{ID: fmt.Sprint(i), TurnID: "turn", Kind: "assistant", Text: strings.Repeat("字", 2100), Final: true})
	}
	preview := boundHistoryPreview(p)
	raw, err := json.Marshal(preview)
	if err != nil || len(raw) > 512<<10 || !preview.Truncated || len(preview.Items) == 0 || preview.Items[len(preview.Items)-1].ID != "199" {
		t.Fatalf("preview cannot fit a worker response: %d bytes, %d items, %v", len(raw), len(preview.Items), err)
	}
	if len(p.Items) != 200 {
		t.Fatal("bounding the preview discarded changes from the full import")
	}
}
