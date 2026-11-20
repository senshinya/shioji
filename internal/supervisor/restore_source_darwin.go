package supervisor

import (
	"context"
	"errors"
	"fmt"
	"shioji/internal/store"
	"time"
)

// shutdown has already reaped old. Its admitted drain intent authorizes
// checking for a cold restart of the selected writer, never the upgrade.
func (h *host) restoreSourceWithoutDrain(ctx context.Context, op *Operation, old *child) error {
	intent, err := ReadDrainIntent(h.options.Directory, op.Input.ID)
	if err != nil || intent.Instance != old.instance || intent.DeviceID != op.DeviceID || intent.BuildID != old.build.BuildID {
		return errors.New("无法确认原执行服务已接纳本次排空")
	}
	lease, err := acquireDatabaseLease(h.options.Directory)
	if err != nil {
		return fmt.Errorf("无法确认原业务写入锁已释放：%w", err)
	}
	defer lease.Close()
	check, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	device, schema, err := store.ReadWorkerIdentity(check, h.options.Directory)
	if err != nil {
		return err
	}
	if device != op.DeviceID || schema != old.build.Schema {
		return errors.New("原执行设备身份或数据库格式已变化")
	}
	path, build, err := h.options.Verify(check, "")
	if err != nil {
		return err
	}
	if build.BuildID != old.build.BuildID || build.Schema != schema || build.Lifecycle != 1 {
		return errors.New("选中的签名执行服务与原版本不一致")
	}
	if err = h.verifyRepairScopes(check); err != nil {
		return err
	}
	if err = check.Err(); err != nil {
		return err
	}
	// Keep the failed upgrade receipt and selection durable before releasing
	// the writer lease. If the supervisor restarts here, normal startup can
	// load the selected source. Cold startup reconciles inputs and keeps queues
	// paused, so we intentionally do not pass the failed upgrade's resume ID.
	if err = h.phase(op, "failed"); err != nil {
		return err
	}
	if err = lease.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	restored, err := h.launch(path, build, false, "")
	if err == nil {
		var ready Ready
		ready, err = h.ready(ctx, restored, false)
		if err == nil && ready.DeviceID != device {
			err = errors.New("恢复后的执行服务设备身份不一致")
		}
	}
	if err != nil {
		if restored != nil {
			err = errors.Join(err, h.stopExact(restored, h.options.StopGrace))
		}
		return fmt.Errorf("原执行服务自动恢复失败：%w", err)
	}
	h.completeCommitted(restored, "", nil)
	return nil
}
