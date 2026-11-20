package worker

import (
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strconv"
	"strings"
	"time"
)

func (s *Server) storage(clean bool, refresh ...bool) (domain.StorageReport, error) {
	requested := time.Now()
	for !s.storageMu.TryLock() {
		select {
		case <-s.ctx.Done():
			return domain.StorageReport{}, s.ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	defer s.storageMu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return domain.StorageReport{}, err
	}
	if cached := s.storageReport; !clean && cached != nil &&
		(cached.MeasuredAt.After(requested) || (len(refresh) == 0 || !refresh[0]) && time.Since(cached.MeasuredAt) < 30*time.Second) {
		return *cached, nil
	}
	// A partial cleanup must never leave an earlier total available as current.
	if clean {
		s.storageReport = nil
	}
	out, err := s.attachments.StorageContext(s.ctx, time.Now(), clean, false)
	if err != nil {
		return out, err
	}
	root, err := os.OpenRoot(s.config.Directory)
	if err != nil {
		return out, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return out, err
	}
	defer dir.Close()
	err = walkStorageFiles(s.ctx, dir, "", func(name string, size int64) error {
		if !rotatedWorkerLog(name) {
			out.Usage.RetainedBytes += size
			return nil
		}
		if !clean {
			out.Usage.LogBytes += size
			return nil
		}
		if err := root.Remove(name); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				out.Usage.LogBytes += size
				if len(out.Failures) < 32 {
					out.Failures = append(out.Failures, domain.StorageFailure{Name: filepath.Base(name), Reason: "无法删除文件"})
				}
			}
		} else {
			out.FreedBytes += size
		}
		return nil
	})
	if err == nil {
		out.MeasuredAt = time.Now().UTC()
		cached := out
		cached.FreedBytes, cached.Failures = 0, nil
		s.storageReport = &cached
	}
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
