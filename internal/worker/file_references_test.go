package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
)

func fileCommand(t *testing.T, s *Server, method string, input any) (json.RawMessage, error) {
	t.Helper()
	b, _ := json.Marshal(input)
	out, err := s.handle(method, b)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(out)
	return data, err
}
func TestFileReferenceSearchAndImmutableDeviceSnapshot(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	root := w.Folders[0]
	if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "rotate.go"), []byte("package original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := fileCommand(t, s, "file.search", map[string]any{"workspaceId": w.ID, "query": "rot"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "src/rotate.go") {
		t.Fatal("file search did not find owning-device file", string(data))
	}
	data, err = fileCommand(t, s, "file.snapshot", map[string]any{"workspaceId": w.ID, "root": root, "path": "src/rotate.go"})
	if err != nil {
		t.Fatal(err)
	}
	var a domain.Attachment
	if err = json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "src", "rotate.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := s.attachments.Path(w.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != "package original\n" {
		t.Fatal("reference changed after selection", string(saved), err)
	}
	in := domain.Submission{OperationID: "referenced", WorkspaceID: w.ID, Attachments: []domain.Attachment{a}, Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	if _, err = s.submit(in); err != nil {
		t.Fatal("snapshot not accepted as ordinary immutable input", err)
	}
	awaitReceipt(t, s, in.OperationID)
}
func TestFileReferencesRejectEscapesAndSessionAccessExpansion(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(w.Folders[0], "escape"))
	for _, in := range []map[string]any{
		{"workspaceId": w.ID, "root": w.Folders[0], "path": "../secret"},
		{"workspaceId": w.ID, "root": outside, "path": "secret"},
		{"workspaceId": w.ID, "root": w.Folders[0], "path": "escape/secret"},
	} {
		if _, err := fileCommand(t, s, "file.snapshot", in); err == nil {
			t.Fatal("read outside accepted roots", in)
		}
	}
	op, err := s.submit(domain.Submission{OperationID: "launch", WorkspaceID: w.ID, Text: "start", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, "launch")
	latest, err := s.store.Workspace(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Adding a folder must not enlarge an existing session's accepted context.
	latest.Folders = append(latest.Folders, outside)
	_, err = s.store.UpdateWorkspace(latest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fileCommand(t, s, "file.snapshot", map[string]any{"workspaceId": w.ID, "sessionId": op.SessionID, "root": outside, "path": "secret"}); err == nil {
		t.Fatal("workspace change expanded existing session access")
	}
}

func TestFileSearchRespectsGitIgnoreAndSnapshotRejectsBinary(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	root := w.Folders[0]
	if _, err := gitRun(s.ctx, root, "init"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("secret.txt\n"), 0600)
	os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0600)
	os.WriteFile(filepath.Join(root, "visible.txt"), []byte("visible"), 0600)
	os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 2}, 0600)
	data, err := fileCommand(t, s, "file.search", map[string]any{"workspaceId": w.ID, "query": ".txt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret.txt") || !strings.Contains(string(data), "visible.txt") {
		t.Fatal("ignored files leaked into search", string(data))
	}
	if _, err = fileCommand(t, s, "file.snapshot", map[string]any{"workspaceId": w.ID, "root": root, "path": "binary.bin"}); err == nil {
		t.Fatal("binary file accepted as textual context")
	}
}

func TestHistoryReferenceReplacementUsesOwningSnapshotsAndPersistsPositions(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha", restart: true})
	root := w.Folders[0]
	snapshot := func(name string) domain.Attachment {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte("package tree\n"), 0600); err != nil {
			t.Fatal(err)
		}
		raw, err := fileCommand(t, s, "file.snapshot", map[string]any{"workspaceId": w.ID, "root": root, "path": name})
		if err != nil {
			t.Fatal(err)
		}
		var a domain.Attachment
		if err = json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	old, next := snapshot("old.go"), snapshot("next.go")
	cfg := domain.RunConfig{Model: "alpha-model", Permission: "limited"}
	first, err := s.submit(domain.Submission{OperationID: "original-inline", WorkspaceID: w.ID, Text: "检查 ", Attachments: []domain.Attachment{old}, ReferencePositions: []domain.ReferencePosition{{AttachmentID: old.ID, Position: 4}}, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	s.dispatchMu.Lock()
	before, err := s.store.Session(first.SessionID, 0, 100)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Items[0].ReferencePositions) != 1 {
		t.Fatal("dispatch dropped accepted positions", before.Items)
	}
	refs := &domain.HistoryReferences{Attachments: []domain.Attachment{next}, ReferencePositions: []domain.ReferencePosition{{AttachmentID: next.ID, Position: 1}}}
	change := domain.HistoryChange{OperationID: "replace-inline", SessionID: first.SessionID, Revision: before.Session.Revision, ItemID: "input:original-inline", Restart: true, Text: "讲解", Config: cfg, References: refs}
	result, err := s.changeHistory(change)
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, change.OperationID)
	s.dispatchMu.Lock()
	after, err := s.store.Session(first.SessionID, 0, 100)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.ID != first.SessionID || len(after.Items[0].Attachments) != 1 || after.Items[0].Attachments[0].ID != next.ID || len(after.Items[0].ReferencePositions) != 1 || after.Items[0].ReferencePositions[0].Position != 1 {
		t.Fatal("restarted history lost changed references", after.Items)
	}
	// A retried request is decoded into a new References pointer. Idempotency
	// compares its values and never submits the replacement again.
	raw, _ := json.Marshal(change)
	var retry domain.HistoryChange
	json.Unmarshal(raw, &retry)
	again, err := s.changeHistory(retry)
	if err != nil || again.ID != result.ID {
		t.Fatal("decoded retry was not idempotent", again, err)
	}
}

func TestRuntimeLaunchFailureKeepsInlineReferencePositions(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	in := domain.Submission{OperationID: "failed-inline", WorkspaceID: w.ID, HarnessID: "alpha", Text: "检查", Skills: []domain.SkillReference{{ID: "skill", Name: "check"}}, ReferencePositions: []domain.ReferencePosition{{SkillID: "skill", Position: 1}}}
	op, err := s.store.Accept(in)
	if err != nil {
		t.Fatal(err)
	}
	s.failRuntimeLaunch(op, in, errors.New("launcher unavailable"))
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Items) == 0 || d.Items[0].Status != "failed" || len(d.Items[0].ReferencePositions) != 1 || d.Items[0].ReferencePositions[0] != in.ReferencePositions[0] {
		t.Fatal("launch failure dropped inline positions", d.Items)
	}
}
