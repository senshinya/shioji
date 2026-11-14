// shioji-update prepares release assets. It is build tooling, not bundled in
// the App or worker. Private keys are read only from the environment or files.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type manifest struct {
	Version   string `json:"version"`
	Notes     string `json:"notes,omitempty"`
	Date      string `json:"date"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: shioji-update keygen|check-key|package [options]")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := f.String("out", "", "Output directory")
	app := f.String("app", "", "Stapled Shioji.app")
	version := f.String("version", "", "Release version")
	base := f.String("base-url", "", "HTTPS release asset directory")
	notes := f.String("notes-file", "", "UTF-8 release notes file")
	pub := f.String("public-key", "", "Expected public key (public, not secret)")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "keygen":
		return keygen(*dir)
	case "check-key":
		_, err := signingKey(*pub)
		return err
	case "package":
		var text []byte
		if *notes != "" {
			var err error
			text, err = os.ReadFile(*notes)
			if err != nil {
				return err
			}
		}
		return publish(*app, *version, *base, string(text), *dir, *pub)
	}
	return errors.New("unknown update command")
}

func keygen(dir string) error {
	if dir == "" {
		return errors.New("keygen requires --out")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Preflight both names; never replace an installed update identity.
	for _, name := range []string{"update.key", "update.pub"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			return errors.New("update key files already exist or cannot be inspected")
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	for _, p := range []struct {
		name string
		data []byte
	}{{"update.key", priv}, {"update.pub", pub}} {
		f, err := os.OpenFile(filepath.Join(dir, p.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.WriteString(base64.StdEncoding.EncodeToString(p.data))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func signingKey(public string) (ed25519.PrivateKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("SHIOJI_UPDATE_PRIVATE_KEY")))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("SHIOJI_UPDATE_PRIVATE_KEY must contain an Ed25519 private key")
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(public))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("SHIOJI_UPDATE_PUBLIC_KEY must contain an Ed25519 public key")
	}
	// Derive from the seed too: a malformed 64-byte private key can embed a
	// public half inconsistent with its seed and generate unusable releases.
	canonical := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	if !canonical.Equal(ed25519.PrivateKey(key)) || !canonical.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(pub)) {
		return nil, errors.New("update signing key does not match the public key")
	}
	return canonical, nil
}

func publish(app, version, base, notes, out, public string) error {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		return errors.New("release version must be major.minor.patch")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("release asset directory must be an HTTPS URL")
	}
	key, err := signingKey(public)
	if err != nil {
		return err
	}
	if filepath.Base(app) != "Shioji.app" || out == "" {
		return errors.New("package requires Shioji.app and --out")
	}
	if st, err := os.Lstat(app); err != nil || !st.IsDir() {
		return errors.New("app bundle is missing")
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(out, ".update-archive-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	err = writeArchive(io.MultiWriter(f, h), app)
	if err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	st, err := os.Stat(f.Name())
	if err != nil {
		return err
	}
	// Keep every advertised archive immutable, including workflow reruns.
	name := fmt.Sprintf("Shioji-%s-darwin-arm64-%x.tar.gz", version, h.Sum(nil))
	m := manifest{Version: version, Notes: notes, Date: time.Now().UTC().Format(time.RFC3339), URL: strings.TrimRight(base, "/") + "/" + name, Size: st.Size(), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, h.Sum(nil)))}
	if err := os.Rename(f.Name(), filepath.Join(out, name)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "update-darwin-arm64.json"), append(data, '\n'), 0644)
}

func writeArchive(w io.Writer, app string) error {
	realApp, err := filepath.EvalSymlinks(app)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(p)
			if err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(realApp, resolved)
			if err != nil || filepath.IsAbs(link) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return errors.New("bundle contains a link outside the app")
			}
		} else if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("bundle contains an unsupported file")
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(app), p)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		tw.Close()
		gz.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}
