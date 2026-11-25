package managedagent

import (
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
