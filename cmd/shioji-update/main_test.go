package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestKeygenNeverOverwritesAndPrivateKeyStaysPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	if err := keygen(dir); err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(dir, "update.key"))
	st, _ := os.Stat(filepath.Join(dir, "update.key"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("private key permissions", st.Mode())
	}
	if err := keygen(dir); err == nil {
		t.Fatal("existing signing identity overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "update.key"))
	if string(key) != string(after) {
		t.Fatal("existing key changed")
	}
}

func TestRebuildingReleaseKeepsPreviouslyAdvertisedArchive(t *testing.T) {
	root := t.TempDir()
	keys := filepath.Join(root, "keys")
	keygen(keys)
	key, _ := os.ReadFile(filepath.Join(keys, "update.key"))
	pub, _ := os.ReadFile(filepath.Join(keys, "update.pub"))
	t.Setenv("SHIOJI_UPDATE_PRIVATE_KEY", string(key))
	app := filepath.Join(root, "Shioji.app")
	os.MkdirAll(app, 0755)
	binary := filepath.Join(app, "binary")
	os.WriteFile(binary, []byte("first release"), 0755)
	out := filepath.Join(root, "out")
	if err := publish(app, "1.2.3", "https://example.com/v1.2.3", "", out, string(pub)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "update-darwin-arm64.json"))
	var first manifest
	json.Unmarshal(data, &first)
	u, _ := url.Parse(first.URL)
	oldPath := filepath.Join(out, filepath.Base(u.Path))
	old, _ := os.ReadFile(oldPath)
	os.WriteFile(binary, []byte("rebuilt release"), 0755)
	if err := publish(app, "1.2.3", "https://example.com/v1.2.3", "", out, string(pub)); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(out, "update-darwin-arm64.json"))
	var second manifest
	json.Unmarshal(data, &second)
	kept, _ := os.ReadFile(oldPath)
	if first.URL == second.URL || string(old) != string(kept) {
		t.Fatal("rebuild invalidated the already published signed archive")
	}
}

func TestPublishedArchiveHasMyGoCompatibleSignatureAndBundleLayout(t *testing.T) {
	root := t.TempDir()
	keys := filepath.Join(root, "keys")
	if err := keygen(keys); err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(keys, "update.key"))
	pub, _ := os.ReadFile(filepath.Join(keys, "update.pub"))
	t.Setenv("SHIOJI_UPDATE_PRIVATE_KEY", string(key))
	app := filepath.Join(root, "Shioji.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0755)
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "shioji"), []byte("signed app bytes"), 0755)
	os.Symlink("shioji", filepath.Join(app, "Contents", "MacOS", "relative-link"))
	out := filepath.Join(root, "out")
	if err := publish(app, "1.2.3", "https://github.com/senshinya/shioji/releases/download/v1.2.3", "Release notes", out, string(pub)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, "update-darwin-arm64.json"))
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	asset, err := url.Parse(m.URL)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(out, filepath.Base(asset.Path)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	signature, _ := base64.StdEncoding.DecodeString(m.Signature)
	public, _ := base64.StdEncoding.DecodeString(string(pub))
	if !ed25519.Verify(public, digest[:], signature) || m.Size != int64(len(archive)) || m.Version != "1.2.3" || m.Notes != "Release notes" || m.URL != "https://github.com/senshinya/shioji/releases/download/v1.2.3/Shioji-1.2.3-darwin-arm64-"+hex.EncodeToString(digest[:])+".tar.gz" {
		t.Fatal("invalid update manifest", m)
	}
	f, _ := os.Open(filepath.Join(out, filepath.Base(asset.Path)))
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	link := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "Shioji.app/Contents/MacOS/shioji" {
			body, _ := io.ReadAll(tr)
			found = string(body) == "signed app bytes" && h.Mode == 0755
		}
		if h.Typeflag == tar.TypeSymlink {
			link = h.Linkname == "shioji"
		}
	}
	if !found || !link {
		t.Fatal("bundle bytes, permissions or links lost")
	}
	if err := publish(app, "1.2.3", "https://example.com", "", filepath.Join(root, "mismatch"), base64.StdEncoding.EncodeToString(make([]byte, 32))); err == nil {
		t.Fatal("mismatching signing keys accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "mismatch", "update-darwin-arm64.json")); !os.IsNotExist(err) {
		t.Fatal("invalid keys published manifest")
	}
	if err := publish(app, "../bad", "https://example.com", "", out, string(pub)); err == nil {
		t.Fatal("invalid release version accepted")
	}
	if err := publish(app, "1.2.3", "http://example.com", "", out, string(pub)); err == nil {
		t.Fatal("insecure update URL accepted")
	}
}
