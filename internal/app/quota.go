package app

import (
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

const (
	QuotaCodexInterval       = 60 * time.Second
	QuotaDeepSeekInterval    = 60 * time.Second
	QuotaAntigravityInterval = 3 * time.Minute
	QuotaBatchCeiling        = 45 * time.Second
)

var quotaBackoffSchedule = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
	300 * time.Second,
	15 * time.Minute,
}

// QuotaTarget identifies one cross-device Agent Profile selected for the local
// left-sidebar quota drawer. Stored in client-only state; never synced.
type QuotaTarget struct {
	ConnectionID string `json:"connectionId"`
	ProfileID    string `json:"profileId"`
}

func (t QuotaTarget) Normalized() QuotaTarget {
	if t.ConnectionID == "" {
		t.ConnectionID = "local"
	}
	return t
}

func (t QuotaTarget) Key() string {
	t = t.Normalized()
	return t.ConnectionID + "\x00" + t.ProfileID
}

// QuotaPanelPreferences persists only on the local client (never synced to workers).
type QuotaPanelPreferences struct {
	Targets   []QuotaTarget `json:"targets,omitempty"`
	ActiveTab string        `json:"activeTab,omitempty"`
}

func (p QuotaPanelPreferences) Normalized() QuotaPanelPreferences {
	out := QuotaPanelPreferences{ActiveTab: p.ActiveTab}
	seen := map[string]bool{}
	for _, t := range p.Targets {
		t = t.Normalized()
		if t.ProfileID == "" {
			continue
		}
		k := t.Key()
		if seen[k] {
			continue
		}
		seen[k] = true
		out.Targets = append(out.Targets, t)
	}
	if len(out.Targets) == 0 {
		out.ActiveTab = ""
	} else if len(out.Targets) == 1 {
		out.ActiveTab = out.Targets[0].Key()
	} else if out.ActiveTab != "" && out.ActiveTab != "all" && !seen[out.ActiveTab] {
		out.ActiveTab = "all"
	}
	return out
}

func (p QuotaPanelPreferences) Has(target QuotaTarget) bool {
	k := target.Key()
	for _, t := range p.Targets {
		if t.Key() == k {
			return true
		}
	}
	return false
}

// QuotaSeverity classifies collapsed dock & inactive tab alert indicators.
type QuotaSeverity uint8

const (
	QuotaSeverityNone QuotaSeverity = iota
	QuotaSeverityWarning
	QuotaSeverityCritical
)

// QuotaProfileState holds live cache, loading, error and backoff state for one target.
type QuotaProfileState struct {
	Snapshot            *domain.QuotaSnapshot
	Loading             bool
	Error               string
	Unsupported         bool
	AuthUnavailable     bool
	ConsecutiveFailures int
	LastAttemptAt       time.Time
	LastSuccessAt       time.Time
	NextRefreshAt       time.Time
	BackoffUntil        time.Time
}

// QuotaStore holds transient client-side quota observations across devices.
type QuotaStore struct {
	Entries map[string]*QuotaProfileState
}

func (m *Model) quotaStore() *QuotaStore {
	if m.Quota == nil {
		m.Quota = &QuotaStore{Entries: map[string]*QuotaProfileState{}}
	}
	if m.Quota.Entries == nil {
		m.Quota.Entries = map[string]*QuotaProfileState{}
	}
	return m.Quota
}

func (m *Model) QuotaState(target QuotaTarget) *QuotaProfileState {
	if m.Quota == nil || m.Quota.Entries == nil {
		return nil
	}
	return m.Quota.Entries[target.Key()]
}

func (m *Model) ensureQuotaState(target QuotaTarget) *QuotaProfileState {
	store := m.quotaStore()
	k := target.Key()
	st := store.Entries[k]
	if st == nil {
		st = &QuotaProfileState{}
		store.Entries[k] = st
	}
	return st
}

// QuotaRefreshInterval returns the scheduled polling interval for an Agent kind.
func QuotaRefreshInterval(kind string) time.Duration {
	switch kind {
	case domain.QuotaKindAntigravity:
		return QuotaAntigravityInterval
	case domain.QuotaKindDeepSeek:
		return QuotaDeepSeekInterval
	default:
		return QuotaCodexInterval
	}
}

// QuotaBackoffDelay computes the Lody/t3code exponential backoff, honoring a larger Retry-After.
func QuotaBackoffDelay(consecutiveFailures int, retryAfter time.Duration) time.Duration {
	if consecutiveFailures <= 0 {
		consecutiveFailures = 1
	}
	idx := consecutiveFailures - 1
	if idx >= len(quotaBackoffSchedule) {
		idx = len(quotaBackoffSchedule) - 1
	}
	delay := quotaBackoffSchedule[idx]
	if retryAfter > delay {
		return retryAfter
	}
	return delay
}

// BeginQuotaFetch marks target as loading (bypassing TTL/backoff when forced).
func (m *Model) BeginQuotaFetch(target QuotaTarget, now time.Time) {
	st := m.ensureQuotaState(target)
	st.Loading = true
	st.LastAttemptAt = now
}

// ApplyQuotaResult applies a runtime.quota RPC response or transport error,
// always preserving any existing cached Snapshot on failure.
func (m *Model) ApplyQuotaResult(target QuotaTarget, kind string, result protocol.QuotaResult, rpcErr error, now time.Time) {
	st := m.ensureQuotaState(target)
	st.Loading = false

	if rpcErr != nil {
		st.ConsecutiveFailures++
		st.Error = rpcErr.Error()
		st.Unsupported = false
		st.AuthUnavailable = false
		delay := QuotaBackoffDelay(st.ConsecutiveFailures, 0)
		st.BackoffUntil = now.Add(delay)
		st.NextRefreshAt = st.BackoffUntil
		return
	}

	if result.Error != "" || result.Snapshot == nil {
		msg := result.Error
		if msg == "" {
			msg = "未返回额度数据"
		}
		st.Error = msg
		st.Unsupported = result.Unsupported
		st.AuthUnavailable = result.AuthUnavailable
		if result.Unsupported {
			st.ConsecutiveFailures = 0
			st.BackoffUntil = time.Time{}
			st.NextRefreshAt = time.Time{}
			return
		}
		st.ConsecutiveFailures++
		retryAfter := time.Duration(result.RetryAfterSecs) * time.Second
		delay := QuotaBackoffDelay(st.ConsecutiveFailures, retryAfter)
		st.BackoffUntil = now.Add(delay)
		st.NextRefreshAt = st.BackoffUntil
		return
	}

	snap := result.Snapshot.Clone()
	// Do not let an older poll overwrite a newer real-time push snapshot.
	if st.Snapshot != nil && st.Snapshot.Version > 0 && snap.Version > 0 && snap.Version < st.Snapshot.Version {
		st.Error = ""
		st.Unsupported = false
		st.AuthUnavailable = false
		st.ConsecutiveFailures = 0
		st.BackoffUntil = time.Time{}
		return
	}
	if snap.Kind == "" {
		snap.Kind = kind
	}
	st.Snapshot = &snap
	st.Error = ""
	st.Unsupported = false
	st.AuthUnavailable = false
	st.ConsecutiveFailures = 0
	st.LastSuccessAt = now
	st.BackoffUntil = time.Time{}
	st.NextRefreshAt = now.Add(QuotaRefreshInterval(snap.Kind))
}

// ReceiveQuotaPush applies a real-time worker push snapshot (e.g. Codex account/rateLimits/updated).
func (m *Model) ReceiveQuotaPush(connectionID string, snap domain.QuotaSnapshot, now time.Time) {
	if snap.HarnessID == "" {
		return
	}
	target := QuotaTarget{ConnectionID: connectionID, ProfileID: snap.HarnessID}.Normalized()
	st := m.ensureQuotaState(target)
	if st.Snapshot != nil && st.Snapshot.Version > 0 && snap.Version > 0 && snap.Version < st.Snapshot.Version {
		return
	}
	copied := snap.Clone()
	st.Snapshot = &copied
	st.Error = ""
	st.Unsupported = false
	st.AuthUnavailable = false
	st.ConsecutiveFailures = 0
	st.LastSuccessAt = now
	st.BackoffUntil = time.Time{}
	st.NextRefreshAt = now.Add(QuotaRefreshInterval(copied.Kind))
}

// ToggleQuotaTarget adds or removes target from local-only panel preferences.
func (m *Model) ToggleQuotaTarget(target QuotaTarget) bool {
	target = target.Normalized()
	if target.ProfileID == "" {
		return false
	}
	p := m.QuotaPanel.Normalized()
	k := target.Key()
	idx := slices.IndexFunc(p.Targets, func(t QuotaTarget) bool { return t.Key() == k })
	if idx >= 0 {
		p.Targets = slices.Delete(p.Targets, idx, idx+1)
		if p.ActiveTab == k {
			if len(p.Targets) >= 2 {
				p.ActiveTab = "all"
			} else if len(p.Targets) == 1 {
				p.ActiveTab = p.Targets[0].Key()
			} else {
				p.ActiveTab = ""
			}
		}
		m.QuotaPanel = p.Normalized()
		return false
	}
	p.Targets = append(p.Targets, target)
	if len(p.Targets) == 1 {
		p.ActiveTab = k
	}
	m.QuotaPanel = p.Normalized()
	return true
}

// SetQuotaActiveTab updates the local-only active tab ("all" or QuotaTarget.Key()).
func (m *Model) SetQuotaActiveTab(tab string) {
	p := m.QuotaPanel.Normalized()
	p.ActiveTab = tab
	m.QuotaPanel = p.Normalized()
}

// QuotaDeviceCatalog describes one device and its eligible quota profiles.
type QuotaDeviceCatalog struct {
	ConnectionID  string
	DeviceName    string
	ShortName     string
	Connected     bool
	WorkerSupport bool
	Profiles      []QuotaProfileDescriptor
}

// QuotaProfileDescriptor describes one eligible Agent Profile on a device.
type QuotaProfileDescriptor struct {
	Target       QuotaTarget
	DeviceName   string
	ShortDevice  string
	Harness      domain.HarnessInfo
	Kind         string // "codex", "antigravity", "deepseek"
	Label        string // base profile display label
	System       bool
	Connected    bool
	WorkerLegacy bool
}

// QuotaCatalog enumerates all devices (local first, then saved SSH devices)
// and their eligible Agent Profiles strictly filtered by domain.SupportsQuota.
func (m *Model) QuotaCatalog() []QuotaDeviceCatalog {
	var out []QuotaDeviceCatalog
	addDevice := func(key, name string) {
		state := m.DeviceStates[key]
		connected := state != nil && state.Connected
		workerSupport := state == nil || state.WorkerMinor == 0 || state.WorkerMinor >= protocol.QuotaMinor
		var ready domain.Readiness
		if state != nil {
			ready = state.Readiness
		} else if key == "local" {
			ready = m.Readiness
		}
		short := shortDeviceLabel(name)
		cat := QuotaDeviceCatalog{
			ConnectionID:  key,
			DeviceName:    name,
			ShortName:     short,
			Connected:     connected,
			WorkerSupport: workerSupport,
		}
		for _, h := range ready.Harnesses {
			kind := domain.QuotaSupportKind(h)
			if kind == "" {
				continue
			}
			label := strings.TrimSpace(h.Name)
			if label == "" {
				label = h.ID
			}
			cat.Profiles = append(cat.Profiles, QuotaProfileDescriptor{
				Target:       QuotaTarget{ConnectionID: key, ProfileID: h.ID},
				DeviceName:   name,
				ShortDevice:  short,
				Harness:      h,
				Kind:         kind,
				Label:        label,
				System:       h.Profile == nil,
				Connected:    connected,
				WorkerLegacy: connected && !workerSupport,
			})
		}
		out = append(out, cat)
	}
	addDevice("local", m.LocalName())
	for _, conn := range m.Connections {
		if conn.ID == "" || conn.ID == "local" {
			continue
		}
		name := strings.TrimSpace(conn.Name)
		if name == "" {
			name = conn.ID
		}
		addDevice(conn.ID, name)
	}
	return out
}

// ResolveQuotaProfile looks up a selected target in the current fleet catalog,
// gracefully synthesizing a placeholder descriptor if the device is offline or not yet loaded.
func (m *Model) ResolveQuotaProfile(target QuotaTarget) QuotaProfileDescriptor {
	target = target.Normalized()
	for _, dev := range m.QuotaCatalog() {
		if dev.ConnectionID != target.ConnectionID {
			continue
		}
		for _, p := range dev.Profiles {
			if p.Target.ProfileID == target.ProfileID {
				return p
			}
		}
		kind := ""
		if st := m.QuotaState(target); st != nil && st.Snapshot != nil {
			kind = st.Snapshot.Kind
		}
		if kind == "" && (target.ProfileID == domain.QuotaKindCodex || target.ProfileID == domain.QuotaKindAntigravity || target.ProfileID == domain.QuotaKindDeepSeek) {
			kind = target.ProfileID
		}
		return QuotaProfileDescriptor{
			Target:       target,
			DeviceName:   dev.DeviceName,
			ShortDevice:  dev.ShortName,
			Kind:         kind,
			Label:        fallbackProfileLabel(target.ProfileID, kind),
			System:       target.ProfileID == domain.QuotaKindCodex || target.ProfileID == domain.QuotaKindAntigravity,
			Connected:    dev.Connected,
			WorkerLegacy: dev.Connected && !dev.WorkerSupport,
		}
	}
	name := target.ConnectionID
	if name == "local" {
		name = m.LocalName()
	}
	kind := ""
	if st := m.QuotaState(target); st != nil && st.Snapshot != nil {
		kind = st.Snapshot.Kind
	}
	return QuotaProfileDescriptor{
		Target:      target,
		DeviceName:  name,
		ShortDevice: shortDeviceLabel(name),
		Kind:        kind,
		Label:       fallbackProfileLabel(target.ProfileID, kind),
		Connected:   false,
	}
}

// DueQuotaTargets returns selected targets that are eligible and due for a scheduled poll.
func (m *Model) DueQuotaTargets(now time.Time) []QuotaProfileDescriptor {
	prefs := m.QuotaPanel.Normalized()
	var due []QuotaProfileDescriptor
	for _, target := range prefs.Targets {
		desc := m.ResolveQuotaProfile(target)
		if !desc.Connected || desc.WorkerLegacy {
			continue
		}
		st := m.QuotaState(target)
		if st != nil {
			if st.Loading || st.Unsupported {
				continue
			}
			if !st.BackoffUntil.IsZero() && now.Before(st.BackoffUntil) {
				continue
			}
			if !st.NextRefreshAt.IsZero() && now.Before(st.NextRefreshAt) {
				continue
			}
		}
		due = append(due, desc)
	}
	return due
}

// TargetSeverity evaluates alert severity for one target (used for tab dots and collapsed badge).
func (m *Model) TargetSeverity(desc QuotaProfileDescriptor) QuotaSeverity {
	st := m.QuotaState(desc.Target)
	if !desc.Connected {
		if st != nil && st.Snapshot != nil {
			return QuotaSeverityWarning
		}
		return QuotaSeverityWarning
	}
	if desc.WorkerLegacy {
		return QuotaSeverityWarning
	}
	sev := QuotaSeverityNone
	if st == nil {
		return sev
	}
	if st.AuthUnavailable {
		return QuotaSeverityCritical
	}
	if st.Error != "" {
		sev = max(sev, QuotaSeverityWarning)
	}
	if snap := st.Snapshot; snap != nil {
		if snap.SpendControlReached || (snap.OrdinaryUsageAllowed != nil && !*snap.OrdinaryUsageAllowed) {
			return QuotaSeverityCritical
		}
		if snap.Balance != nil && !snap.Balance.Available {
			return QuotaSeverityCritical
		}
		if minRem, ok := SnapshotMinRemainingPercent(*snap); ok {
			if minRem <= 5 {
				return QuotaSeverityCritical
			}
			if minRem <= 20 {
				sev = max(sev, QuotaSeverityWarning)
			}
		}
	}
	return sev
}

// OverallQuotaSeverity returns the highest alert severity across all selected targets.
func (m *Model) OverallQuotaSeverity() QuotaSeverity {
	prefs := m.QuotaPanel.Normalized()
	sev := QuotaSeverityNone
	for _, target := range prefs.Targets {
		s := m.TargetSeverity(m.ResolveQuotaProfile(target))
		if s > sev {
			sev = s
		}
	}
	return sev
}

// SnapshotMinRemainingPercent finds the tightest window remaining percentage across
// direct windows and groups, returning (_, false) when no percentage windows exist.
func SnapshotMinRemainingPercent(snap domain.QuotaSnapshot) (float64, bool) {
	found := false
	minVal := 100.0
	check := func(w domain.QuotaWindow) {
		if rem, ok := w.EffectiveRemainingPercent(); ok {
			if !found || rem < minVal {
				minVal = rem
				found = true
			}
		}
	}
	for _, w := range snap.Windows {
		check(w)
	}
	for _, g := range snap.Groups {
		for _, w := range g.Windows {
			check(w)
		}
	}
	if snap.SpendLimit != nil && snap.SpendLimit.RemainingPercent != nil {
		rem := float64(*snap.SpendLimit.RemainingPercent)
		if !found || rem < minVal {
			minVal = rem
			found = true
		}
	}
	return minVal, found
}

// TightestWindow returns the window with the lowest remaining percentage.
func SnapshotTightestWindow(snap domain.QuotaSnapshot) (domain.QuotaWindow, string, bool) {
	var best domain.QuotaWindow
	bestGroup := ""
	bestVal := 101.0
	found := false
	check := func(w domain.QuotaWindow, group string) {
		rem, ok := w.EffectiveRemainingPercent()
		if !ok {
			return
		}
		if !found || rem < bestVal {
			best = w
			bestGroup = group
			bestVal = rem
			found = true
		}
	}
	for _, w := range snap.Windows {
		check(w, "")
	}
	for _, g := range snap.Groups {
		for _, w := range g.Windows {
			check(w, g.Name)
		}
	}
	return best, bestGroup, found
}

// QuotaTabLabels assigns concise tab labels to selected targets, automatically
// appending a short device suffix whenever two targets share the same base label.
func QuotaTabLabels(profiles []QuotaProfileDescriptor) []string {
	counts := map[string]int{}
	for _, p := range profiles {
		counts[p.Label]++
	}
	labels := make([]string, len(profiles))
	for i, p := range profiles {
		if counts[p.Label] > 1 && p.ShortDevice != "" {
			labels[i] = p.Label + "·" + p.ShortDevice
		} else {
			labels[i] = p.Label
		}
	}
	return labels
}

func shortDeviceLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if idx := strings.LastIndexAny(name, " -_·"); idx > 0 && idx < len(name)-1 {
		tail := strings.TrimSpace(name[idx+1:])
		if len([]rune(tail)) >= 2 && len([]rune(tail)) <= 10 {
			return tail
		}
	}
	runes := []rune(name)
	if len(runes) > 8 {
		return string(runes[:8])
	}
	return name
}

func fallbackProfileLabel(profileID, kind string) string {
	switch kind {
	case domain.QuotaKindCodex:
		return "Codex"
	case domain.QuotaKindAntigravity:
		return "Antigravity"
	case domain.QuotaKindDeepSeek:
		return "DeepSeek"
	}
	if profileID != "" {
		return profileID
	}
	return "Profile"
}
