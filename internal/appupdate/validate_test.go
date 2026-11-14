package appupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCandidateIdentityVersionAndSystemAdmission(t *testing.T) {
	valid := bundleInfo{Identifier: "dev.shioji.app", Executable: "shioji", Version: "1.2.3", Build: "1.2.3", Minimum: "13.0"}
	if err := validateInfo(valid, "1.2.3", "15.0"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*bundleInfo){
		func(p *bundleInfo) { p.Identifier = "other.app" },
		func(p *bundleInfo) { p.Executable = "other" },
		func(p *bundleInfo) { p.Version = "1.2.2" },
		func(p *bundleInfo) { p.Build = "1.2.2" },
		func(p *bundleInfo) { p.Minimum = "99.0" },
		func(p *bundleInfo) { p.Minimum = "" },
		func(p *bundleInfo) { p.Minimum = "garbage" },
	} {
		p := valid
		change(&p)
		if validateInfo(p, "1.2.3", "15.0") == nil {
			t.Fatal("invalid candidate accepted", p)
		}
	}
}

func TestSignedArchiveCandidateReadOnly(t *testing.T) {
	bundle := os.Getenv("SHIOJI_VERIFY_RELEASE_APP")
	if bundle == "" {
		t.Skip("requires an existing signed release, never builds or launches an App")
	}
	version, err := exec.Command("/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(bundle, "Contents", "Info.plist")).Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(context.Background(), bundle, strings.TrimSpace(string(version)), os.Getenv("SHIOJI_VERIFY_RELEASE_TEAM")); err != nil {
		t.Fatal(err)
	}
}

func TestUnsignedBundleIsRejectedWithoutExecutingCandidate(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Shioji.app")
	dir := filepath.Join(bundle, "Contents", "MacOS")
	os.MkdirAll(dir, 0755)
	// A genuine Mach-O fixture, without running it or building the App.
	data, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "shioji"), data, 0755)
	plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>dev.shioji.app</string><key>CFBundleExecutable</key><string>shioji</string><key>CFBundleShortVersionString</key><string>1.2.3</string><key>CFBundleVersion</key><string>1.2.3</string><key>LSMinimumSystemVersion</key><string>13.0</string></dict></plist>`
	os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0644)
	if err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", bundle).Run(); err != nil {
		t.Fatal(err)
	}
	if err := Validate(context.Background(), bundle, "1.2.3", "ABCDEFGHIJ"); err == nil {
		t.Fatal("ad-hoc signed app was accepted as our Developer ID release")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Validate(ctx, bundle, "1.2.3", "ABCDEFGHIJ") == nil {
		t.Fatal("canceled validation succeeded")
	}
}
