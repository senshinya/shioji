package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"strings"
)

func (s *Server) configureAdapters() error {
	adapters := s.config.Harnesses
	if len(adapters) == 0 {
		adapters = defaultAdapters()
	}
	s.adapters = map[string]harness.Adapter{}
	for _, a := range adapters {
		d := a.Descriptor()
		if d.ID == "" || d.ID == "." || d.ID == ".." || filepath.Base(d.ID) != d.ID || d.Name == "" {
			return errors.New("invalid harness descriptor")
		}
		if _, exists := s.adapters[d.ID]; exists {
			return fmt.Errorf("duplicate harness %q", d.ID)
		}
		s.adapters[d.ID] = a
		s.adapterOrder = append(s.adapterOrder, d.ID)
	}
	if s.config.DefaultHarnessID == "" {
		s.config.DefaultHarnessID = s.adapterOrder[0]
	}
	if s.adapters[s.config.DefaultHarnessID] == nil {
		return errors.New("default harness is not configured")
	}
	if err := s.loadCatalogOwner(); err != nil {
		return err
	}
	s.catalogs = map[string]harness.Runtime{}
	s.catalogErrors = map[string]error{}
	return nil
}
func (s *Server) catalogDirectory(id string) string {
	if id == s.rootCatalogID {
		return s.config.Directory
	} // Existing ownership records stay valid.
	return filepath.Join(s.config.Directory, "catalogs", id)
}
func (s *Server) catalogRuntime(id string) harness.Runtime {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if id == s.config.DefaultHarnessID || id == "" {
		return s.runtime
	}
	return s.catalogs[id]
}
func (s *Server) inputHarness(in domain.Submission) (string, error) {
	id := in.HarnessID
	if in.SessionID != "" {
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return "", err
		}
		if id != "" && id != d.Session.HarnessID {
			return "", errors.New("session harness cannot change")
		}
		id = d.Session.HarnessID
		if id == "" {
			return "", errors.New("saved session has no harness identity")
		}
	} else if id == "" {
		id = s.config.DefaultHarnessID
	}
	if s.adapters[id] == nil {
		return "", fmt.Errorf("harness %q is unavailable", id)
	}
	return id, nil
}
func (s *Server) inputRuntime(in domain.Submission) (harness.Runtime, error) {
	id, err := s.inputHarness(in)
	if err != nil {
		return nil, err
	}
	r := s.catalogRuntime(id)
	if r == nil || s.ctx.Err() != nil {
		return nil, errors.New("runtime unavailable")
	}
	return r, nil
}
func (s *Server) openCatalog(ctx context.Context, id string, stop bool) (runtime harness.Runtime, startErr error) {
	defer func() {
		s.runtimeMu.Lock()
		if s.catalogErrors == nil {
			s.catalogErrors = map[string]error{}
		}
		s.catalogErrors[id] = startErr
		s.runtimeMu.Unlock()
	}()
	if r := s.catalogRuntime(id); r != nil && !r.Stopped() {
		return r, nil
	}
	a := s.adapters[id]
	if a == nil {
		return nil, errors.New("harness unavailable")
	}
	dir := s.catalogDirectory(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if stop {
		if err := stopPreviousProcess(ctx, dir); err != nil {
			return nil, err
		}
	}
	if err := checkPreviousProcess(dir); err != nil {
		return nil, err
	}
	if err := recordProcess(dir, 0); err != nil {
		return nil, err
	}
	next, err := a.Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error { return s.onHarnessEvent(id, "", e) }, OnStart: func(pid int) error { return recordProcess(dir, pid) }})
	if err != nil {
		os.Remove(filepath.Join(dir, "runtime-process.json"))
		return nil, err
	}
	s.runtimeMu.Lock()
	s.catalogs[id] = next
	if id == s.config.DefaultHarnessID {
		s.runtime = next
	}
	s.runtimeMu.Unlock()
	s.watchRuntime(next)
	return next, nil
}
func (s *Server) runtimeReadiness() domain.Readiness {
	var all []domain.HarnessInfo
	for _, id := range s.adapterOrder {
		descriptor := s.adapters[id].Descriptor()
		info := domain.HarnessInfo{ID: id, Name: descriptor.Name, Icon: descriptor.Icon, State: domain.RuntimeUnavailable}
		if r := s.catalogRuntime(id); r != nil {
			ready := r.Readiness()
			info.State, info.Error, info.Models, info.Permissions, info.PermissionChoices, info.Plan, info.Capabilities = ready.State, ready.Error, ready.Models, ready.Permissions, ready.PermissionChoices, ready.Plan, ready.Capabilities
		} else {
			s.runtimeMu.RLock()
			err := s.catalogErrors[id]
			s.runtimeMu.RUnlock()
			if err != nil {
				info.Error = err.Error()
			}
		}
		all = append(all, info)
	}
	if len(all) == 0 {
		if r := s.currentRuntime(); r != nil {
			return r.Readiness()
		}
		return s.readiness
	}
	result := domain.Readiness{HarnessID: s.config.DefaultHarnessID, Harnesses: all}
	selected, _ := result.ForHarness(s.config.DefaultHarnessID)
	return selected
}

// Adapters report agent activity, not authoritative workspace/session commands.
// Bind events to the configured harness and owned session before persistence.
func (s *Server) onHarnessEvent(harnessID, sessionID string, e domain.Event) error {
	if e.SessionID == "" {
		return nil
	}
	if sessionID != "" && e.SessionID != sessionID {
		return errors.New("harness event belongs to another session")
	}
	if sessionID == "" {
		d, err := s.store.Session(e.SessionID, 0, 1)
		if err != nil {
			return err
		}
		if d.Session.HarnessID != harnessID {
			return errors.New("harness event belongs to another harness")
		}
	}
	if e.Workspace != nil || e.Session != nil || e.Operation != nil || e.Queue != nil || e.HistoryTurnOrder != nil {
		return errors.New("harness event contains worker-owned mutation")
	}
	if e.Request != nil && e.Request.SessionID != "" && e.Request.SessionID != e.SessionID {
		return errors.New("request belongs to another session")
	}
	switch e.Type {
	case "session.title", "turn", "item.upsert", "request.open", "request.resolve", "plan", "plan.text", "runtime.lost":
	default:
		return errors.New("harness event cannot mutate worker-owned state")
	}
	return s.onEvent(e)
}

// The legacy root process record has one durable owner. Selecting a different
// default never reassigns that process to another adapter.
func (s *Server) loadCatalogOwner() error {
	path := filepath.Join(s.config.Directory, "catalog-owner")
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
			return errors.New("invalid catalog owner record")
		}
		s.rootCatalogID = id
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	id := s.config.DefaultHarnessID
	if _, err := os.Stat(filepath.Join(s.config.Directory, "runtime-process.json")); err == nil {
		id = legacyCatalogHarnessID()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(s.config.Directory, "catalog-owner-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString(id + "\n")
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	s.rootCatalogID = id
	return nil
}
