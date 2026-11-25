package worker

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

const systemAgentProfilesFile = "system-agent-profiles.json"

// A system Profile refers to the original Agent selector and its native home.
type systemAgentProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) loadSystemAgentProfiles() error {
	s.systemAgentProfiles = map[string]systemAgentProfile{}
	data, err := os.ReadFile(filepath.Join(s.config.Directory, systemAgentProfilesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var profiles []systemAgentProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return err
	}
	for _, profile := range profiles {
		s.systemAgentProfiles[profile.ID] = profile
	}
	return nil
}

// The caller serializes migration with runtime settings changes under dispatchMu,
// or calls it before the worker starts serving commands.
func (s *Server) migrateSystemAgentProfiles() error {
	s.runtimeMu.RLock()
	saved := maps.Clone(s.systemAgentProfiles)
	s.runtimeMu.RUnlock()
	before := len(saved)
	for _, info := range s.runtimeProfiles(nil, true).Harnesses {
		if info.SystemProfile || info.Profile != nil || s.adapter(info.ID).Descriptor().ProfileOnly {
			continue
		}
		if _, configured := s.runtimeSettings[info.ID]; configured || info.HasSystemProfile() {
			saved[info.ID] = systemAgentProfile{ID: info.ID, Name: info.Name}
		}
	}
	if len(saved) == before {
		return nil
	}
	profiles := slices.SortedFunc(maps.Values(saved), func(a, b systemAgentProfile) int { return cmp.Compare(a.ID, b.ID) })
	data, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	if err := writeDeviceFile(s.config.Directory, systemAgentProfilesFile, data); err != nil {
		return err
	}
	s.runtimeMu.Lock()
	s.systemAgentProfiles = saved
	s.runtimeMu.Unlock()
	return nil
}
