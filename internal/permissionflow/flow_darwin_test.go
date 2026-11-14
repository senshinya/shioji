package permissionflow

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ebitengine/purego"
)

func TestMissingRuntimeDoesNotAuthorizeMainApp(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Shioji.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	f := newFlow(filepath.Join(app, "Contents", "MacOS", "shioji"), filepath.Join(root, "home"))
	if f.RuntimeAvailable() {
		t.Fatal("missing Runtime was offered for authorization")
	}
	err := f.Open(true)
	if err == nil || !strings.Contains(err.Error(), "Shioji Runtime.app") {
		t.Fatal("missing Runtime fell back to the main App", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "home/Library/Application Support/Shioji/Runtime/Shioji Runtime.app"), 0700); err != nil {
		t.Fatal(err)
	}
	if !f.RuntimeAvailable() {
		t.Fatal("installed Runtime cannot be selected")
	}
}

func TestMissingLibraryReportsFailureAndAllowsRetry(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Shioji.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	f := newFlow(filepath.Join(app, "Contents", "MacOS", "shioji"), t.TempDir())
	for range 2 {
		if err := f.Open(false); err == nil || !strings.Contains(err.Error(), "PermissionFlow") {
			t.Fatal("missing packaged library did not report a recoverable error", err)
		}
	}
	f.Close() // No library was loaded and no guide needs cleanup.
}

// Opt-in boundary check against the built Swift product, without opening a
// system page or requiring any permission. A non-main-thread call is rejected.
func TestSwiftBridgeRejectsOffMainThread(t *testing.T) {
	library := os.Getenv("SHIOJI_PERMISSION_FLOW_TEST_LIBRARY")
	if library == "" {
		t.Skip("requires the standalone Swift bridge build")
	}
	app := filepath.Join(t.TempDir(), "Fixture.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>dev.shioji.permissionflow.fixture</string><key>CFBundleExecutable</key><string>fixture</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	f := newFlow(filepath.Join(app, "Contents", "MacOS", "fixture"), t.TempDir())
	f.library = library
	var mainThread func() int32
	purego.RegisterLibFunc(&mainThread, purego.RTLD_DEFAULT, "pthread_main_np")
	var openOffMain func() error
	openOffMain = func() error {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if mainThread() != 0 {
			// Hold the main OS thread while another goroutine obtains a
			// different one; Go tests themselves can run on the main thread.
			result := make(chan error)
			go func() { result <- openOffMain() }()
			return <-result
		}
		return f.Open(false)
	}
	if err := openOffMain(); err == nil || !strings.Contains(err.Error(), "主线程") {
		t.Fatal("Swift bridge did not enforce the native UI thread", err)
	}
}
