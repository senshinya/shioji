package antigravity

import (
	"context"
	"errors"
	"slices"

	"shioji/internal/domain"
)

const yoloPermission = "yolo"

var errPermissionRestart = errors.New("请等待当前会话空闲后退出 YOLO")

func addYOLOPermission(ready *domain.Readiness) {
	if ready.State == domain.RuntimeReady && !slices.Contains(ready.Permissions, yoloPermission) {
		ready.Permissions = append(ready.Permissions, yoloPermission)
		ready.PermissionChoices = append(ready.PermissionChoices, domain.OptionChoice{
			ID: yoloPermission, Name: "YOLO", Description: "自动通过工具审批", Live: true,
		})
	}
}

func (r *runtime) PermissionRequiresRestart(cfg domain.RunConfig) bool {
	return r.nativeYOLO && (cfg.Permission != yoloPermission || cfg.Plan)
}

func (r *runtime) ChangePermission(ctx context.Context, s domain.Session, permission string, accept func() error) error {
	r.direct.Lock()
	defer r.direct.Unlock()
	if !r.Observes(s) || !r.ExecutionReady() {
		return errors.New("所属运行环境已变化，权限未更改")
	}
	if r.PermissionRequiresRestart(domain.RunConfig{Permission: permission}) {
		return errPermissionRestart
	}
	if permission != yoloPermission {
		if err := r.setOption(ctx, "mode", permission); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := accept(); err != nil {
		return err
	}
	r.session.Config.Permission = permission
	if permission != yoloPermission {
		return nil
	}
	for id, pending := range r.requests {
		if pending.Request.Kind == "approval" {
			if err := r.resolveRequest(ctx, id, pending, permissionOutcome(pending.AllowOnce), pending.AllowOnce == ""); err != nil {
				return err
			}
		}
	}
	return nil
}
