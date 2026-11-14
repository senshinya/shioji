package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func filePeer(t *testing.T, device string, data []byte, corrupt bool) (AttachmentTarget, *atomic.Int64) {
	t.Helper()
	directory, err := os.MkdirTemp("", "shioji-file-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	listener, err := net.Listen("unix", filepath.Join(directory, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	reads := &atomic.Int64{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				codec, epoch, err := protocol.Welcome(conn, device)
				if err != nil {
					return
				}
				meta := domain.FileResource{Path: "/device/review.mp4", Name: "review.mp4", MIME: "video/mp4", Size: int64(len(data)), Version: "revision"}
				for {
					var frame protocol.Frame
					if codec.Read(&frame) != nil {
						return
					}
					var q protocol.FileResourceQuery
					json.Unmarshal(frame.Body, &q)
					var out any = meta
					if frame.Method == "file.read" {
						reads.Add(1)
						end := min(int64(len(data)), q.Offset+64<<10)
						chunk := domain.FileResourceChunk{Resource: meta, Data: data[q.Offset:end], Offset: end, Complete: end == int64(len(data))}
						if corrupt {
							chunk.Offset++
						}
						out = chunk
					}
					frame.Type = "result"
					frame.Epoch = epoch
					frame.Body, _ = json.Marshal(out)
					if codec.Write(frame) != nil {
						return
					}
				}
			}()
		}
	}()
	return AttachmentTarget{Directory: directory, DeviceID: device, WorkspaceID: "workspace"}, reads
}
func TestLinkedFileDownloadsAboveAttachmentLimitAndSeparatesDevices(t *testing.T) {
	data := bytes.Repeat([]byte("media"), 2<<20)
	a, reads := filePeer(t, "device-a", data, false)
	b, _ := filePeer(t, "device-b", []byte("other-device"), false)
	cache := t.TempDir()
	var progress int64
	first, err := a.FetchFile(context.Background(), cache, "session", "/device/review.mp4", func(_ domain.FileResource, n int64) { progress = n })
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(first.Path)
	if err != nil || !bytes.Equal(saved, data) || progress != int64(len(data)) {
		t.Fatal("incomplete linked resource", err, progress)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = a.FetchFile(ctx, cache, "session", "/device/review.mp4", func(_ domain.FileResource, done int64) {
		if done == 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cache verification still completed", err)
	}
	calls := reads.Load()
	again, err := a.FetchFile(context.Background(), cache, "session", "/device/review.mp4", nil)
	if err != nil || first.Path != again.Path || reads.Load() != calls {
		t.Fatal("verified cache not reused", err)
	}
	other, err := b.FetchFile(context.Background(), cache, "session", "/device/review.mp4", nil)
	if err != nil || first.Path == other.Path {
		t.Fatal("same path on different devices collided", err)
	}
	saved, _ = os.ReadFile(other.Path)
	if string(saved) != "other-device" {
		t.Fatal("read controlling-device file instead of owning peer")
	}
	// A changed cache must be re-downloaded, even if its size is unchanged.
	os.WriteFile(first.Path, bytes.Repeat([]byte("x"), len(data)), 0600)
	if _, err = a.FetchFile(context.Background(), cache, "session", "/device/review.mp4", nil); err != nil || reads.Load() == calls {
		t.Fatal("corrupted cache reused", err)
	}
	report, err := LinkedFileStorage(cache, false)
	if err != nil || report.Usage.PreviewBytes < int64(len(data)) {
		t.Fatal("linked cache not measured", report, err)
	}
	report, err = LinkedFileStorage(cache, true)
	if err != nil || report.FreedBytes < int64(len(data)) {
		t.Fatal("linked cache not cleaned", report, err)
	}
	if _, err = os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatal("downloaded cache survived cleanup", err)
	}
}
func TestLinkedFileRejectsInvalidChunkAndCancels(t *testing.T) {
	target, _ := filePeer(t, "device", []byte("data"), true)
	cache := t.TempDir()
	if _, err := target.FetchFile(context.Background(), cache, "session", "file", nil); err == nil {
		t.Fatal("invalid chunk accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := target.FetchFile(ctx, cache, "session", "file", nil); err == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation ignored", err)
	}
	entries, _ := os.ReadDir(cache)
	for _, entry := range entries {
		files, _ := os.ReadDir(filepath.Join(cache, entry.Name()))
		for _, f := range files {
			if filepath.Ext(f.Name()) == ".part" {
				t.Fatal("incomplete download retained")
			}
		}
	}
}

func TestLinkedFileTargetUsesActiveDeviceRoute(t *testing.T) {
	m := NewModel()
	m.LocalDeviceID = "local-device"
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "w", DeviceID: "remote-device"}}
	selected := transport.SSH{Target: "user@100.64.0.9", TailnetPeer: "peer", Worker: "/worker"}
	m.Connections = []DeviceConnection{{ID: "remote", DeviceID: "remote-device", SSH: transport.SSH{Target: "old@192.168.1.1"}, ActiveRouteID: "chosen", Routes: []DeviceRoute{{ID: "chosen", SSH: selected}}}}
	target, err := m.AttachmentTarget("/local-worker", "w")
	if err != nil || target.SSH == nil || target.SSH.Target != selected.Target || target.SSH.TailnetPeer != "peer" || target.DeviceID != "remote-device" {
		t.Fatal("file transfer ignored active owning-device route", target, err)
	}
}
