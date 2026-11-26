package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
)

// Quota queries Antigravity usage via `agy --print /usage --output-format json`.
// Only the system default profile (profileID == "") is supported; custom GEMINI_HOME
// profiles are explicitly excluded.
func (a Adapter) Quota(ctx context.Context, _ *managedexec.Scope) (domain.QuotaSnapshot, error) {
	if a.profileID != "" {
		return domain.QuotaSnapshot{}, &harness.QuotaError{
			Message:     "Antigravity 仅支持系统默认 Profile 的额度查询",
			Unsupported: true,
		}
	}
	env, _ := a.environment(ctx)
	if ctx.Err() != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "Antigravity 额度查询超时"}
	}
	path, err := a.executable(env)
	if err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: err.Error()}
	}
	cmd := exec.Command(path, "--print", "/usage", "--output-format", "json")
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "无法启动 agy 额度探针：" + err.Error()}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		// Terminate the entire process group to prevent orphan agy child processes.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "Antigravity 额度查询超时"}
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		lower := strings.ToLower(detail)
		if strings.Contains(lower, "login") || strings.Contains(lower, "auth") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "credential") {
			return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "Antigravity 尚未登录或授权已失效", AuthUnavailable: true}
		}
		if detail == "" {
			detail = waitErr.Error()
		}
		if len(detail) > 160 {
			detail = detail[:160] + "…"
		}
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "Antigravity 额度查询失败：" + detail}
	}
	snap, err := parseAntigravityUsageJSON(stdout.Bytes(), time.Now().UTC())
	if err != nil {
		return domain.QuotaSnapshot{}, err
	}
	return snap, nil
}

type agyUsageOutput struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Command *struct {
		Name string `json:"name"`
		Data *struct {
			Groups []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Buckets []struct {
					ID                string   `json:"id"`
					Window            string   `json:"window"`
					Label             string   `json:"label"`
					RemainingFraction *float64 `json:"remaining_fraction"`
					RemainingPercent  *float64 `json:"remaining_percent"`
					UsedFraction      *float64 `json:"used_fraction"`
					UsedPercent       *float64 `json:"used_percent"`
					ResetTime         string   `json:"reset_time"`
				} `json:"buckets"`
			} `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

func parseAntigravityUsageJSON(raw []byte, now time.Time) (domain.QuotaSnapshot, error) {
	trimmed := bytes.TrimSpace(raw)
	// Some CLI builds print log lines before the JSON object; extract the JSON payload safely.
	if idx := bytes.IndexByte(trimmed, '{'); idx > 0 {
		trimmed = trimmed[idx:]
	}
	var out agyUsageOutput
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "Antigravity 额度数据解析失败"}
	}
	if out.Status != "" && !strings.EqualFold(out.Status, "SUCCESS") {
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = "Antigravity 额度查询返回状态 " + out.Status
		}
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: msg}
	}
	if out.Command == nil || out.Command.Data == nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "Antigravity 未返回额度数据"}
	}
	snap := domain.QuotaSnapshot{
		HarnessID: domain.QuotaKindAntigravity,
		Kind:      domain.QuotaKindAntigravity,
		Source:    domain.QuotaSourcePoll,
		FetchedAt: now,
	}
	for _, g := range out.Command.Data.Groups {
		var windows []domain.QuotaWindow
		for _, b := range g.Buckets {
			label := strings.TrimSpace(b.Window)
			if label == "" {
				label = strings.TrimSpace(b.Label)
			}
			w := domain.QuotaWindow{
				ID:    strings.TrimSpace(b.ID),
				Label: label,
			}
			switch {
			case b.RemainingFraction != nil && validFloat(*b.RemainingFraction):
				rem := clampPct(*b.RemainingFraction * 100)
				used := clampPct(100 - rem)
				w.RemainingPercent = &rem
				w.UsedPercent = &used
			case b.RemainingPercent != nil && validFloat(*b.RemainingPercent):
				rem := clampPct(*b.RemainingPercent)
				used := clampPct(100 - rem)
				w.RemainingPercent = &rem
				w.UsedPercent = &used
			case b.UsedFraction != nil && validFloat(*b.UsedFraction):
				used := clampPct(*b.UsedFraction * 100)
				rem := clampPct(100 - used)
				w.UsedPercent = &used
				w.RemainingPercent = &rem
			case b.UsedPercent != nil && validFloat(*b.UsedPercent):
				used := clampPct(*b.UsedPercent)
				rem := clampPct(100 - used)
				w.UsedPercent = &used
				w.RemainingPercent = &rem
			}
			if rt := strings.TrimSpace(b.ResetTime); rt != "" {
				if parsed, err := time.Parse(time.RFC3339, rt); err == nil {
					utc := parsed.UTC()
					w.ResetAt = &utc
				}
			}
			if w.RemainingPercent != nil || w.ResetAt != nil || w.Label != "" {
				windows = append(windows, w)
			}
		}
		if len(windows) > 0 {
			snap.Groups = append(snap.Groups, domain.QuotaWindowGroup{
				ID:      strings.TrimSpace(g.ID),
				Name:    strings.TrimSpace(g.Name),
				Windows: windows,
			})
		}
	}
	if len(snap.Groups) == 0 {
		return snap, errors.New("Antigravity 未返回任何模型分组额度")
	}
	return snap, nil
}

func validFloat(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
