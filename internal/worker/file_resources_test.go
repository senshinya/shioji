package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/harness/antigravity"
)

func resourceSession(t *testing.T) (*Server, domain.Workspace, domain.Session) {
	t.Helper()
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	op, err := s.submit(domain.Submission{OperationID: "resource-session", WorkspaceID: w.ID, Text: "inspect", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	d, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	return s, w, d.Session
}
func TestFileResourceReadsLargeFilesWithinAcceptedSession(t *testing.T) {
	s, w, session := resourceSession(t)
	path := filepath.Join(w.Folders[0], "review.mp4")
	data := bytes.Repeat([]byte("video-data"), 1<<20)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	query := map[string]any{"workspaceId": w.ID, "sessionId": session.ID, "path": "review.mp4"}
	body, err := fileCommand(t, s, "file.resolve", query)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Path, Name, MIME, Version string
		Size                      int64
	}
	if err = json.Unmarshal(body, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Path != path || meta.Name != "review.mp4" || meta.Size != int64(len(data)) || meta.Version == "" {
		t.Fatal("wrong resource identity", meta)
	}
	query["version"] = meta.Version
	query["offset"] = int64(len(data) - 100)
	body, err = fileCommand(t, s, "file.read", query)
	if err != nil {
		t.Fatal(err)
	}
	var chunk struct {
		Data     []byte
		Offset   int64
		Complete bool
	}
	json.Unmarshal(body, &chunk)
	if !chunk.Complete || chunk.Offset != int64(len(data)) || !bytes.Equal(chunk.Data, data[len(data)-100:]) {
		t.Fatal("wrong final chunk", chunk.Offset, len(chunk.Data))
	}
	go s.Serve()
	target := app.AttachmentTarget{Directory: s.Directory(), DeviceID: s.store.DeviceID(), WorkspaceID: w.ID}
	downloaded, e := target.FetchFile(context.Background(), t.TempDir(), session.ID, path, nil)
	if e != nil {
		t.Fatal("authenticated owning worker transfer failed", e)
	}
	saved, e := os.ReadFile(downloaded.Path)
	if e != nil || !bytes.Equal(saved, data) {
		t.Fatal("worker transfer changed bytes", e)
	}
	os.WriteFile(path, []byte("changed"), 0600)
	if _, err = fileCommand(t, s, "file.read", query); err == nil {
		t.Fatal("changed resource was accepted")
	}
}
func TestFileResourceRejectsWrongScopeAndSymlinkEscapes(t *testing.T) {
	s, w, session := resourceSession(t)
	home, _ := os.UserHomeDir()
	outside, err := os.MkdirTemp(home, ".shioji-resource-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	path := filepath.Join(outside, "private.txt")
	os.WriteFile(path, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(w.Folders[0], "escape"))
	for _, path := range []string{path, "escape/private.txt", ".", "missing.txt"} {
		if _, err := fileCommand(t, s, "file.resolve", map[string]any{"workspaceId": w.ID, "sessionId": session.ID, "path": path}); err == nil {
			t.Fatal("accepted invalid resource", path)
		}
	}
	if _, err := fileCommand(t, s, "file.resolve", map[string]any{"workspaceId": w.ID, "path": w.Folders[0]}); err == nil {
		t.Fatal("resource read without owning session")
	}
	// A new workspace folder must not expand an existing session's access.
	w.Folders = append(w.Folders, outside)
	if _, err = s.store.UpdateWorkspace(w); err != nil {
		t.Fatal(err)
	}
	if _, err = fileCommand(t, s, "file.resolve", map[string]any{"workspaceId": w.ID, "sessionId": session.ID, "path": path}); err == nil {
		t.Fatal("workspace edit expanded existing session scope")
	}
}
func TestFileResourceSupportsOwningDeviceTemporaryOutputs(t *testing.T) {
	s, w, session := resourceSession(t)
	path := filepath.Join(t.TempDir(), "preview.pdf")
	os.WriteFile(path, []byte("%PDF-1.7\n"), 0600)
	body, err := fileCommand(t, s, "file.resolve", map[string]any{"workspaceId": w.ID, "sessionId": session.ID, "path": path})
	if err != nil {
		t.Fatal(err)
	}
	var meta struct{ MIME string }
	json.Unmarshal(body, &meta)
	if meta.MIME != "application/pdf" {
		t.Fatal("wrong media detection", meta)
	}
}

func TestAntigravityFileResourcesStayInTheirNativeSession(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := os.MkdirTemp(home, ".shioji-agy-output-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(profile) })
	t.Setenv("GEMINI_HOME", profile)
	s, w := memoryWorker(t, antigravity.Adapter{}, memoryHarness{id: "alpha"})
	newSession := func(harnessID, threadID string) domain.Session {
		t.Helper()
		op, err := s.store.AcceptForHarness(domain.Submission{OperationID: harnessID, WorkspaceID: w.ID, Text: "inspect"}, harnessID, domain.LaunchContext{CWD: w.Folders[0]})
		if err != nil {
			t.Fatal(err)
		}
		session, err := s.store.SessionRecord(op.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		session.ThreadID = threadID
		if _, err := s.store.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
			t.Fatal(err)
		}
		return session
	}
	const thread = "00000000-0000-4000-8000-000000000001"
	agy := newSession("antigravity", thread)
	other := newSession("alpha", thread)
	brain := filepath.Join(profile, "antigravity-cli", "brain")
	report := filepath.Join(brain, thread, "report.md")
	image := filepath.Join(brain, thread, "comparison.png")
	sibling := filepath.Join(brain, "00000000-0000-4000-8000-000000000002", "report.md")
	config := filepath.Join(profile, "config", "private.txt")
	imageHeader := []byte("\x89PNG\r\n\x1a\n")
	for path, data := range map[string][]byte{report: []byte("# Report"), image: imageHeader, sibling: []byte("other thread"), config: []byte("config")} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(filepath.Dir(report), "escape.txt")
	if err := os.Symlink(config, link); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	target := app.AttachmentTarget{Directory: s.Directory(), DeviceID: s.store.DeviceID(), WorkspaceID: w.ID}
	for _, tc := range []struct {
		path, mime string
		data       []byte
	}{{report, "text/", []byte("# Report")}, {image, "image/png", imageHeader}} {
		got, err := target.FetchFile(context.Background(), t.TempDir(), agy.ID, tc.path, nil)
		if err != nil {
			t.Fatalf("could not preview %s: %v", filepath.Base(tc.path), err)
		}
		data, err := os.ReadFile(got.Path)
		if err != nil || !bytes.Equal(data, tc.data) || got.Resource.Name != filepath.Base(tc.path) || !strings.HasPrefix(got.Resource.MIME, tc.mime) {
			t.Fatalf("wrong preview for %s: %v, %q", tc.path, err, got.Resource.MIME)
		}
	}
	for _, tc := range []struct{ session, path string }{{agy.ID, sibling}, {agy.ID, config}, {agy.ID, link}, {other.ID, report}} {
		if _, err := target.FetchFile(context.Background(), t.TempDir(), tc.session, tc.path, nil); err == nil {
			t.Fatalf("preview accepted an out-of-scope path %s", tc.path)
		}
	}
	if err := os.Rename(filepath.Dir(report), filepath.Dir(report)+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(sibling), filepath.Dir(report)); err != nil {
		t.Fatal(err)
	}
	if _, err := target.FetchFile(context.Background(), t.TempDir(), agy.ID, report, nil); err == nil {
		t.Fatal("preview accepted another thread through a symlinked session root")
	}
}
