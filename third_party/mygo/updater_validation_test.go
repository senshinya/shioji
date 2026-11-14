//go:build darwin

package mygo

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/update"
)

func TestUpdateValidationRejectsBeforeStaging(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "new", "Shioji.app")
	os.MkdirAll(bundle, 0755)
	os.WriteFile(filepath.Join(bundle, "candidate"), []byte("new"), 0644)
	var archive bytes.Buffer
	if err := update.WriteArchive(&archive, filepath.Dir(bundle), []string{"Shioji.app"}); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	digest := sha256.Sum256(archive.Bytes())
	m := update.Manifest{Version: "1.2.3", URL: "archive", Size: int64(archive.Len()), Signature: update.Sign(key, digest[:])}
	target := filepath.Join(root, "Shioji.app")
	os.Mkdir(target, 0755)
	os.WriteFile(filepath.Join(target, "original"), []byte("old"), 0644)
	called := false
	Updater.SetValidator(func(ctx context.Context, candidate, version string) error {
		called = true
		if version != "1.2.3" || filepath.Base(candidate) != "Shioji.app" {
			t.Fatalf("wrong candidate: %s %s", candidate, version)
		}
		if _, err := os.Stat(filepath.Join(candidate, "candidate")); err != nil {
			t.Fatal(err)
		}
		return errors.New("wrong signing identity")
	})
	defer Updater.SetValidator(nil)
	get := func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archive.Bytes())), nil
	}
	err := installUpdate(context.Background(), get, m, base64.StdEncoding.EncodeToString(pub), "1.0.0", target, nil)
	if err == nil || !called {
		t.Fatal("candidate bypassed validation", err)
	}
	if data, _ := os.ReadFile(filepath.Join(target, "original")); string(data) != "old" {
		t.Fatal("running app was changed")
	}
	if pendingUpdate.Load() != nil {
		t.Fatal("rejected update was staged")
	}
	left, _ := filepath.Glob(filepath.Join(root, ".Shioji.app.update-*"))
	if len(left) != 0 {
		t.Fatal("rejected update left staging files", left)
	}
	called = false
	m.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	if err := installUpdate(context.Background(), get, m, base64.StdEncoding.EncodeToString(pub), "1.0.0", target, nil); err == nil || called {
		t.Fatal("unsigned archive reached validation")
	}
}

func TestUpdateDownloadRejectsInsecureRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://0.0.0.0:1/archive", http.StatusFound)
	}))
	defer server.Close()
	body, err := httpGet(context.Background(), server.URL)
	if body != nil {
		body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatal("update followed an insecure redirect", err)
	}
}

func TestAcceptedBundleWaitsForAppExitThenReplacesOnlyItsTarget(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "new", "Shioji.app")
	os.MkdirAll(bundle, 0755)
	os.WriteFile(filepath.Join(bundle, "candidate"), []byte("new"), 0644)
	var archive bytes.Buffer
	if err := update.WriteArchive(&archive, filepath.Dir(bundle), []string{"Shioji.app"}); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	digest := sha256.Sum256(archive.Bytes())
	m := update.Manifest{Version: "1.2.3", URL: "archive", Size: int64(archive.Len()), Signature: update.Sign(key, digest[:])}
	target := filepath.Join(root, "Shioji.app")
	os.Mkdir(target, 0755)
	os.WriteFile(filepath.Join(target, "original"), []byte("old"), 0644)
	other := filepath.Join(root, "worker-running")
	os.WriteFile(other, []byte("untouched"), 0644)
	// Stand in for an App PID. Never terminate or relaunch the real GUI.
	process := exec.Command("/bin/sleep", "60")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer process.Process.Kill()
	oldPID := appPid
	appPid = process.Process.Pid
	defer func() { appPid = oldPID }()
	Updater.SetValidator(func(context.Context, string, string) error { return nil })
	defer Updater.SetValidator(nil)
	get := func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archive.Bytes())), nil
	}
	if err := installUpdate(context.Background(), get, m, base64.StdEncoding.EncodeToString(pub), "1.0.0", target, nil); err != nil {
		t.Fatal(err)
	}
	staged := pendingUpdate.Load()
	if staged == nil {
		t.Fatal("valid update was not staged")
	}
	defer func() { staged.helper.Process.Kill(); pendingUpdate.Store(nil) }()
	if data, _ := os.ReadFile(filepath.Join(target, "original")); string(data) != "old" {
		t.Fatal("app replaced before exit")
	}
	process.Process.Kill()
	process.Wait()
	done := make(chan error, 1)
	go func() { done <- staged.helper.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("staging helper did not finish")
	}
	if data, _ := os.ReadFile(filepath.Join(target, "candidate")); string(data) != "new" {
		t.Fatal("verified update not installed after exit")
	}
	if data, _ := os.ReadFile(other); string(data) != "untouched" {
		t.Fatal("update changed unrelated worker state")
	}
}
