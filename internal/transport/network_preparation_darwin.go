package transport

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/tailnet"
)

type networkPreparationResult struct {
	Version     int
	Error       string
	NeedsAction bool
}

type networkUserActionError string

func (e networkUserActionError) Error() string        { return string(e) }
func (e networkUserActionError) Is(target error) bool { return target == tailnet.ErrUserAction }

func networkPreparationPath(directory, id string) (string, error) {
	if len(id) != 32 {
		return "", errors.New("网络准备操作标识无效")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", errors.New("网络准备操作标识无效")
	}
	return filepath.Join(directory, "prepare-"+id+".json"), nil
}

func writeNetworkPreparation(directory, id string, result networkPreparationResult) error {
	path, err := networkPreparationPath(directory, id)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(result)
	f, err := os.CreateTemp(directory, ".prepare-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	return os.Rename(f.Name(), path)
}

func CompleteNetworkPreparation(directory, id string, err error) error {
	r := networkPreparationResult{Version: 1}
	if err != nil {
		r.Error = err.Error()
		r.NeedsAction = errors.Is(err, tailnet.ErrUserAction)
	}
	return writeNetworkPreparation(directory, id, r)
}

func prepareNetworkDetached(ctx context.Context, directory, workerBinary, developmentHost string) error {
	productionDir, err := networkDefaultDirectory()
	if err != nil {
		return err
	}
	var bin string
	if directory == productionDir {
		bin, err = installNetworkRuntime(ctx, workerBinary)
	} else {
		bin = developmentHost
		if bin == "" {
			bin = filepath.Join(bundledRuntime(workerBinary), "Contents", "MacOS", "shioji-runtime")
		}
		bin, err = filepath.Abs(bin)
		if err == nil {
			err = inspectNetworkRuntime(ctx, bin)
		}
	}
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(directory, "prepare-*.json"))
	if err != nil {
		return err
	}
	pending := 0
	for _, path := range files {
		st, e := os.Lstat(path)
		if e != nil {
			continue
		}
		data, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		var result networkPreparationResult
		if time.Since(st.ModTime()) > 2*time.Minute {
			os.Remove(path)
		} else if len(data) > 4096 || json.Unmarshal(data, &result) != nil || result.Version != 1 {
			pending++
		}
	}
	if pending >= 16 {
		return networkUserActionError("网络服务正在准备，请稍后重试")
	}
	id := strings.ReplaceAll(domain.NewID(), "-", "")
	if err = writeNetworkPreparation(directory, id, networkPreparationResult{}); err != nil {
		return err
	}
	path, _ := networkPreparationPath(directory, id)
	cmd := exec.Command(bin, "--network-prepare", "--network-dir", directory, "--network-worker", workerBinary, "--network-candidate", bin, "--network-operation", id)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = ctx.Err(); err != nil {
		os.Remove(path)
		return err
	}
	if err = cmd.Start(); err != nil {
		os.Remove(path)
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		data, e := os.ReadFile(path)
		var result networkPreparationResult
		if e == nil && len(data) <= 4096 && json.Unmarshal(data, &result) == nil && result.Version == 1 {
			os.Remove(path)
			if result.Error != "" {
				if result.NeedsAction {
					return networkUserActionError(result.Error)
				}
				return errors.New(result.Error)
			}
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return networkUserActionError("网络服务仍在准备，请稍后重试")
		case <-done:
			// Read its final result once more; a process that exits without
			// reporting is not interpreted as successful preparation.
			done = nil
			data, e := os.ReadFile(path)
			if e != nil || json.Unmarshal(data, &result) != nil || result.Version != 1 {
				os.Remove(path)
				return errors.New("网络服务准备未完成，请重试")
			}
		case <-timer.C:
		}
		timer.Stop()
	}
}
