// Package runtimebuild identifies installed Shioji service artifacts.
package runtimebuild

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
	"shioji/internal/domain"
	"strconv"
	"sync"
)

// Set once for both executables by the packaging build.
var BuiltAt string

func Digest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var current = sync.OnceValue(func() domain.WorkerBuild {
	var info domain.WorkerBuild
	if path, err := os.Executable(); err == nil {
		info.BuildID, _ = Digest(path)
	}
	info.BuiltAt, _ = strconv.ParseInt(BuiltAt, 10, 64)
	dirty := false
	if b, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range b.Settings {
			if setting.Key == "vcs.revision" {
				info.Revision = setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				dirty = true
			}
		}
	}
	if dirty {
		info.Revision += "+modified"
	}
	return info
})

func Current() domain.WorkerBuild { return current() }
