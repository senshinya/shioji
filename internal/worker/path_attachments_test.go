package worker_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/worker"
	"strings"
	"testing"
)

func TestPathAttachmentActualWorkerAndCodexUseOwningDevicePath(t *testing.T) {
	server, client, provider := startServer(t)
	var workspace, foreign domain.Workspace
	call(t, client, "workspace.create", worker.WorkspaceInput{Name: "Binary references", Folders: []string{t.TempDir()}}, &workspace)
	call(t, client, "workspace.create", worker.WorkspaceInput{Name: "Foreign", Folders: []string{t.TempDir()}}, &foreign)
	stage := attachments.New(t.TempDir())
	data := bytes.Repeat([]byte("NEVER_EMBED_BINARY\x00\xff"), 7000)
	a, err := stage.SavePathResource("design.zip", data)
	if err != nil {
		t.Fatal(err)
	}
	var receipt attachments.Receipt
	call(t, client, "attachment.put", protocol.AttachmentPut{WorkspaceID: workspace.ID, Attachment: a, Data: data[:attachments.ChunkSize]}, &receipt)
	var ready domain.Readiness
	call(t, client, "readiness", nil, &ready)
	if !ready.Capabilities.PathAttachments {
		t.Fatal("actual adapter doesn't advertise path input")
	}
	in := domain.Submission{OperationID: domain.NewID(), WorkspaceID: workspace.ID, Text: "检查上传的资料", Attachments: []domain.Attachment{a}, Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}
	if err := client.Call(context.Background(), "session.submit", in, nil); err == nil {
		t.Fatal("partial binary resource submitted")
	}
	target := app.AttachmentTarget{Directory: server.Directory(), DeviceID: client.DeviceID, WorkspaceID: workspace.ID}
	var received int64
	if err := target.UploadProgress(context.Background(), stage, a, func(offset int64) { received = offset }); err != nil {
		t.Fatal(err)
	}
	if received != int64(len(data)) {
		t.Fatal("real worker didn't acknowledge complete upload", received)
	}
	privatePath, err := attachments.New(filepath.Join(server.Directory(), "attachments")).Path(workspace.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(privatePath); err != nil || !bytes.Equal(got, data) {
		t.Fatal("owning-device bytes differ", err)
	}
	spoofed := a
	spoofed.ID, spoofed.SourcePath = domain.NewID(), "/etc/passwd"
	if err := client.Call(context.Background(), "attachment.put", protocol.AttachmentPut{WorkspaceID: workspace.ID, Attachment: spoofed, Data: data[:attachments.ChunkSize]}, nil); err == nil {
		t.Fatal("client-chosen remote file path accepted")
	}
	in.WorkspaceID = foreign.ID
	if err := client.Call(context.Background(), "session.submit", in, nil); err == nil {
		t.Fatal("foreign workspace could use the binary")
	}
	in.WorkspaceID, in.OperationID = workspace.ID, domain.NewID()
	var op domain.Operation
	call(t, client, "session.submit", in, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, client, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return detail.Session.Status == "completed" || detail.Session.Status == "failed"
	})
	requests := strings.Join(provider.Inputs(), "\n")
	if detail.Session.Status != "completed" || !strings.Contains(requests, privatePath) || !strings.Contains(requests, "design.zip") || strings.Contains(requests, "NEVER_EMBED_BINARY") || strings.Contains(requests, stage.Directory) {
		t.Fatal("actual app-server didn't receive only the owning-device path", detail.Session.Status, requests)
	}
	found := false
	for _, item := range detail.Items {
		if item.Kind == "user" && len(item.Attachments) == 1 && item.Attachments[0] == a {
			found = true
		}
	}
	if !found {
		t.Fatal("history lost immutable binary reference")
	}
	copyPath, err := target.Download(context.Background(), attachments.New(t.TempDir()), a)
	if err != nil {
		t.Fatal("another client can't retrieve the resource", err)
	}
	if copy, err := os.ReadFile(copyPath); err != nil || !bytes.Equal(copy, data) {
		t.Fatal("second client received different bytes", err)
	}
}
