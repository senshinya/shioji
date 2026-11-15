package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/portforward"
	"shioji/internal/protocol"
	"sync/atomic"
	"testing"
)

// URL deliberately cannot resolve on the controlling Mac. The authenticated
// owning-worker stream maps it to a real HTTP server and records the target.
func TestFetchURLUsesOwningWorkerAndSavesExactResponseBytes(t *testing.T) {
	data := []byte("report\x00bytes\xff")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/report?version=1" {
			t.Error("lost URL", r.URL)
		}
		w.Header().Set("Content-Disposition", `attachment; filename="report.pdf"`)
		w.Write(data)
	}))
	defer server.Close()
	dir, err := os.MkdirTemp("", "shioji-url-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	count := &atomic.Int64{}
	targets := make(chan portforward.Target, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				codec, epoch, err := protocol.Welcome(conn, "device-B")
				if err != nil {
					return
				}
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				if f.Method != protocol.TCPStreamMethod {
					return
				}
				var target portforward.Target
				if json.Unmarshal(f.Body, &target) != nil {
					return
				}
				targets <- target
				count.Add(1)
				peer, err := net.Dial("tcp", server.Listener.Addr().String())
				if err != nil {
					return
				}
				defer peer.Close()
				codec.Write(protocol.Frame{Type: "result", ID: f.ID, DeviceID: "device-B", Epoch: epoch})
				portforward.Relay(context.Background(), conn, peer)
			}()
		}
	}()
	target := AttachmentTarget{Directory: dir, DeviceID: "device-B", WorkspaceID: "w"}
	result, err := target.FetchURL(context.Background(), t.TempDir(), "http://owner-only.invalid:5173/report?version=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(result.Path)
	if err != nil || string(got) != string(data) {
		t.Fatal("wrong saved bytes", got, err)
	}
	if result.Resource.Name != "report.pdf" || result.Resource.Size != int64(len(data)) || len(result.SHA256) != 64 {
		t.Fatal("lost download metadata", result)
	}
	routed := <-targets
	if routed.Host != "owner-only.invalid" || routed.Port != 5173 || count.Load() != 1 {
		t.Fatal("wrong owning-device route", routed, count.Load())
	}
}

func TestHTTPDownloadRejectsErrorsAndCancellationWithoutPartialFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not found", http.StatusNotFound) }))
	defer server.Close()
	dir := t.TempDir()
	_, err := downloadLinkHTTP(context.Background(), dir, server.URL, http.DefaultClient, nil)
	if err == nil {
		t.Fatal("saved HTTP error page")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("left partial cache", entries)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = downloadLinkHTTP(ctx, dir, server.URL, http.DefaultClient, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("cancel left partial file")
	}
}

func TestHTTPDownloadRemovesTruncatedAndCancelledBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/short" {
			w.Header().Set("Content-Length", "100")
			w.Write([]byte("partial"))
			return
		}
		w.Write([]byte("downloaded bytes"))
	}))
	defer server.Close()
	dir := t.TempDir()
	if _, err := downloadLinkHTTP(context.Background(), dir, server.URL+"/short", http.DefaultClient, nil); err == nil {
		t.Fatal("accepted truncated response")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("truncation left partial file", entries)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := downloadLinkHTTP(ctx, dir, server.URL, http.DefaultClient, func(_ domain.FileResource, done int64) {
		if done > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("mid-transfer cancellation ignored", err)
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("cancel left downloaded bytes", entries)
	}
}
