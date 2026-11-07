package worker

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/store"
)

type imageHarness struct {
	memoryHarness
	data []byte
}

func (a imageHarness) Start(_ context.Context, o harness.StartOptions) (harness.Runtime, error) {
	return &imageRuntime{memoryRuntime: &memoryRuntime{adapter: a.memoryHarness, options: o, done: make(chan struct{})}, data: a.data}, nil
}

type imageRuntime struct {
	*memoryRuntime
	data []byte
}

func (r *imageRuntime) Submit(_ context.Context, s domain.Session, op string, _ harness.TurnInput) (string, error) {
	turn := "turn/" + op
	if err := r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: turn, Status: "running"}}); err != nil {
		return "", err
	}
	ref, err := r.options.ImportImage(harness.OutputImage{SessionID: s.ID, ItemID: turn + "/image", Name: "diagram.png", Data: r.data})
	if err != nil {
		return "", err
	}
	if err = r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "item.upsert", Item: &domain.Item{ID: turn + "/image", TurnID: turn, Kind: "tool", Presentation: "image", Name: "生成图片", Final: true, Parts: []domain.ContentPart{{Kind: "image", Attachment: &ref}}}}); err != nil {
		return "", err
	}
	return turn, r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: turn, Status: "completed"}})
}

func TestOutputImageCanBeDownloadedByAnotherClientAfterHistoryLoad(t *testing.T) {
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

func TestOutputImageSnapshotIsImmutableScopedAndIdempotent(t *testing.T) {
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
	source := harness.OutputImage{SessionID: op.SessionID, ItemID: "turn/render", Path: path}
	a, err := s.importOutputImage("test", op.SessionID, source)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.importOutputImage("test", op.SessionID, source)
	if err != nil || a != b {
		t.Fatal("duplicate output created another resource", a, b, err)
	}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: source.ItemID, Kind: "tool", Final: true, Parts: []domain.ContentPart{{Kind: "image", Attachment: &a}}}}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("replaced"), 0600)
	b, err = s.importOutputImage("test", op.SessionID, source)
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
	outside := filepath.Join(t.TempDir(), "secret.png")
	os.WriteFile(outside, data.Bytes(), 0600)
	link := filepath.Join(dir, "escaped.png")
	os.Symlink(outside, link)
	for _, bad := range []harness.OutputImage{
		{SessionID: op.SessionID, ItemID: "outside", Path: outside},
		{SessionID: op.SessionID, ItemID: "link", Path: link},
		{SessionID: domain.NewID(), ItemID: "foreign", Data: data.Bytes()},
		{SessionID: op.SessionID, ItemID: "inline", Data: []byte("not an image")},
	} {
		if _, err = s.importOutputImage("test", op.SessionID, bad); err == nil {
			t.Fatal("invalid output imported", bad.Path)
		}
	}
	if _, err = s.importOutputImage("other", op.SessionID, harness.OutputImage{SessionID: op.SessionID, ItemID: "inline", Data: data.Bytes()}); err == nil {
		t.Fatal("foreign harness imported image")
	}
}
