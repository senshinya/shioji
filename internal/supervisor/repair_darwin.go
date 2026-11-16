package supervisor

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"time"
)

// Admission holds h.mu only for binding the operation and acquiring the
// unchanged writer lease. Candidate I/O runs outside the control mutex.
func (h *host) beginRepair(req Request) (Operation, error) {
	refuse := func(message string) (Operation, error) {
		return Operation{}, &protocol.RemoteError{Code: "notSent", Message: message}
	}
	if !h.blocked || h.updating {
		return refuse("当前服务不需要冷修复或仍在切换")
	}
	previous, err := h.journal.current()
	if err != nil {
		return Operation{}, err
	}
	device := previous.DeviceID
	if device == "" {
		device = h.health.DeviceID
	}
	if req.DeviceID == "" || req.DeviceID != device || !req.Input.Explicit || !safeID(req.Input.ID) {
		return refuse("修复操作或设备身份未确认")
	}
	if existing, e := h.journal.load(req.Input.ID); e == nil {
		if existing.Input != req.Input {
			return refuse("修复标识已绑定其他操作")
		}
		return existing, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return Operation{}, e
	}
	if h.child != nil && managedexec.ProcessState(h.child.identity) == "running" {
		return refuse("候选执行服务尚未退出。请先停止本产品后台服务或重启设备，再重试更新；历史和备份会保留")
	}
	fd, err := unix.Open(filepath.Join(h.options.Directory, "owner.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return Operation{}, err
	}
	writer := os.NewFile(uintptr(fd), "cold repair writer lease")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		writer.Close()
		return refuse("原业务写入者仍在运行，请停止本产品后台服务或重启设备后重试")
	}
	op := Operation{Created: time.Now().UTC(), Input: req.Input, Phase: "preparing", Instance: h.instance, DeviceID: device, PreviousBuild: previous.Input.BuildID}
	if op.PreviousBuild == "" {
		op.PreviousBuild = h.health.BuildID
	}
	if err = h.journal.save(op); err != nil {
		writer.Close()
		return op, err
	}
	h.updating = true
	h.updateID = op.Input.ID
	ctx, cancel := context.WithTimeout(h.ctx, 8*time.Minute)
	h.cancelUpdate = cancel
	done := make(chan struct{})
	h.updateDone = done
	go func() { defer close(done); defer cancel(); defer writer.Close(); h.repair(ctx, op, writer) }()
	return op, nil
}

func (h *host) repair(ctx context.Context, op Operation, writer *os.File) {
	var candidate *child
	prepare := func() error {
		device, schema, err := store.ReadWorkerIdentity(ctx, h.options.Directory)
		if err != nil {
			return err
		}
		if device != op.DeviceID {
			return errors.New("原业务库设备身份已变化，保留原件后使用兼容安装器修复")
		}
		if err = h.verifyRepairScopes(ctx); err != nil {
			return err
		}
		path, build, err := h.options.Verify(ctx, op.Input.BuildID)
		if err != nil {
			return err
		}
		if build.BuildID != op.Input.BuildID || build.Lifecycle != 1 || build.Schema != schema {
			return errors.New("候选与原业务库格式不兼容，请使用对应的兼容安装器")
		}
		if err = probeCandidate(ctx, h.options.Directory, path, build); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = SaveDrainIntent(h.options.Directory, DrainIntent{ID: op.Input.ID, Instance: "cold-repair", DeviceID: op.DeviceID}); err != nil {
			return err
		}
		if err = h.phase(&op, "observing"); err != nil {
			return err
		}
		if err = writer.Close(); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		candidate, err = h.launch(path, build, true, op.Input.ID)
		if err != nil {
			return err
		}
		ready, err := h.ready(ctx, candidate, true)
		if err != nil {
			return err
		}
		if ready.DeviceID != op.DeviceID {
			return errors.New("修复候选设备身份与原件不一致")
		}
		if err = h.options.Select(build.BuildID); err != nil {
			return err
		}
		if err = h.phase(&op, "applied"); err != nil {
			return err
		}
		return h.resume(ctx, candidate, op.Input.ID)
	}
	err := prepare()
	if err != nil {
		if candidate != nil {
			err = errors.Join(err, h.stopExact(candidate, h.options.StopGrace))
		}
		op.Error = err.Error()
		err = errors.Join(err, h.phase(&op, "blocked"))
		_ = h.blockHealth(err)
	}
	h.mu.Lock()
	h.blocked = err != nil
	h.updating = false
	h.updateID = ""
	h.cancelUpdate = nil
	if err == nil {
		h.device = op.DeviceID
		h.readyDone = true
		h.health = serviceHealth{Phase: "ready", DeviceID: op.DeviceID, BuildID: candidate.build.BuildID, Schema: candidate.build.Schema}
		if e := h.saveHealth(); e != nil {
			h.blocked = true
			h.health.Phase, h.health.Error = "blocked", e.Error()
		}
	}
	h.mu.Unlock()
	h.signalWake()
}

func (h *host) verifyRepairScopes(ctx context.Context) error {
	for _, namespace := range []string{"executions", "title-executions"} {
		directory := filepath.Join(h.options.Directory, namespace)
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		manager, err := managedexec.New(directory, "")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() {
				continue
			}
			scope, e := manager.Existing(entry.Name())
			if e != nil {
				return errors.New("旧进程登记记录不可读取；请保留原件并用兼容安装器恢复一致备份")
			}
			if scope == nil {
				continue
			}
			if e = scope.Fence(); e != nil {
				return e
			}
			if e = scope.VerifyStopped(); e != nil {
				return errors.New("旧登记进程仍未退出，请重启执行设备后重试；不会按名称结束其他进程")
			}
		}
	}
	return nil
}
