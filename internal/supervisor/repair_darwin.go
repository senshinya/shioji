package supervisor

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"time"
)

// beginRepair is the compatible installer's cold path, not a force-close flow.
// It refuses a live writer or registered process and retains every old receipt.
// Caller holds h.mu and the fixed control lease throughout admission.
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
	if req.DeviceID == "" || req.DeviceID != previous.DeviceID || !req.Input.Explicit || !safeID(req.Input.ID) {
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
	defer writer.Close()
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return refuse("原业务写入者仍在运行，请停止本产品后台服务或重启设备后重试")
	}
	for _, namespace := range []string{"executions", "title-executions"} {
		directory := filepath.Join(h.options.Directory, namespace)
		entries, e := os.ReadDir(directory)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return Operation{}, e
		}
		manager, e := managedexec.New(directory, "")
		if e != nil {
			return Operation{}, e
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			scope, e := manager.Existing(entry.Name())
			if e != nil {
				return refuse("旧进程登记记录不可读取；请保留原件并用兼容安装器恢复一致备份")
			}
			if scope != nil {
				if e = scope.Fence(); e != nil {
					return Operation{}, e
				}
				if e = scope.VerifyStopped(); e != nil {
					return refuse("旧登记进程仍未退出，请重启执行设备后重试；不会按名称结束其他进程")
				}
			}
		}
	}
	path, build, err := h.options.Verify(h.ctx, req.Input.BuildID)
	if err != nil {
		return Operation{}, err
	}
	if build.Lifecycle != 1 {
		return refuse("候选不支持当前固定服务协议")
	}
	if h.child != nil && build.Schema != h.child.build.Schema {
		return refuse("候选存储格式不兼容，请使用对应的兼容安装器")
	}
	op := Operation{Created: time.Now().UTC(), Input: req.Input, Phase: "observing", Instance: h.instance, DeviceID: req.DeviceID, PreviousBuild: previous.Input.BuildID}
	if err = SaveDrainIntent(h.options.Directory, DrainIntent{ID: op.Input.ID, Instance: "cold-repair", DeviceID: op.DeviceID}); err != nil {
		return Operation{}, err
	}
	if err = h.journal.save(op); err != nil {
		return op, err
	}
	if err = writer.Close(); err != nil {
		return op, err
	}
	h.updating = true
	ctx, cancel := context.WithCancel(h.ctx)
	h.cancelUpdate = cancel
	done := make(chan struct{})
	h.updateDone = done
	go func() {
		defer close(done)
		defer cancel()
		candidate, e := h.launch(path, build, true, op.Input.ID)
		if e == nil {
			var ready Ready
			ready, e = h.ready(ctx, candidate, true)
			if e == nil && ready.DeviceID != op.DeviceID {
				e = errors.New("修复候选设备身份与原件不一致")
			}
		}
		if e == nil {
			e = h.options.Select(build.BuildID)
		}
		if e == nil {
			e = h.phase(&op, "applied")
		}
		if e == nil {
			e = h.resume(ctx, candidate, op.Input.ID)
		}
		if e != nil {
			op.Error = e.Error()
			_ = h.phase(&op, "blocked")
		}
		h.mu.Lock()
		h.blocked = e != nil
		h.updating = false
		h.cancelUpdate = nil
		if e == nil {
			h.device = op.DeviceID
			h.readyDone = true
		}
		h.mu.Unlock()
		h.signalWake()
	}()
	return op, nil
}
