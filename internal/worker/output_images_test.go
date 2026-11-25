package worker

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/store"
)

type imageHarness struct {
	memoryHarness
	data       []byte
	kind, name string
}

func TestOutputImagesFromDeviceTemporaryDirectories(t *testing.T) {
	// Isolate the worker's TMPDIR from the workspace and rejected outside file.
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	public, err := os.MkdirTemp("/tmp", "shioji-output-image-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(public) })
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := db.CreateWorkspace("temporary images", []string{workspace})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "temporary-image", WorkspaceID: w.ID, HarnessID: "test", Text: "show image"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, attachments: attachments.New(t.TempDir())}
	var data bytes.Buffer
	if err = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 20, 10))); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{temporary, public} {
		if err = os.WriteFile(filepath.Join(dir, "result.png"), data.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for label, dir := range map[string]string{"worker-temp": temporary, "tmp": public} {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, root := range []string{dir, real} {
			source := harness.OutputResource{Kind: "image", SessionID: op.SessionID, ItemID: label, Path: filepath.Join(root, "result.png")}
			a, err := s.importOutputResource("test", op.SessionID, source)
			if err != nil {
				t.Error("device temporary image cannot be previewed", root, err)
				continue
			}
			got, err := s.attachments.Get(w.ID, a.ID, 0)
			if err != nil || !bytes.Equal(got.Data, data.Bytes()) || a.MIME != "image/png" {
				t.Error("temporary image was not copied into an immutable workspace resource", a, err)
			}
		}
	}
	if err = os.WriteFile(outside, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	text := filepath.Join(temporary, "not-an-image.png")
	if err = os.WriteFile(text, []byte("plain text"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(temporary, "escape.png")
	if err = os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(temporary, "pipe.png")
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(temporary, "large.png")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate((8 << 20) + 1)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []harness.OutputResource{
		{Kind: "image", Path: outside},
		{Kind: "image", Path: link},
		{Kind: "image", Path: text},
		{Kind: "image", Path: fifo},
		{Kind: "image", Path: large},
		{Kind: "file", Path: text},
	} {
		source.SessionID, source.ItemID = op.SessionID, "rejected"
		if _, err = s.importOutputResource("test", op.SessionID, source); err == nil {
			t.Error("temporary image exception permitted a forbidden resource", source.Kind, source.Path)
		}
	}
}

func (a imageHarness) Start(_ context.Context, o harness.StartOptions) (harness.Runtime, error) {
	return &imageRuntime{memoryRuntime: &memoryRuntime{adapter: a.memoryHarness, options: o, done: make(chan struct{})}, data: a.data, kind: a.kind, name: a.name}, nil
}

type imageRuntime struct {
	*memoryRuntime
	data       []byte
	kind, name string
}

func (r *imageRuntime) Submit(_ context.Context, s domain.Session, op string, _ harness.TurnInput) (string, error) {
	turn := "turn/" + op
	if err := r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: turn, Status: "running"}}); err != nil {
		return "", err
	}
	kind, name := r.kind, r.name
	if kind == "" {
		kind, name = "image", "diagram.png"
	}
	ref, err := r.options.ImportResource(harness.OutputResource{Kind: kind, SessionID: s.ID, ItemID: turn + "/image", Name: name, Data: r.data})
	if err != nil {
		return "", err
	}
	if err = r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "item.upsert", Item: &domain.Item{ID: turn + "/image", TurnID: turn, Kind: "tool", Presentation: kind, Name: "生成文件", Final: true, Parts: []domain.ContentPart{{Kind: kind, Attachment: &ref}}}}); err != nil {
		return "", err
	}
	return turn, r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: turn, Status: "completed"}})
}

func TestOutputResourceCanBeDownloadedByAnotherClientAfterHistoryLoad(t *testing.T) {
	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 30, 20)))
	s, w := memoryWorker(t, imageHarness{memoryHarness: memoryHarness{id: "images"}, data: data.Bytes()})
	go s.Serve()
	op, err := s.submit(domain.Submission{WorkspaceID: w.ID, HarnessID: "images", OperationID: "render-image", Text: "draw", Config: domain.RunConfig{Model: "images-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := awaitReceipt(t, s, op.ID); got.State != "confirmed" {
		t.Fatal(got)
	}
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ref *domain.Attachment
	for _, item := range d.Items {
		for _, part := range item.Parts {
			if part.Kind == "image" {
				ref = part.Attachment
			}
		}
	}
	if ref == nil {
		t.Fatal("worker history lost the output resource", d.Items)
	}
	target := app.AttachmentTarget{Directory: s.Directory(), DeviceID: s.store.DeviceID(), WorkspaceID: w.ID}
	path, err := target.Download(context.Background(), attachments.New(t.TempDir()), *ref)
	if err != nil {
		t.Fatal("independent authenticated image transfer failed", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data.Bytes()) {
		t.Fatal("other client received different pixels", err)
	}
}

func TestOutputResourceSnapshotIsImmutableScopedAndIdempotent(t *testing.T) {
	// Keep the rejected file outside the worker's permitted temporary roots.
	outside := filepath.Join(t.TempDir(), "secret.png")
	t.Setenv("TMPDIR", t.TempDir())
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := db.CreateWorkspace("images", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "image", WorkspaceID: w.ID, HarnessID: "test", Text: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, attachments: attachments.New(t.TempDir())}
	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 20, 10)))
	path := filepath.Join(dir, "result.png")
	os.WriteFile(path, data.Bytes(), 0600)
	source := harness.OutputResource{Kind: "image", SessionID: op.SessionID, ItemID: "turn/render", Path: path}
	a, err := s.importOutputResource("test", op.SessionID, source)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.importOutputResource("test", op.SessionID, source)
	if err != nil || a != b {
		t.Fatal("duplicate output created another resource", a, b, err)
	}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: source.ItemID, Kind: "tool", Final: true, Parts: []domain.ContentPart{{Kind: "image", Attachment: &a}}}}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("replaced"), 0600)
	b, err = s.importOutputResource("test", op.SessionID, source)
	if err != nil || a != b {
		t.Fatal("history refresh reread a mutable image", err)
	}
	got, err := s.attachments.Get(w.ID, a.ID, 0)
	if err != nil || !bytes.Equal(got.Data, data.Bytes()) {
		t.Fatal("output still depends on mutable device path", err)
	}
	if _, err = s.attachments.Get(domain.NewID(), a.ID, 0); err == nil {
		t.Fatal("foreign workspace read image")
	}
	os.WriteFile(outside, data.Bytes(), 0600)
	link := filepath.Join(dir, "escaped.png")
	os.Symlink(outside, link)
	for _, bad := range []harness.OutputResource{
		{Kind: "image", SessionID: op.SessionID, ItemID: "outside", Path: outside},
		{Kind: "image", SessionID: op.SessionID, ItemID: "link", Path: link},
		{Kind: "image", SessionID: domain.NewID(), ItemID: "foreign", Data: data.Bytes()},
		{Kind: "image", SessionID: op.SessionID, ItemID: "inline", Data: []byte("not an image")},
	} {
		if _, err = s.importOutputResource("test", op.SessionID, bad); err == nil {
			t.Fatal("invalid output imported", bad.Path)
		}
	}
	if _, err = s.importOutputResource("other", op.SessionID, harness.OutputResource{Kind: "image", SessionID: op.SessionID, ItemID: "inline", Data: data.Bytes()}); err == nil {
		t.Fatal("foreign harness imported image")
	}
}

func TestToolAudioStaysAWorkspaceOwnedOutputSnapshot(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	workspace, err := db.CreateWorkspace("audio", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "sound", WorkspaceID: workspace.ID, HarnessID: "test", Text: "beep"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: db, attachments: attachments.New(t.TempDir())}
	wav := []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x80\x3e\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00")
	source := harness.OutputResource{Kind: "audio", SessionID: op.SessionID, ItemID: "tool", Name: "tool-output.wav", Data: wav}
	ref, err := s.importOutputResource("test", op.SessionID, source)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.attachments.Get(workspace.ID, ref.ID, 0)
	if err != nil || ref.MIME != "audio/wav" || !receipt.Complete || !bytes.Equal(receipt.Data, wav) {
		t.Fatalf("audio did not survive the ordinary attachment route: %v, %+v", err, ref)
	}
	_, err = s.attachmentInputs(domain.Submission{WorkspaceID: workspace.ID, Text: "reuse", Attachments: []domain.Attachment{ref}})
	if err == nil {
		t.Fatal("output-only audio entered model inputs")
	}
	source.Name = "tool-output.command"
	if _, err := s.importOutputResource("test", op.SessionID, source); err == nil {
		t.Fatal("audio bytes with an executable extension were accepted")
	}
	source.Name, source.Data = "tool-output.wav", []byte("not audio")
	if _, err := s.importOutputResource("test", op.SessionID, source); err == nil {
		t.Fatal("fake audio was accepted")
	}
}
