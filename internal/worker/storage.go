package worker

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strconv"
	"strings"
	"time"
)

func (s *Server) storage(clean bool) (domain.StorageReport, error) {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	out, err := s.attachments.Storage(time.Now(), clean, false)
	if err != nil {
		return out, err
	}
	root, err := os.OpenRoot(s.config.Directory)
	if err != nil {
		return out, err
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "attachments" && d.IsDir() {
			return fs.SkipDir
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := root.Lstat(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if !rotatedWorkerLog(name) {
			out.Usage.RetainedBytes += info.Size()
			return nil
		}
		if !clean {
			out.Usage.LogBytes += info.Size()
			return nil
		}
		if err := root.Remove(name); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				out.Usage.LogBytes += info.Size()
				if len(out.Failures) < 32 {
					out.Failures = append(out.Failures, domain.StorageFailure{Name: filepath.Base(name), Reason: "无法删除文件"})
				}
			}
		} else {
			out.FreedBytes += info.Size()
		}
		return nil
	})
	return out, err
}

func rotatedWorkerLog(name string) bool {
	if !strings.HasPrefix(name, "worker.log.") {
		return false
	}
	suffix := strings.TrimPrefix(name, "worker.log.")
	if number, err := strconv.Atoi(suffix); err == nil {
		return number > 0 && strconv.Itoa(number) == suffix
	}
	_, err := time.Parse("2006-01-02", suffix)
	return err == nil
}
