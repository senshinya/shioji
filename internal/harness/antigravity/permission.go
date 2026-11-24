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
			if err := r.autoApprove(ctx, id, pending, r.active); err != nil {
				return err
			}
		}
	}
	return nil
}

// Keep the native tool ID as the one source for later execution and output
// updates. Some ACP peers ask for permission before publishing a tool call.
func (r *runtime) autoApprove(ctx context.Context, key string, p nativeRequest, turn *logicalTurn) error {
	if err := r.reply(ctx, p.WireID, permissionOutcome(p.AllowOnce), nil); err != nil {
		return err
	}
	if p.AllowOnce != "" && turn != nil && !turn.interrupted && turn.turn.ID == p.Request.TurnID && p.ToolCall.ToolCallID != "" {
		if _, known := turn.tools[p.ToolCall.ToolCallID]; !known {
			if err := r.finishStream(turn); err != nil {
				return err
			}
			update := p.ToolCall
			update.Status = "pending"
			if update.Title == "" {
				update.Title = p.Request.Title
			}
			if err := r.updateTool(turn.tools, turn.turn.ID, update); err != nil {
				return err
			}
		}
	}
	if key != "" {
		return r.closeRequest(key, p, p.AllowOnce == "", p.AllowOnce != "")
	}
	return nil
}
