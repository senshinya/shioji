package ui

import (
	n "github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAttachmentPathInsertionKeepsNativeUndo(t *testing.T) {
	m := layoutModel()
	key := m.DraftKey()
	m.Drafts[key] = "检查文件"
	c := app.NewController("")
	v := New(m, c, filepath.Join(t.TempDir(), "client.json"))
	defer v.Saver.Close()
	ui := n.NewTester(v.View, 1440, 900)
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "sample.pdf")
	if err := os.WriteFile(file, []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	v.addAttachmentFiles(key, []string{file})
	select {
	case update := <-c.Updates:
		update(m)
	case <-time.After(3 * time.Second):
		t.Fatal("file preparation did not finish")
	}
	ui.Frame()
	withPath := m.Drafts[key]
	if !strings.Contains(withPath, file) {
		t.Fatalf("path missing from native composer: %q", withPath)
	}
	ui.Key(n.Cmd, n.KeyZ)
	if m.Drafts[key] != "检查文件" {
		t.Fatalf("path insertion cannot be undone: %q", m.Drafts[key])
	}
	ui.Key(n.Cmd|n.Shift, n.KeyZ)
	if m.Drafts[key] != withPath {
		t.Fatalf("path insertion cannot be redone: %q", m.Drafts[key])
	}
}

func TestAttachmentPathAfterPendingSendStartsNextDraft(t *testing.T) {
	v, ui, in := pendingSendRow(t)
	if err := v.appendAttachmentPaths("session:s", []string{`"/tmp/next.pdf"`}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if got := v.Model.Drafts["session:s"]; got != `"/tmp/next.pdf"` {
		t.Fatalf("path restored the already sent prompt: %q", got)
	}
	if pending := v.Model.Pending[in.OperationID]; pending.Text != in.Text {
		t.Fatal("path changed the pending submission")
	}
}

func TestAttachmentLimitsDoNotBecomePathReferences(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"large-text.txt", []byte(strings.Repeat("a", (128<<10)+1))},
		{"large-binary.zip", make([]byte, (8<<20)+1)},
		{"empty.txt", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := layoutModel()
			key := m.DraftKey()
			m.Drafts[key] = "保留草稿"
			c := app.NewController("")
			v := New(m, c, filepath.Join(t.TempDir(), "client.json"))
			defer v.Saver.Close()
			file := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(file, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			v.addAttachmentFiles(key, []string{file})
			select {
			case update := <-c.Updates:
				update(m)
			case <-time.After(3 * time.Second):
				t.Fatal("file preparation did not finish")
			}
			if m.Drafts[key] != "保留草稿" || len(m.Attachments[key]) != 0 || m.Error == "" {
				t.Fatalf("invalid file became input: text=%q attachments=%v error=%q", m.Drafts[key], m.Attachments[key], m.Error)
			}
		})
	}
}

// These exercise the actual asynchronous file preparation, including ownership
// changes before its result reaches the draft.
func TestUnsupportedAttachmentPaths(t *testing.T) {
	for _, mode := range []string{"local", "remote", "changed-device", "cancelled-edit"} {
		t.Run(mode, func(t *testing.T) {
			m := layoutModel()
			m.LocalDeviceID = "mac"
			m.Snapshot.Workspaces[0].DeviceID = "mac"
			m.Connections = []app.DeviceConnection{{ID: "air", DeviceID: "peer"}}
			if mode == "remote" {
				m.Snapshot.Workspaces[0].DeviceID = "peer"
			}
			key := m.DraftKey()
			m.Drafts[key] = "请检查"
			if mode == "cancelled-edit" {
				m.QueueEdits["s"] = app.QueueEdit{ID: "queued", Displaced: "ordinary"}
			}
			c := app.NewController("")
			v := New(m, c, filepath.Join(t.TempDir(), "client.json"))
			defer v.Saver.Close()
			file := filepath.Join(t.TempDir(), "设计 \"draft\".pdf")
			if err := os.WriteFile(file, []byte("%PDF-1.7\n\x00binary"), 0600); err != nil {
				t.Fatal(err)
			}
			v.addAttachmentFiles(key, []string{file})
			// A result must append to the latest text, not restore a captured draft.
			m.Drafts[key] = "请检查此文件"
			if mode == "changed-device" {
				m.Snapshot.Workspaces[0].DeviceID = "peer"
			}
			if mode == "cancelled-edit" {
				m.CancelQueueEdit("s")
			}
			select {
			case update := <-c.Updates:
				update(m)
			case <-time.After(3 * time.Second):
				t.Fatal("file preparation did not finish")
			}
			if len(m.Attachments[key]) != 0 {
				t.Fatal("unsupported file became a content attachment")
			}
			if mode == "local" {
				path, err := strconv.Unquote(strings.TrimPrefix(m.Drafts[key], "请检查此文件\n"))
				if err != nil || path != file || m.Error != "" {
					t.Fatalf("missing local path: draft=%q error=%q", m.Drafts[key], m.Error)
				}
			} else {
				want := "请检查此文件"
				if mode == "cancelled-edit" {
					want = "ordinary"
				}
				if m.Drafts[key] != want {
					t.Fatalf("local path leaked into another context: %q", m.Drafts[key])
				}
				if mode == "remote" && (!strings.Contains(m.Error, "远程") || !strings.Contains(m.Error, "路径")) {
					t.Fatalf("remote rejection does not explain inaccessible local path: %q", m.Error)
				}
			}
		})
	}
}

func TestLocalAttachmentPathPreservesSupportedFilesAndReadFailures(t *testing.T) {
	m := layoutModel()
	key := m.DraftKey()
	m.Drafts[key] = "检查"
	c := app.NewController("")
	v := New(m, c, filepath.Join(t.TempDir(), "client.json"))
	defer v.Saver.Close()
	dir := t.TempDir()
	binary, text, missing := filepath.Join(dir, "archive.zip"), filepath.Join(dir, "notes.txt"), filepath.Join(dir, "missing.zip")
	if err := os.WriteFile(binary, []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(text, []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}
	v.addAttachmentFiles(key, []string{binary, text, missing, dir})
	select {
	case update := <-c.Updates:
		update(m)
	case <-time.After(3 * time.Second):
		t.Fatal("file preparation did not finish")
	}
	if len(m.Attachments[key]) != 1 || m.Attachments[key][0].Attachment.Name != "notes.txt" {
		t.Fatal("supported attachment lost", m.Attachments[key])
	}
	path, err := strconv.Unquote(strings.TrimPrefix(m.Drafts[key], "检查\n"))
	if err != nil || path != binary {
		t.Fatalf("wrong path fallback: %q", m.Drafts[key])
	}
	if !strings.Contains(m.Error, "missing.zip") || !strings.Contains(m.Error, "不支持目录") {
		t.Fatal("read failures lost", m.Error)
	}
	if err := (domain.Submission{Text: m.Drafts[key]}).ValidateContent(); err != nil {
		t.Fatal("path text cannot be submitted", err)
	}
}
