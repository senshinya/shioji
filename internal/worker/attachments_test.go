package worker_test

import (
	"bytes"
	"context"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/worker"
	"strings"
	"testing"
)

func TestWorkerAcceptsUploadedTextAndRejectsForeignResource(t *testing.T) {
	server, c, provider := startServer(t)
	var w, other domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Attach", Folders: []string{t.TempDir()}}, &w)
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Other", Folders: []string{t.TempDir()}}, &other)
	data := []byte("package sample\n")
	a, _ := attachments.Describe("sample.go", data)
	var receipt attachments.Receipt
	call(t, c, "attachment.put", map[string]any{"workspaceId": w.ID, "attachment": a, "data": data}, &receipt)
	if !receipt.Complete {
		t.Fatal("upload not committed")
	}
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	in := domain.Submission{OperationID: "foreign-attach", WorkspaceID: other.ID, Attachments: []domain.Attachment{a}, Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}
	if err := c.Call(context.Background(), "session.submit", in, nil); err == nil {
		t.Fatal("foreign attachment accepted")
	}
	in.OperationID = "accepted-attach"
	in.WorkspaceID = w.ID
	var op domain.Operation
	call(t, c, "session.submit", in, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	found := false
	for _, i := range d.Items {
		if i.Kind == "user" {
			found = len(i.Attachments) == 1 && i.Attachments[0] == a
		}
	}
	if !strings.Contains(strings.Join(provider.Inputs(), "\n"), "Attachment: sample.go") {
		t.Fatal("text attachment did not reach actual model request")
	}
	_ = server
	if !found {
		t.Fatal("accepted attachment lost from history")
	}
}

func TestRealAppServerReceivesUploadedImageAsImage(t *testing.T) {
	server, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Images", Folders: []string{t.TempDir()}}, &w)
	var encoded bytes.Buffer
	png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	staging := attachments.New(t.TempDir())
	a, err := staging.Save("image.png", encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	target := app.AttachmentTarget{Directory: server.Directory(), DeviceID: c.DeviceID, WorkspaceID: w.ID}
	if err = target.Upload(context.Background(), staging, a); err != nil {
		t.Fatal(err)
	}
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "vision", WorkspaceID: w.ID, Attachments: []domain.Attachment{a}, Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed" || d.Session.Status == "failed"
	})
	requests := strings.Join(p.Inputs(), "\n")
	if !strings.Contains(requests, "input_image") || !strings.Contains(requests, "data:image/png;base64,") {
		t.Fatal("image did not reach actual pinned app-server model request", d.Session.Status, requests)
	}
	if _, err = target.Download(context.Background(), attachments.New(t.TempDir()), a); err != nil {
		t.Fatal("other client could not retrieve history attachment", err)
	}
}

func TestRealAppServerAcceptsJPEGAndGIFInputs(t *testing.T) {
	for _, format := range []string{"jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			server, c, p := startServer(t)
			var w domain.Workspace
			call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Image formats", Folders: []string{t.TempDir()}}, &w)
			var encoded bytes.Buffer
			img := image.NewRGBA(image.Rect(0, 0, 16, 16))
			var err error
			if format == "jpeg" {
				err = jpeg.Encode(&encoded, img, nil)
			} else {
				err = gif.Encode(&encoded, img, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			stage := attachments.New(t.TempDir())
			ref, err := stage.Save("image."+format, encoded.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			target := app.AttachmentTarget{Directory: server.Directory(), DeviceID: c.DeviceID, WorkspaceID: w.ID}
			if err = target.Upload(context.Background(), stage, ref); err != nil {
				t.Fatal(err)
			}
			var ready domain.Readiness
			call(t, c, "readiness", nil, &ready)
			var op domain.Operation
			call(t, c, "session.submit", domain.Submission{OperationID: "vision-" + format, WorkspaceID: w.ID, Attachments: []domain.Attachment{ref}, Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
			var d domain.SessionDetail
			wait(t, func() bool {
				call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
				return d.Session.Status == "completed" || d.Session.Status == "failed"
			})
			request := strings.Join(p.Inputs(), "\n")
			if d.Session.Status != "completed" || !strings.Contains(request, "input_image") || !strings.Contains(request, "data:image/") {
				t.Fatal("actual app-server rejected image format", format, d.Session.Error)
			}
		})
	}
}
