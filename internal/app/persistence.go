package app

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

func (m *Model) EncodeState() ([]byte, error) {
	completed := !m.FirstRun
	state := savedState{RemotePathUploads: m.RemotePathUploads, WorkerRecoveryIDs: m.WorkerRecoveryIDs, WorkerMaintenanceIDs: m.WorkerMaintenanceIDs, ArchivePolicyChanges: m.ArchivePolicyChanges, Navigation: m.Navigation, QuotaPanel: m.QuotaPanel.Normalized(), ServicePreparations: m.ServicePreparations, ServicePreparationDeviceID: m.ServicePreparationDeviceID, WorkerMigrations: m.WorkerMigrations, SignInIDs: m.SignInIDs, SetupCompleted: &completed, ModelMenus: m.ModelMenus, LocalDeviceName: m.LocalDeviceName, LocalDeviceIcon: m.LocalDeviceIcon, DraftSkills: m.DraftSkills, HomeConfigContext: m.homeConfigContext, HomeHarnesses: m.HomeHarnesses, HarnessConfigs: m.HarnessConfigs, HistoryActions: m.HistoryActions, HistoryEdits: m.HistoryEdits, Preferences: m.Preferences, SecondaryWorkspaceID: m.SecondaryWorkspaceID, SecondarySessionID: m.SecondarySessionID, FocusSecondary: m.FocusSecondary, Attachments: m.Attachments, DeviceCache: m.cachedDevices(), Connections: normalizedConnections(m.Connections), ActiveConnection: m.ActiveConnection, PendingDevices: m.PendingDevices, PendingGenerations: m.pendingGenerations, QueueExpanded: m.QueueExpanded, QueueEdits: m.QueueEdits, WorkspaceID: m.WorkspaceID, Drafts: m.Drafts, Configs: m.Configs, SessionConfigDrafts: m.sessionConfigDrafts, Answers: m.Answers, Pager: m.Pager, Collapsed: m.Collapsed, Pending: m.Pending, IndependentInputs: m.independentInputs, DeviceID: m.localIdentity(), GitDrafts: m.GitDrafts, GitRepositories: m.GitRepositories, Inspectors: m.Inspectors, InspectorLayout: m.InspectorLayout, InspectorSplitHeight: m.InspectorSplitHeight, InspectorWidth: m.InspectorWidth}
	return m.encodeState(state)
}

// Old clients put alternate drafts and their references under retained:* keys.
// Recover unambiguous drafts into vacant editors. Conflicts stay in the state
// file under their original keys so that saving after an upgrade is lossless.
func (m *Model) restoreLegacyDrafts() {
	blocked := map[string]bool{}
	for _, in := range m.Pending {
		key := "home"
		if in.SessionID != "" {
			key = "session:" + in.SessionID
		}
		blocked[key] = true
	}
	for id, edit := range m.QueueEdits {
		if edit.ID != "" {
			blocked["session:"+id] = true
		}
	}
	candidates := map[string]string{}
	for legacy := range m.Drafts {
		parts, ok := strings.CutPrefix(legacy, "retained:")
		if !ok || m.draftEmpty(legacy) {
			continue
		}
		kind, rest, _ := strings.Cut(parts, ":")
		id, operation, ok := strings.Cut(rest, ":")
		if !ok || id == "" || operation == "" {
			continue
		}
		var key string
		switch {
		case kind == "session":
			key = "session:" + id
		case kind == "home" && m.WorkspaceID == id:
			key = "home"
		default:
			continue
		}
		if blocked[key] || !m.draftEmpty(key) {
			continue
		}
		if _, multiple := candidates[key]; multiple {
			candidates[key] = ""
		} else {
			candidates[key] = legacy
		}
	}
	for key, legacy := range candidates {
		if legacy != "" {
			m.Drafts[key], m.Attachments[key], m.DraftSkills[key] = m.Drafts[legacy], m.Attachments[legacy], m.DraftSkills[legacy]
			m.deleteDraft(legacy)
		}
	}
}

func (m *Model) deleteLegacyDrafts(prefix string) {
	for key := range m.Drafts {
		if strings.HasPrefix(key, "retained:"+prefix+":") {
			m.deleteDraft(key)
		}
	}
}

func (m *Model) deleteDraft(key string) {
	delete(m.Drafts, key)
	delete(m.Attachments, key)
	delete(m.DraftSkills, key)
}

// Only layout changes invalidate the large disclosure map. Encode the smaller
// current state independently so animation frames and streaming events do not
// repeatedly sort and quote every historical tool ID on the application thread.
// All returned byte slices remain immutable for asynchronous StateWriter calls.
type clientStateEncoding struct {
	layout                 map[string]bool
	layoutJSON, rest, data []byte
}

func (m *Model) encodeState(state savedState) ([]byte, error) {
	state.Collapsed = nil
	rest, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	cache := m.stateEncoding
	changed := cache == nil || (m.Collapsed == nil) != (cache.layout == nil) || !maps.Equal(m.Collapsed, cache.layout)
	if changed {
		layout, err := json.Marshal(m.Collapsed)
		if err != nil {
			return nil, err
		}
		cache = &clientStateEncoding{layout: maps.Clone(m.Collapsed), layoutJSON: layout}
		m.stateEncoding = cache
	}
	if !changed && bytes.Equal(rest, cache.rest) {
		return cache.data, nil
	}
	// Both fragments come from json.Marshal; join the omitted layout field at
	// the object boundary without reparsing or copying its keys through JSON.
	data := make([]byte, 0, len(rest)+len(cache.layoutJSON)+13)
	data = append(data, rest[:len(rest)-1]...)
	data = append(data, `,"collapsed":`...)
	data = append(data, cache.layoutJSON...)
	data = append(data, '}')
	cache.rest, cache.data = rest, data
	return data, nil
}

func WriteState(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "client-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
