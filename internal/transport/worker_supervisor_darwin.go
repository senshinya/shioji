package transport

import (
	"context"
	"errors"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/supervisor"
	"time"
)

// The signed fixed entry owns only service control. Every business writer is a
// separate child that must acquire the existing, never-replaced owner.lock.
func RunWorkerSupervisor(ctx context.Context, directory, initial, codex string, managed bool, install ...*supervisor.LegacyInstall) error {
	prepare, cancel := context.WithTimeout(ctx, 8*time.Minute)
	original, err := StageWorker(prepare, directory, initial)
	cancel()
	if err != nil {
		return err
	}
	args := []string{"--state-dir", directory, "--codex", codex}
	if managed {
		args = append(args, "--managed-service")
	}
	verify := func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
		if build == "" {
			var e error
			build, e = ActiveWorkerBuild(directory)
			if e != nil {
				return "", domain.WorkerBuild{}, e
			}
			if build == "" {
				build = original.BuildID
			}
		}
		info, e := VerifyStagedWorker(ctx, directory, build)
		if e != nil {
			return "", info, e
		}
		if info.Lifecycle != 1 {
			return "", info, errors.New("候选执行服务不支持固定服务控制协议")
		}
		path, e := StagedWorkerPath(directory, build)
		return path, info, e
	}
	path, err := StagedWorkerPath(directory, original.BuildID)
	if err != nil {
		return err
	}
	var legacy *supervisor.LegacyInstall
	if len(install) > 0 {
		legacy = install[0]
	}
	options := supervisor.Options{LegacyInstall: legacy, Directory: filepath.Clean(directory), InitialPath: path, Arguments: args, Verify: verify, Select: func(build string) error { return SaveActiveWorker(directory, build) }}
	for {
		err := supervisor.Run(ctx, options)
		if !managed || !errors.Is(err, supervisor.ErrOwned) {
			return err
		}
		// A registered login job may coexist with an already running manual fixed
		// entry. Wait as a relay, then take over after that actual owner exits.
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			lease, e := supervisor.AcquireLease(directory)
			if e == nil {
				lease.Close()
				break
			}
			if !errors.Is(e, supervisor.ErrOwned) {
				return e
			}
		}
	}
}
