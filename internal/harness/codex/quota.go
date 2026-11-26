package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
)

type codexRateLimitWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int64   `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

type codexCreditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

type codexSpendControlLimitSnapshot struct {
	Limit            string `json:"limit"`
	RemainingPercent *int   `json:"remainingPercent"`
	ResetsAt         *int64 `json:"resetsAt"`
	Used             string `json:"used"`
}

type codexRateLimitSnapshot struct {
	LimitID             *string                         `json:"limitId"`
	LimitName           *string                         `json:"limitName"`
	Primary             *codexRateLimitWindow           `json:"primary"`
	Secondary           *codexRateLimitWindow           `json:"secondary"`
	Credits             *codexCreditsSnapshot           `json:"credits"`
	IndividualLimit     *codexSpendControlLimitSnapshot `json:"individualLimit"`
	PlanType            *string                         `json:"planType"`
	SpendControlReached bool                            `json:"spendControlReached"`
}

type codexGetRateLimitsResponse struct {
	RateLimits           *codexRateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID  map[string]*codexRateLimitSnapshot `json:"rateLimitsByLimitId"`
	OrdinaryUsageAllowed *bool                              `json:"ordinaryUsageAllowed"`
	AccountID            *string                            `json:"accountId"`
}

type codexRateLimitsUpdatedParams struct {
	RateLimits *codexRateLimitSnapshot `json:"rateLimits"`
}

func (r *Runtime) harnessID() string {
	if r.profileID != "" {
		return r.profileID
	}
	if r.apiPreset != nil {
		return r.apiPreset.id
	}
	return domain.QuotaKindCodex
}

// ReadQuota queries Codex rate limits on a running app-server.
// Only subscription ("chatgpt") accounts are supported; API Key mode is skipped.
func (r *Runtime) ReadQuota(ctx context.Context) (domain.QuotaSnapshot, error) {
	if r.apiPreset != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "此 Agent 请通过适配器接口查询余额", Unsupported: true}
	}
	var account accountStatus
	if err := r.call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "无法读取 Codex 账号状态：" + err.Error()}
	}
	var public struct {
		Type     string `json:"type"`
		PlanType string `json:"planType"`
	}
	_ = json.Unmarshal(account.Account, &public)
	switch strings.ToLower(strings.TrimSpace(public.Type)) {
	case "chatgpt":
	case "apikey", "api_key", "api":
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "API Key 模式不支持额度查询", Unsupported: true}
	default:
		if account.RequiresOpenAIAuth || public.Type == "" {
			return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "尚未登录 Codex 订阅账号", AuthUnavailable: true}
		}
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "当前账号模式不支持额度查询", Unsupported: true}
	}
	if !r.supports("account/rateLimits/read") {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "当前 Codex 版本不支持额度查询", Unsupported: true}
	}
	var resp codexGetRateLimitsResponse
	if err := r.call(ctx, "account/rateLimits/read", map[string]any{}, &resp); err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "获取 Codex 额度失败：" + err.Error()}
	}
	if resp.RateLimits != nil {
		r.quotaMu.Lock()
		copied := cloneCodexRateLimitSnapshot(resp.RateLimits)
		r.lastRateLimits = copied
		r.quotaMu.Unlock()
	}
	snap := convertCodexRateLimits(r.harnessID(), resp, public.PlanType, domain.QuotaSourcePoll, time.Now().UTC())
	return snap, nil
}

func (r *Runtime) receiveRateLimitsUpdated(raw json.RawMessage) {
	if r.apiPreset != nil || r.onEvent == nil {
		return
	}
	var params codexRateLimitsUpdatedParams
	if err := json.Unmarshal(raw, &params); err != nil || params.RateLimits == nil {
		return
	}
	r.quotaMu.Lock()
	merged := mergeCodexRateLimitSnapshot(r.lastRateLimits, params.RateLimits)
	r.lastRateLimits = merged
	r.quotaMu.Unlock()
	planType := ""
	if r.account != nil {
		planType = r.account.Subscription
	}
	snap := convertCodexRateLimits(r.harnessID(), codexGetRateLimitsResponse{RateLimits: merged}, planType, domain.QuotaSourcePush, time.Now().UTC())
	// Best-effort non-fatal push notification: never kill the session runtime if ignored.
	_ = r.onEvent(domain.Event{Type: "quota.updated", Quota: &snap})
}

func mergeCodexRateLimitSnapshot(prev, next *codexRateLimitSnapshot) *codexRateLimitSnapshot {
	if next == nil {
		return cloneCodexRateLimitSnapshot(prev)
	}
	if prev == nil {
		return cloneCodexRateLimitSnapshot(next)
	}
	// If the update targets a different limitId, treat it as a fresh snapshot.
	if prev.LimitID != nil && next.LimitID != nil && *prev.LimitID != *next.LimitID {
		return cloneCodexRateLimitSnapshot(next)
	}
	out := cloneCodexRateLimitSnapshot(prev)
	if next.LimitID != nil {
		v := *next.LimitID
		out.LimitID = &v
	}
	if next.LimitName != nil {
		v := *next.LimitName
		out.LimitName = &v
	}
	if next.Primary != nil {
		w := *next.Primary
		out.Primary = &w
	}
	if next.Secondary != nil {
		w := *next.Secondary
		out.Secondary = &w
	}
	if next.Credits != nil {
		c := *next.Credits
		if next.Credits.Balance != nil {
			b := *next.Credits.Balance
			c.Balance = &b
		}
		out.Credits = &c
	}
	if next.IndividualLimit != nil {
		l := *next.IndividualLimit
		out.IndividualLimit = &l
	}
	if next.PlanType != nil {
		p := *next.PlanType
		out.PlanType = &p
	}
	out.SpendControlReached = next.SpendControlReached
	return out
}

func cloneCodexRateLimitSnapshot(s *codexRateLimitSnapshot) *codexRateLimitSnapshot {
	if s == nil {
		return nil
	}
	out := *s
	if s.LimitID != nil {
		v := *s.LimitID
		out.LimitID = &v
	}
	if s.LimitName != nil {
		v := *s.LimitName
		out.LimitName = &v
	}
	if s.Primary != nil {
		w := *s.Primary
		out.Primary = &w
	}
	if s.Secondary != nil {
		w := *s.Secondary
		out.Secondary = &w
	}
	if s.Credits != nil {
		c := *s.Credits
		if s.Credits.Balance != nil {
			b := *s.Credits.Balance
			c.Balance = &b
		}
		out.Credits = &c
	}
	if s.IndividualLimit != nil {
		l := *s.IndividualLimit
		out.IndividualLimit = &l
	}
	if s.PlanType != nil {
		p := *s.PlanType
		out.PlanType = &p
	}
	return &out
}

func convertCodexRateLimits(harnessID string, resp codexGetRateLimitsResponse, fallbackPlan, source string, now time.Time) domain.QuotaSnapshot {
	snap := domain.QuotaSnapshot{
		HarnessID:            harnessID,
		Kind:                 domain.QuotaKindCodex,
		Source:               source,
		FetchedAt:            now,
		PlanType:             fallbackPlan,
		OrdinaryUsageAllowed: resp.OrdinaryUsageAllowed,
	}
	primaryLimitID := ""
	if rl := resp.RateLimits; rl != nil {
		if rl.LimitID != nil {
			primaryLimitID = *rl.LimitID
		}
		if rl.PlanType != nil && strings.TrimSpace(*rl.PlanType) != "" {
			snap.PlanType = *rl.PlanType
		}
		snap.SpendControlReached = rl.SpendControlReached
		snap.Windows = codexWindowsFromSnapshot(rl)
		if rl.Credits != nil {
			bal := ""
			if rl.Credits.Balance != nil {
				bal = strings.TrimSpace(*rl.Credits.Balance)
			}
			if rl.Credits.HasCredits || rl.Credits.Unlimited || bal != "" {
				snap.Credits = &domain.QuotaCredits{
					HasCredits: rl.Credits.HasCredits,
					Unlimited:  rl.Credits.Unlimited,
					Balance:    bal,
				}
			}
		}
		if rl.IndividualLimit != nil {
			sl := &domain.QuotaSpendLimit{
				Used:  strings.TrimSpace(rl.IndividualLimit.Used),
				Limit: strings.TrimSpace(rl.IndividualLimit.Limit),
			}
			if rl.IndividualLimit.RemainingPercent != nil {
				v := *rl.IndividualLimit.RemainingPercent
				sl.RemainingPercent = &v
			}
			if rl.IndividualLimit.ResetsAt != nil && *rl.IndividualLimit.ResetsAt > 0 {
				t := time.Unix(*rl.IndividualLimit.ResetsAt, 0).UTC()
				sl.ResetAt = &t
			}
			snap.SpendLimit = sl
		}
	}
	if len(resp.RateLimitsByLimitID) > 0 {
		keys := make([]string, 0, len(resp.RateLimitsByLimitID))
		for k := range resp.RateLimitsByLimitID {
			if primaryLimitID != "" && k == primaryLimitID {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			extra := resp.RateLimitsByLimitID[k]
			if extra == nil {
				continue
			}
			windows := codexWindowsFromSnapshot(extra)
			if len(windows) == 0 {
				continue
			}
			name := k
			if extra.LimitName != nil && strings.TrimSpace(*extra.LimitName) != "" {
				name = strings.TrimSpace(*extra.LimitName)
			}
			snap.Groups = append(snap.Groups, domain.QuotaWindowGroup{
				ID:      k,
				LimitID: k,
				Name:    name,
				Windows: windows,
			})
		}
	}
	return snap
}

func codexWindowsFromSnapshot(rl *codexRateLimitSnapshot) []domain.QuotaWindow {
	if rl == nil {
		return nil
	}
	var out []domain.QuotaWindow
	if rl.Primary != nil {
		if w, ok := convertCodexWindow("primary", rl.Primary); ok {
			out = append(out, w)
		}
	}
	if rl.Secondary != nil {
		if w, ok := convertCodexWindow("secondary", rl.Secondary); ok {
			out = append(out, w)
		}
	}
	return out
}

func convertCodexWindow(id string, w *codexRateLimitWindow) (domain.QuotaWindow, bool) {
	if w == nil {
		return domain.QuotaWindow{}, false
	}
	out := domain.QuotaWindow{ID: id}
	if w.WindowDurationMins != nil && *w.WindowDurationMins > 0 {
		mins := *w.WindowDurationMins
		out.WindowMinutes = &mins
		out.Label = domain.FormatQuotaWindowMinutes(mins)
	}
	if w.UsedPercent != nil && !math.IsNaN(*w.UsedPercent) && !math.IsInf(*w.UsedPercent, 0) {
		used := clampPercent(*w.UsedPercent)
		rem := clampPercent(100 - used)
		out.UsedPercent = &used
		out.RemainingPercent = &rem
	}
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		t := time.Unix(*w.ResetsAt, 0).UTC()
		out.ResetAt = &t
	}
	if out.WindowMinutes == nil && out.RemainingPercent == nil && out.ResetAt == nil {
		return domain.QuotaWindow{}, false
	}
	return out, true
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (r *adapterRuntime) ReadQuota(ctx context.Context) (domain.QuotaSnapshot, error) {
	if r.titleAdapter.apiPreset != nil && r.titleAdapter.apiPreset.id == domain.QuotaKindDeepSeek {
		return r.titleAdapter.quotaDeepSeek(ctx)
	}
	if r.titleAdapter.responses {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "此 Agent 不支持额度查询", Unsupported: true}
	}
	return r.Runtime.ReadQuota(ctx)
}

func (c *catalogRuntime) ReadQuota(ctx context.Context) (domain.QuotaSnapshot, error) {
	if c.adapter.apiPreset != nil && c.adapter.apiPreset.id == domain.QuotaKindDeepSeek {
		return c.adapter.quotaDeepSeek(ctx)
	}
	if c.adapter.responses {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "此 Agent 不支持额度查询", Unsupported: true}
	}
	if c.adapter.profileID != "" && c.adapter.profileMode == domain.AgentProfileAPI {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "API Key 模式不支持额度查询", Unsupported: true}
	}
	p, err := c.acquire(ctx)
	if err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "无法启动 Codex 探测进程：" + err.Error()}
	}
	defer c.release(p)
	return p.runtime.ReadQuota(ctx)
}

// Quota implements harness.QuotaProvider for Codex (subscription mode) and DeepSeek (balance only).
func (a Adapter) Quota(ctx context.Context, scope *managedexec.Scope) (domain.QuotaSnapshot, error) {
	if a.responses {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "此 Agent 不支持额度查询", Unsupported: true}
	}
	if a.apiPreset != nil && a.apiPreset.id == domain.QuotaKindDeepSeek {
		return a.quotaDeepSeek(ctx)
	}
	if a.apiPreset != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "此 Agent 不支持额度查询", Unsupported: true}
	}
	if a.profileID != "" && a.profileMode == domain.AgentProfileAPI {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "API Key 模式不支持额度查询", Unsupported: true}
	}
	r, err := a.startNative(ctx, harness.StartOptions{Scope: scope})
	if err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "无法连接 Codex 探测进程：" + err.Error()}
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Close(closeCtx)
	}()
	return r.ReadQuota(ctx)
}

// DeepSeek balance query: GET <endpoint>/user/balance using credentials resolved
// strictly on the owning device. Returns balance-only data (no progress bar, no reset time).
func (a Adapter) quotaDeepSeek(ctx context.Context) (domain.QuotaSnapshot, error) {
	token, endpoint, err := a.resolveDeepSeekCredentials(ctx)
	if err != nil {
		return domain.QuotaSnapshot{}, err
	}
	if endpoint == "" {
		endpoint = a.apiPreset.endpoint
	}
	balanceURL := strings.TrimRight(endpoint, "/") + "/user/balance"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, balanceURL, nil)
	if err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "DeepSeek 余额接口地址无效"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "DeepSeek 余额查询超时"}
		}
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "无法连接 DeepSeek 余额接口"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "DeepSeek API 凭据无效或已失效", AuthUnavailable: true}
	}
	if resp.StatusCode != http.StatusOK {
		retryAfter := parseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now())
		msg := fmt.Sprintf("DeepSeek 余额查询失败（HTTP %d）", resp.StatusCode)
		if resp.StatusCode == http.StatusTooManyRequests {
			msg = "DeepSeek 请求过于频繁（429）"
		}
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: msg, RetryAfter: retryAfter}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "DeepSeek 余额响应过大或读取失败"}
	}
	var payload struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return domain.QuotaSnapshot{}, &harness.QuotaError{Message: "DeepSeek 余额响应解析失败"}
	}
	entries := make([]domain.QuotaBalanceEntry, 0, len(payload.BalanceInfos))
	for _, info := range payload.BalanceInfos {
		entries = append(entries, domain.QuotaBalanceEntry{
			Currency:        strings.TrimSpace(info.Currency),
			TotalBalance:    strings.TrimSpace(info.TotalBalance),
			GrantedBalance:  strings.TrimSpace(info.GrantedBalance),
			ToppedUpBalance: strings.TrimSpace(info.ToppedUpBalance),
		})
	}
	id := a.profileID
	if id == "" {
		id = domain.QuotaKindDeepSeek
	}
	return domain.QuotaSnapshot{
		HarnessID: id,
		Kind:      domain.QuotaKindDeepSeek,
		Source:    domain.QuotaSourcePoll,
		FetchedAt: time.Now().UTC(),
		Balance: &domain.QuotaBalance{
			Available: payload.IsAvailable,
			Entries:   entries,
		},
	}, nil
}

func (a Adapter) resolveDeepSeekCredentials(ctx context.Context) (token, endpoint string, err error) {
	env, _ := a.environment(ctx)
	if a.Home != "" {
		tok, envKey, baseURL := parseProviderFromConfigTOML(filepath.Join(a.Home, "config.toml"), a.apiPreset.id)
		if baseURL != "" {
			endpoint = baseURL
		}
		if tok != "" {
			return tok, endpoint, nil
		}
		if envKey != "" {
			if v := environmentValue(env, envKey); v != "" {
				return v, endpoint, nil
			}
		}
	}
	if v := environmentValue(env, "DEEPSEEK_API_KEY"); v != "" {
		return v, endpoint, nil
	}
	// Fallback: inspect via a short-lived app-server config/read if credentials live
	// in an inherited user layer or keyring.
	r, startErr := a.startNative(ctx, harness.StartOptions{})
	if startErr != nil {
		return "", endpoint, &harness.QuotaError{Message: "尚未配置 DeepSeek API 凭据", AuthUnavailable: true}
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Close(closeCtx)
	}()
	cfg, cfgErr := r.Runtime.systemConfiguration(ctx)
	if cfgErr != nil {
		return "", endpoint, &harness.QuotaError{Message: "无法读取 DeepSeek 配置", AuthUnavailable: true}
	}
	provider := cfg.provider(r.Runtime.processEnvironment())
	if provider.Endpoint != "" {
		endpoint = provider.Endpoint
	}
	tok := provider.Token
	if provider.EnvKey != "" {
		if v := environmentValue(r.Runtime.processEnvironment(), provider.EnvKey); v != "" {
			tok = v
		}
	}
	if tok == "" {
		tok, _ = cfg.nativeAPIKey(ctx)
	}
	if strings.TrimSpace(tok) == "" {
		return "", endpoint, &harness.QuotaError{Message: "尚未配置 DeepSeek API 凭据", AuthUnavailable: true}
	}
	return tok, endpoint, nil
}

// parseProviderFromConfigTOML extracts experimental_bearer_token, env_key and
// base_url from [model_providers.<providerID>] in Codex's config.toml without
// spawning a subprocess.
func parseProviderFromConfigTOML(path, providerID string) (token, envKey, baseURL string) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", ""
	}
	defer f.Close()
	targetSection := "model_providers." + providerID
	inSection := false
	scanner := bufio.NewScanner(io.LimitReader(f, 512<<10))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				inSection = false
				continue
			}
			header := strings.TrimSpace(line[1:end])
			// Strip optional quotes around dotted keys like [model_providers."deepseek"].
			header = strings.ReplaceAll(header, `"`, "")
			header = strings.ReplaceAll(header, `'`, "")
			inSection = header == targetSection
			continue
		}
		if !inSection {
			continue
		}
		key, rawVal, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val, ok := unquoteTOMLString(strings.TrimSpace(rawVal))
		if !ok {
			continue
		}
		switch key {
		case "experimental_bearer_token":
			token = val
		case "env_key":
			envKey = val
		case "base_url":
			baseURL = val
		}
	}
	return token, envKey, baseURL
}

func unquoteTOMLString(raw string) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	if raw[0] == '"' {
		// Find closing unescaped quote (strip trailing inline comments).
		escaped := false
		for i := 1; i < len(raw); i++ {
			if escaped {
				escaped = false
				continue
			}
			if raw[i] == '\\' {
				escaped = true
				continue
			}
			if raw[i] == '"' {
				s, err := strconv.Unquote(raw[:i+1])
				return s, err == nil
			}
		}
		return "", false
	}
	if raw[0] == '\'' {
		end := strings.IndexByte(raw[1:], '\'')
		if end >= 0 {
			return raw[1 : 1+end], true
		}
	}
	return "", false
}

func parseRetryAfterHeader(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
