package worker

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"shioji/internal/managedexec"
	"shioji/internal/store"
	"shioji/internal/transport"
)

type recoveryConsent struct {
	Executions  map[string]string
	Generations map[string]string
}

func recoveryGenerations(dir string) (map[string]string, error) {
	manager, err := managedexec.New(filepath.Join(dir, "executions"), "")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(manager.Directory)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		scope, err := manager.Existing(entry.Name())
		if err != nil {
			return nil, err
		}
		if scope != nil {
			out[scope.Session] = scope.Generation
		}
	}
	return out, nil
}
func captureRecoveryConsent(ctx context.Context, dir string) (recoveryConsent, error) {
	out := recoveryConsent{Executions: map[string]string{}}
	var err error
	out.Generations, err = recoveryGenerations(dir)
	if err != nil {
		return out, err
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.sqlite")}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(200)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return out, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT id FROM sessions ORDER BY id")
	if err != nil {
		return out, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Executions[id], err = store.ExecutionConsentRef(db, id)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
func validateRecoveryConsent(ctx context.Context, dir string, snapshot recoveryPreviewRecord) error {
	if snapshot.Preview.ScopeUnknown {
		return nil
	} // The confirmation explicitly covers unobservable device-wide work.
	current, err := captureRecoveryConsent(ctx, dir)
	if err != nil {
		return errors.New("无法重新核对执行范围，请重新检查并确认设备全部环境")
	}
	if !reflect.DeepEqual(snapshot.Consent, current) {
		return errors.New("确认期间有新任务、轮次或运行环境变化，请重新检查并确认")
	}
	return nil
}
func confirmRecoveryScopes(dir, id string, snapshot recoveryPreviewRecord) error {
	generations, err := recoveryGenerations(dir)
	if err != nil {
		return err
	}
	manager, err := managedexec.New(filepath.Join(dir, "executions"), "")
	if err != nil {
		return err
	}
	for session, generation := range generations {
		expected, ok := snapshot.Consent.Generations[session]
		if !snapshot.Preview.ScopeUnknown && (!ok || expected != generation) {
			return errors.New("运行环境代次已变化，旧关闭不能确认新实例")
		}
		scope, err := manager.Existing(session)
		if err != nil {
			return err
		}
		if scope != nil {
			if err = scope.ConfirmExplicitExit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Startup admission and recovery serialization have separate lifetimes. The
// active reservation survives the startup-fence handoff through verification.
func recoveryReservation(dir string) (*transport.RecoveryFence, error) {
	if fence, err := transport.PendingDeviceRecovery(dir); err != nil || fence != nil {
		return fence, err
	}
	var active transport.RecoveryFence
	err := recoveryRead(dir, "active", &active)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if active.ID == "" || active.DeviceID == "" {
		return nil, errors.New("恢复保留记录不完整")
	}
	return &active, nil
}
