package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
)

// InspectWorker runs only a signature-verified Shioji artifact's metadata path;
// it does not open the database or start a harness.
func InspectWorker(ctx context.Context, path string) (domain.WorkerBuild, error) {
	var none domain.WorkerBuild
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", path).Run(); err != nil {
		return none, fmt.Errorf("执行服务签名校验失败：%w", err)
	}
	data, err := exec.CommandContext(ctx, path, "--describe").Output()
	if err != nil {
		return none, errors.New("无法读取候选执行服务版本")
	}
	var f protocol.Frame
	if json.Unmarshal(data, &f) != nil || f.Type != "worker-build" || f.Major != protocol.Major || f.Worker == nil || !f.Worker.SafeUpdate {
		return none, errors.New("候选执行服务不支持本机安全更新")
	}
	digest, err := runtimebuild.Digest(path)
	if err != nil {
		return none, err
	}
	if f.Worker.BuildID != digest {
		return none, errors.New("执行服务在检查期间改变，请重试")
	}
	return *f.Worker, nil
}

func StageWorker(ctx context.Context, directory, binary string) (domain.WorkerBuild, error) {
	var none domain.WorkerBuild
	dir := filepath.Join(directory, "updates")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return none, err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return none, err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return none, errors.New("执行服务更新目录不安全")
	}
	source, err := os.Open(binary)
	if err != nil {
		return none, err
	}
	defer source.Close()
	f, err := os.CreateTemp(dir, ".worker-*")
	if err != nil {
		return none, err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), source)
	if err == nil {
		err = f.Chmod(0700)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return none, err
	}
	info, err := InspectWorker(ctx, f.Name())
	if err != nil {
		return none, err
	}
	if info.BuildID != hex.EncodeToString(h.Sum(nil)) {
		return none, errors.New("候选执行服务摘要不同")
	}
	targetDir := filepath.Join(dir, info.BuildID)
	if err = os.MkdirAll(targetDir, 0700); err != nil {
		return none, err
	}
	st, err = os.Lstat(targetDir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return none, errors.New("执行服务制品目录不安全")
	}
	target := filepath.Join(targetDir, "shioji-worker")
	if _, err = os.Lstat(target); os.IsNotExist(err) {
		if err = os.Rename(f.Name(), target); err != nil {
			return none, err
		}
	}
	return VerifyStagedWorker(ctx, directory, info.BuildID)
}

func StagedWorkerPath(directory, buildID string) (string, error) {
	digest, err := hex.DecodeString(buildID)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != buildID {
		return "", errors.New("执行服务构建标识无效")
	}
	return filepath.Join(directory, "updates", buildID, "shioji-worker"), nil
}
func VerifyStagedWorker(ctx context.Context, directory, buildID string) (domain.WorkerBuild, error) {
	path, err := StagedWorkerPath(directory, buildID)
	if err != nil {
		return domain.WorkerBuild{}, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return domain.WorkerBuild{}, err
	}
	if !st.Mode().IsRegular() {
		return domain.WorkerBuild{}, errors.New("执行服务制品必须是普通文件")
	}
	info, err := InspectWorker(ctx, path)
	if err == nil && info.BuildID != buildID {
		err = errors.New("暂存执行服务已改变")
	}
	return info, err
}
