package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"shioji/internal/domain"
)

const maxNativeConfig = 1 << 20

type nativeConfigDocument struct {
	revision string
	mode     os.FileMode
	fields   map[string]json.RawMessage
}

func readNativeConfig(base, name string) (nativeConfigDocument, error) {
	root, err := os.OpenRoot(base)
	if errors.Is(err, os.ErrNotExist) {
		return readNativeConfigFile(nil, name)
	}
	if err != nil {
		return nativeConfigDocument{}, errors.New("无法打开 Agent 配置目录")
	}
	defer root.Close()
	return readNativeConfigFile(root, name)
}

func readNativeConfigFile(root *os.Root, name string) (nativeConfigDocument, error) {
	doc := nativeConfigDocument{fields: map[string]json.RawMessage{}, mode: 0600, revision: hex.EncodeToString(sha256.New().Sum(nil))}
	if root == nil {
		return doc, nil
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return doc, errors.New("无法读取 Antigravity 原生配置")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxNativeConfig {
		return doc, errors.New("Antigravity 原生配置不是有效的普通文件")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxNativeConfig+1))
	if err != nil || len(raw) > maxNativeConfig {
		return doc, errors.New("无法读取 Antigravity 原生配置")
	}
	// AGY creates an empty file before the user adds any native settings.
	if len(raw) != 0 && (json.Unmarshal(raw, &doc.fields) != nil || doc.fields == nil) {
		return doc, errors.New("无法解析 Antigravity 原生配置")
	}
	digest := sha256.Sum256(raw)
	doc.revision, doc.mode = hex.EncodeToString(digest[:]), info.Mode().Perm()
	return doc, nil
}

func writeNativeConfig(ctx context.Context, root *os.Root, name string, doc nativeConfigDocument, maxMode os.FileMode) error {
	data, err := json.MarshalIndent(doc.fields, "", "  ")
	if err != nil || len(data) >= maxNativeConfig {
		return errors.New("Antigravity 原生配置过大或格式无效")
	}
	if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return errors.New("无法创建 Antigravity 原生配置目录")
	}
	pending := filepath.Join(filepath.Dir(name), ".shioji-config-"+domain.NewID())
	file, err := root.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, doc.mode&maxMode)
	if err != nil {
		return errors.New("无法写入 Antigravity 原生配置")
	}
	defer root.Remove(pending)
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return errors.New("无法写入 Antigravity 原生配置")
	}
	current, err := readNativeConfigFile(root, name)
	if err != nil {
		return err
	}
	if current.revision != doc.revision || current.mode != doc.mode {
		return errors.New("配置已变化，请刷新后重新编辑")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(pending, name); err != nil {
		return errors.New("无法替换 Antigravity 原生配置")
	}
	return nil
}

var projectNativeConfigDirectories = []string{".agents", ".agent", "_agents", "_agent"}

// Antigravity can inherit from Git parents. Only report parents that can be
// observed inside a directory already accepted by the owning worker.
func projectNativeConfigBases(launch domain.LaunchContext) []string {
	roots := append([]string{launch.CWD}, launch.AdditionalDirectories...)
	allowed := func(path string) bool {
		for _, root := range roots {
			if rel, err := filepath.Rel(root, path); filepath.IsAbs(root) && err == nil && filepath.IsLocal(rel) {
				return true
			}
		}
		return false
	}
	var result []string
	seen := map[string]bool{}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		ancestors := []string{root}
		for dir := root; allowed(dir); {
			if info, err := os.Lstat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
				for parent := root; parent != dir; {
					parent = filepath.Dir(parent)
					ancestors = append(ancestors, parent)
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		for _, dir := range ancestors {
			if !seen[dir] {
				seen[dir] = true
				result = append(result, dir)
			}
		}
	}
	return result
}
