// Package managedagent installs pinned, device-owned agent binaries.
package managedagent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

type Release struct {
	name, version, url, digest, executable string
	size                                   int64
}

var Codex = Release{
	name: "codex", version: "0.172.0",
	url:    "https://github.com/openai/codex/releases/download/rust-v0.172.0/codex-package-aarch64-apple-darwin.tar.zst",
	digest: "475abc53c349ea802ddd9cb0db1376bef7b0553b6282c82e442e7aa0973cd9a5", size: 129157256,
	executable: "bin/codex",
}

var Claude = Release{
	name: "claude-code", version: "2.1.312",
	url:    "https://downloads.claude.ai/claude-code-releases/2.1.312/darwin-arm64/claude",
	digest: "6b5ec691195e551b02c41df9f7d723f4ea61eaa03c2df90acefe3736f1aeb620", size: 275988512,
	executable: "claude",
}

var ClaudeNode = Release{
	name: "claude-sdk-node", version: "22.23.5",
	url:    "https://nodejs.org/dist/v22.23.5/node-v22.23.5-darwin-arm64.tar.gz",
	digest: "e7131995759b61a0170b646010d2ad35a5d37b4cb0d471f3e8317eac6f742f3d", size: 50004868,
	executable: "node-v22.23.5-darwin-arm64/bin/node",
}

var ClaudeSDK = Release{
	name: "claude-agent-sdk", version: "0.3.312",
	url:    "https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.312.tgz",
	digest: "98a6c27b00b273fa0598b80540aa152d9fb58b6a1ac885b5993381e98469926d", size: 1410313,
	executable: "package/sdk.mjs",
}

var installs sync.Map

// EnsureClaudeToolchain reports a single download total across the missing
// components. A compatible device-installed CLI can be used with the SDK.
func EnsureClaudeToolchain(ctx context.Context, state string, includeCLI bool, report func(int64, int64)) error {
	releases := []Release{ClaudeNode, ClaudeSDK}
	if includeCLI {
		releases = append(releases, Claude)
	}
	pending := make([]Release, 0, len(releases))
	var total, completed int64
	for _, release := range releases {
		if release.Find(state) == "" {
			pending = append(pending, release)
			total += release.size
		}
	}
	for _, release := range pending {
		var progress func(int64, int64)
		if report != nil {
			progress = func(done, _ int64) { report(completed+done, total) }
		}
		if _, err := release.Ensure(ctx, state, progress); err != nil {
			return err
		}
		completed += release.size
		if report != nil {
			report(completed, total)
		}
	}
	return nil
}

func (r Release) directory(state string) string {
	return filepath.Join(state, "agent-runtimes", r.name, r.version)
}

func (r Release) Find(state string) string {
	if state == "" {
		return ""
	}
	dir := r.directory(state)
	for _, current := range []string{filepath.Dir(filepath.Dir(dir)), filepath.Dir(dir), dir} {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return ""
		}
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".verified"))
	if err != nil || string(marker) != r.digest {
		return ""
	}
	executable := filepath.Join(dir, r.executable)
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
		return ""
	}
	return executable
}

func (r Release) Ensure(ctx context.Context, state string, updates ...func(int64, int64)) (string, error) {
	var progress func(int64, int64)
	if len(updates) > 0 {
		progress = updates[0]
	}
	if state == "" || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return "", errors.New("此设备不支持托管安装")
	}
	target := r.directory(state)
	gate, _ := installs.LoadOrStore(target, make(chan struct{}, 1))
	lock := gate.(chan struct{})
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if executable := r.Find(state); executable != "" {
		return executable, nil
	}
	for _, dir := range []string{filepath.Dir(filepath.Dir(target)), filepath.Dir(target)} {
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", errors.New("无法创建托管运行环境目录")
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("托管运行环境目录不可用")
		}
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("同版本的托管运行环境未通过校验")
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".install-*")
	if err != nil {
		return "", errors.New("无法准备运行环境下载")
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, "download")
	if err := r.download(ctx, archive, progress); err != nil {
		return "", err
	}
	stagedExecutable := filepath.Join(stage, r.executable)
	switch {
	case strings.HasSuffix(r.url, ".tar.gz"), strings.HasSuffix(r.url, ".tgz"):
		if err := unpackGzipEntry(ctx, archive, r.executable, stagedExecutable); err != nil {
			return "", errors.New("无法解包托管运行环境")
		}
	case strings.HasSuffix(r.url, ".tar.zst"):
		if err := unpack(ctx, archive, stage); err != nil {
			return "", errors.New("无法解包托管运行环境")
		}
	default:
		if err := os.Rename(archive, stagedExecutable); err != nil {
			return "", err
		}
	}
	if err := os.Remove(archive); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	info, err := os.Lstat(stagedExecutable)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("运行环境缺少可执行文件")
	}
	if err := os.Chmod(stagedExecutable, 0700); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, ".verified"), []byte(r.digest), 0600); err != nil {
		return "", err
	}
	if err := os.Rename(stage, target); err != nil {
		return "", errors.New("无法启用托管运行环境")
	}
	return filepath.Join(target, r.executable), nil
}

func unpackGzipEntry(ctx context.Context, archive, entry, out string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for ctx.Err() == nil {
		header, err := reader.Next()
		if err != nil {
			return err
		}
		if header.Name != entry {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 256<<20 {
			return errors.New("invalid runtime archive member")
		}
		if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
			return err
		}
		target, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(target, reader)
		return errors.Join(err, target.Close())
	}
	return ctx.Err()
}

func (r Release) download(ctx context.Context, target string, progress func(int64, int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 8 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) > 5 || req.URL.Host != "release-assets.githubusercontent.com" && req.URL.Host != "downloads.claude.ai" {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("无法下载运行环境，请检查设备网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("运行环境下载失败")
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	var destination io.Writer = io.MultiWriter(f, hash)
	if progress != nil {
		progress(0, r.size)
		destination = &progressWriter{Writer: destination, total: r.size, report: progress}
	}
	size, err := io.Copy(destination, io.LimitReader(resp.Body, r.size+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || size != r.size || hex.EncodeToString(hash.Sum(nil)) != r.digest {
		return errors.New("运行环境下载不完整或未通过完整性校验")
	}
	return nil
}

type progressWriter struct {
	io.Writer
	total, done, last int64
	report            func(int64, int64)
}

func (w *progressWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	w.done += int64(n)
	if w.done-w.last >= max(1, w.total/100) || w.done == w.total {
		w.last = w.done
		w.report(min(w.done, w.total), w.total)
	}
	return n, err
}

func unpack(ctx context.Context, archive, destination string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file, zstd.WithDecoderMaxMemory(128<<20))
	if err != nil {
		return err
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(header.Name)
		if !filepath.IsLocal(name) || name == "." || header.Size < 0 || header.Size > (900<<20)-total {
			return errors.New("invalid archive member")
		}
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0700)
		case tar.TypeReg:
			total += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|os.FileMode(header.Mode)&0100)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, reader)
			err = errors.Join(copyErr, f.Close())
		default:
			return errors.New("unsupported archive member")
		}
		if err != nil {
			return err
		}
	}
}
