package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"shioji/internal/domain"
)

func probeCandidate(ctx context.Context, directory, path string, build domain.WorkerBuild) error {
	dir, err := os.MkdirTemp(directory, ".probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return probeWorkerDatabase(ctx, dir, path, build)
}

func probeWorkerDatabase(ctx context.Context, dir, path string, build domain.WorkerBuild) error {
	command := exec.CommandContext(ctx, path, "--probe", "--state-dir", dir)
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}
	data, err := command.Output()
	if err != nil {
		return err
	}
	var actual domain.WorkerBuild
	if json.Unmarshal(data, &actual) != nil || actual.BuildID != build.BuildID || actual.Schema != build.Schema || actual.Lifecycle != 1 {
		return errors.New("isolated candidate probe did not match inspected build and storage format")
	}
	return nil
}
