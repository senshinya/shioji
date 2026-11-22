package mobileclient

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"shioji/internal/app"
	"shioji/internal/domain"
)

type LegacySessionNavigation struct {
	DeviceID string
	Path     string
}

// ReadSessionPins migrates each enrolled device's original local ordering on
// first use. Once the shared file exists, even an empty list is authoritative.
func ReadSessionPins(path string, legacy []LegacySessionNavigation) ([]app.SessionLocation, error) {
	data, err := os.ReadFile(path)
	if !os.IsNotExist(err) {
		if err != nil {
			return nil, err
		}
		var pins []app.SessionLocation
		err = json.Unmarshal(data, &pins)
		return pins, err
	}

	var pins []app.SessionLocation
	for _, device := range legacy {
		data, err := os.ReadFile(device.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var state struct {
			Navigation app.NavigationPreferences `json:"navigation"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, fmt.Errorf("读取旧版置顶顺序：%w", err)
		}
		for _, pin := range state.Navigation.Pinned {
			if pin.DeviceID == device.DeviceID && pin.WorkspaceID != "" && pin.SessionID != "" && !slices.Contains(pins, pin) {
				pins = append(pins, pin)
			}
		}
	}
	if len(pins) > 0 {
		return pins, SaveSessionPins(path, pins)
	}
	return pins, nil
}

func SaveSessionPins(path string, pins []app.SessionLocation) error {
	data, err := json.Marshal(pins)
	if err != nil {
		return err
	}
	return app.WriteState(path, data)
}

func FindUnarchivedSession(catalog domain.Snapshot, ref app.SessionLocation) (domain.Session, domain.Workspace, bool) {
	if ref.DeviceID == "" || ref.WorkspaceID == "" || ref.SessionID == "" || catalog.DeviceID != ref.DeviceID {
		return domain.Session{}, domain.Workspace{}, false
	}
	for _, workspace := range catalog.Workspaces {
		if workspace.ID != ref.WorkspaceID || workspace.DeviceID != ref.DeviceID || workspace.Archived {
			continue
		}
		for _, session := range catalog.Sessions {
			if session.ID == ref.SessionID && session.WorkspaceID == workspace.ID && !session.Archived &&
				(session.Context.DeviceID == "" || session.Context.DeviceID == ref.DeviceID) {
				return session, workspace, true
			}
		}
		break
	}
	return domain.Session{}, domain.Workspace{}, false
}
