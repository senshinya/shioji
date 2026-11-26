package worker

import (
	"context"
	"errors"
	"math"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

const (
	quotaCacheTTL     = 15 * time.Second
	quotaQueryTimeout = 20 * time.Second
)

type quotaFlight struct {
	done   chan struct{}
	result protocol.QuotaResult
}

type quotaEntry struct {
	snapshot *domain.QuotaSnapshot
	cachedAt time.Time
	version  int64
	inFlight *quotaFlight
}

func (s *Server) initQuotaState() {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.quotaEntries == nil {
		s.quotaEntries = map[string]*quotaEntry{}
	}
	if s.quotaWake == nil {
		s.quotaWake = make(chan struct{})
	}
}

func (s *Server) quotaEntryLocked(harnessID string) *quotaEntry {
	if s.quotaEntries == nil {
		s.quotaEntries = map[string]*quotaEntry{}
	}
	e := s.quotaEntries[harnessID]
	if e == nil {
		e = &quotaEntry{}
		s.quotaEntries[harnessID] = e
	}
	return e
}

func (s *Server) recordQuotaPush(harnessID string, snap domain.QuotaSnapshot) {
	if harnessID == "" {
		harnessID = snap.HarnessID
	}
	if harnessID == "" {
		return
	}
	now := time.Now().UTC()
	snap = snap.Clone()
	snap.HarnessID = harnessID
	snap.Source = domain.QuotaSourcePush
	if snap.FetchedAt.IsZero() {
		snap.FetchedAt = now
	}
	s.quotaMu.Lock()
	s.quotaVersion++
	snap.Version = s.quotaVersion
	entry := s.quotaEntryLocked(harnessID)
	entry.snapshot = &snap
	entry.cachedAt = now
	entry.version = snap.Version
	if s.quotaWake != nil {
		close(s.quotaWake)
		s.quotaWake = make(chan struct{})
	}
	s.quotaMu.Unlock()
}

func (s *Server) quotaUpdatesAfter(after int64) (int64, []domain.QuotaSnapshot) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	current := s.quotaVersion
	if current <= after {
		return current, nil
	}
	var updates []domain.QuotaSnapshot
	for _, entry := range s.quotaEntries {
		if entry.snapshot != nil && entry.version > after {
			updates = append(updates, entry.snapshot.Clone())
		}
	}
	return current, updates
}

func (s *Server) quotaWatchChannel(after int64) (<-chan struct{}, bool) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.quotaVersion > after {
		return nil, true
	}
	if s.quotaWake == nil {
		s.quotaWake = make(chan struct{})
	}
	return s.quotaWake, false
}

func (s *Server) readQuota(in protocol.QuotaQuery) (protocol.QuotaResult, error) {
	id := in.HarnessID
	if id == "" {
		id = s.config.DefaultHarnessID
	}
	adapter := s.adapter(id)
	if adapter == nil {
		return protocol.QuotaResult{Error: "未找到对应的 Agent Profile", Unsupported: true}, nil
	}
	if unsupported := s.quotaEligibilityError(id, adapter); unsupported != "" {
		return protocol.QuotaResult{Error: unsupported, Unsupported: true}, nil
	}
	provider, hasProvider := adapter.(harness.QuotaProvider)
	activeRuntime := s.activeQuotaRuntime(id)
	catalogRuntime := s.catalogQuotaRuntime(id)
	if !hasProvider && activeRuntime == nil && catalogRuntime == nil {
		return protocol.QuotaResult{Error: "此 Agent 不支持额度查询", Unsupported: true}, nil
	}

	s.quotaMu.Lock()
	entry := s.quotaEntryLocked(id)
	if !in.Force && entry.snapshot != nil && time.Since(entry.cachedAt) < quotaCacheTTL {
		copied := entry.snapshot.Clone()
		s.quotaMu.Unlock()
		return protocol.QuotaResult{Snapshot: &copied}, nil
	}
	if entry.inFlight != nil {
		flight := entry.inFlight
		s.quotaMu.Unlock()
		select {
		case <-flight.done:
			return cloneQuotaResult(flight.result), nil
		case <-s.ctx.Done():
			return protocol.QuotaResult{}, s.ctx.Err()
		}
	}
	startVersion := entry.version
	flight := &quotaFlight{done: make(chan struct{})}
	entry.inFlight = flight
	s.quotaMu.Unlock()

	var result protocol.QuotaResult
	defer func() {
		flight.result = result
		s.quotaMu.Lock()
		if entry.inFlight == flight {
			entry.inFlight = nil
		}
		s.quotaMu.Unlock()
		close(flight.done)
	}()

	ctx, cancel := context.WithTimeout(s.ctx, quotaQueryTimeout)
	defer cancel()

	var (
		snap domain.QuotaSnapshot
		err  error
	)
	switch {
	case activeRuntime != nil:
		snap, err = activeRuntime.ReadQuota(ctx)
	case catalogRuntime != nil:
		snap, err = catalogRuntime.ReadQuota(ctx)
	default:
		snap, err = provider.Quota(ctx, nil)
	}

	if err != nil {
		result = quotaErrorResult(err)
		return cloneQuotaResult(result), nil
	}

	now := time.Now().UTC()
	snap = snap.Clone()
	snap.HarnessID = id
	if snap.Source == "" {
		snap.Source = domain.QuotaSourcePoll
	}
	if snap.FetchedAt.IsZero() {
		snap.FetchedAt = now
	}

	s.quotaMu.Lock()
	// A real-time push received while polling wins over the older poll snapshot.
	if entry.version > startVersion && entry.snapshot != nil {
		copied := entry.snapshot.Clone()
		s.quotaMu.Unlock()
		result = protocol.QuotaResult{Snapshot: &copied}
		return cloneQuotaResult(result), nil
	}
	s.quotaVersion++
	snap.Version = s.quotaVersion
	stored := snap.Clone()
	entry.snapshot = &stored
	entry.cachedAt = now
	entry.version = snap.Version
	s.quotaMu.Unlock()

	result = protocol.QuotaResult{Snapshot: &snap}
	return cloneQuotaResult(result), nil
}

func (s *Server) quotaEligibilityError(id string, adapter harness.Adapter) string {
	s.runtimeMu.RLock()
	profile, isProfile := s.agentProfiles[id]
	s.runtimeMu.RUnlock()
	if isProfile {
		if profile.Disabled {
			return "此 Agent Profile 已停用"
		}
		switch profile.HarnessID {
		case domain.QuotaKindCodex:
			if profile.Mode == domain.AgentProfileAPI {
				return "Codex API Key 模式不支持额度查询"
			}
			return ""
		case domain.QuotaKindDeepSeek:
			return ""
		case domain.QuotaKindAntigravity:
			return "Antigravity 仅支持系统默认 Profile 的额度查询"
		default:
			return "此 Agent 不支持额度查询"
		}
	}
	desc := adapter.Descriptor()
	if desc.ProfileOnly {
		return "此 Agent 请选择具体的 Profile 查询额度"
	}
	switch id {
	case domain.QuotaKindCodex, domain.QuotaKindAntigravity:
		return ""
	default:
		return "此 Agent 不支持额度查询"
	}
}

func (s *Server) activeQuotaRuntime(harnessID string) harness.QuotaRuntime {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	for _, r := range s.runtimes {
		if r == nil || r.Stopped() {
			continue
		}
		if r.Readiness().HarnessID != harnessID {
			continue
		}
		if qr, ok := r.(harness.QuotaRuntime); ok {
			return qr
		}
	}
	return nil
}

func (s *Server) catalogQuotaRuntime(harnessID string) harness.QuotaRuntime {
	r := s.catalogRuntime(harnessID)
	if r == nil || r.Stopped() {
		return nil
	}
	if qr, ok := r.(harness.QuotaRuntime); ok {
		return qr
	}
	return nil
}

func quotaErrorResult(err error) protocol.QuotaResult {
	var qe *harness.QuotaError
	if errors.As(err, &qe) {
		secs := 0
		if qe.RetryAfter > 0 {
			secs = int(math.Ceil(qe.RetryAfter.Seconds()))
		}
		return protocol.QuotaResult{
			Error:           qe.Message,
			RetryAfterSecs:  secs,
			Unsupported:     qe.Unsupported,
			AuthUnavailable: qe.AuthUnavailable,
		}
	}
	var unsupported *harness.UnsupportedError
	if errors.As(err, &unsupported) {
		return protocol.QuotaResult{Error: "此 Agent 不支持额度查询", Unsupported: true}
	}
	return protocol.QuotaResult{Error: err.Error()}
}

func cloneQuotaResult(r protocol.QuotaResult) protocol.QuotaResult {
	if r.Snapshot != nil {
		copied := r.Snapshot.Clone()
		r.Snapshot = &copied
	}
	return r
}
