package managedagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
)

func TestManagedInstallExtractsPinnedRuntimeFromGzip(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("managed releases target Apple Silicon")
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "upstream/bin/unused", Typeflag: tar.TypeSymlink, Linkname: "../unused"}); err != nil {
		t.Fatal(err)
	}
	entry := "upstream/bin/runtime"
	if err := tarWriter.WriteHeader(&tar.Header{Name: entry, Mode: 0755, Size: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tarWriter, "runtime"); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive.Bytes())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive.Bytes()) }))
	defer server.Close()
	release := Release{name: "runtime", version: "1", executable: entry, url: server.URL + "/runtime.tar.gz", digest: hex.EncodeToString(digest[:]), size: int64(archive.Len())}
	state := t.TempDir()
	file, err := release.Ensure(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "runtime" || release.Find(state) != file {
		t.Fatalf("runtime installation is incomplete: %q, %v", data, err)
	}
}

func TestManagedInstallPublishesOnlyVerifiedBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("managed releases target Apple Silicon")
	}
	body := []byte("fixture")
	digest := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, string(body)) }))
	defer server.Close()
	release := Release{name: "test", version: "1", executable: "fixture", url: server.URL, digest: hex.EncodeToString(digest[:]), size: int64(len(body))}
	state := t.TempDir()
	tampered := release
	tampered.digest = "wrong"
	if _, err := tampered.Ensure(context.Background(), state); err == nil || release.Find(state) != "" {
		t.Fatal("unverified artifact was published")
	}
	path, err := release.Ensure(context.Background(), state)
	if err != nil || release.Find(state) != path {
		t.Fatal(path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(body) {
		t.Fatal("installed bytes differ", err)
	}
}
