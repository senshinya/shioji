package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/systemexec"
)

func decodeNativeQuota(data []byte) ([]domain.RuntimeQuotaWindow, error) {
	var native struct {
		Status  string
		Turns   *int `json:"num_turns"`
		Command struct {
			Name string
			Data struct {
				Groups []struct {
					Name    string
					Buckets []struct {
						ID        string
						Window    string
						Remaining *float64 `json:"remaining_fraction"`
						Reset     string   `json:"reset_time"`
					}
				}
			}
		}
	}
	unavailable := errors.New("Antigravity 未提供额度窗口")
	if json.Unmarshal(data, &native) != nil || native.Status != "SUCCESS" || native.Turns == nil || *native.Turns != 0 ||
		native.Command.Name != "usage" || len(native.Command.Data.Groups) > 32 {
		return nil, unavailable
	}
	var result []domain.RuntimeQuotaWindow
	for _, group := range native.Command.Data.Groups {
		if group.Name == "" || len(group.Name) > 160 || len(group.Buckets) > 16 {
			continue
		}
		for _, bucket := range group.Buckets {
			duration := 0
			switch bucket.Window {
			case "5h":
				duration = 18000
			case "weekly":
				duration = 604800
			}
			if duration == 0 || bucket.ID == "" || bucket.Remaining == nil || *bucket.Remaining < 0 || *bucket.Remaining > 1 {
				continue
			}
			window := domain.RuntimeQuotaWindow{Group: group.Name, DurationSeconds: duration, UsedPercent: (1 - *bucket.Remaining) * 100}
			if reset, err := time.Parse(time.RFC3339, bucket.Reset); err == nil {
				window.ResetsAt = &reset
			}
			result = append(result, window)
		}
	}
	if len(result) == 0 {
		return nil, unavailable
	}
	return result, nil
}

type quotaOutput struct{ bytes.Buffer }

func (w *quotaOutput) Write(p []byte) (int, error) {
	if w.Len()+len(p) > 65536 {
		return 0, errors.New("quota output too large")
	}
	return w.Buffer.Write(p)
}

// Print mode uses the system profile. A custom ACP profile cannot be queried
// with these arguments without risking a reading from another account.
func queryNativeQuota(ctx context.Context, executable, version string, env []string) []domain.RuntimeQuotaWindow {
	if ctx.Err() != nil || !verifiedConcurrentPrompt(version) || systemexec.EnvironmentValue(env, "GEMINI_HOME") != "" {
		return nil
	}
	home := systemexec.EnvironmentValue(env, "HOME")
	if !filepath.IsAbs(home) {
		return nil
	}
	dir, err := os.MkdirTemp("", "shioji-agy-usage-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out := &quotaOutput{}
	cmd := exec.CommandContext(ctx, executable, "--print", "/usage", "--output-format", "json")
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, out, io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	if cmd.Run() != nil {
		return nil
	}
	windows, _ := decodeNativeQuota(out.Bytes())
	return windows
}
