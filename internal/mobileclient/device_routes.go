package mobileclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"shioji/internal/app"
)

// SavedDevice owns one catalog and draft storage slot. Each route carries its
// own SSH login and host verification.
type SavedDevice struct {
	Slot     string         `json:"slot,omitempty"`
	Endpoint Endpoint       `json:"endpoint"`
	Routes   []Endpoint     `json:"routes,omitempty"`
	Name     string         `json:"name,omitempty"`
	Icon     app.DeviceIcon `json:"icon,omitempty"`
}

func (d SavedDevice) WithAppearance(name string, icon app.DeviceIcon) (SavedDevice, error) {
	if d.Endpoint.DeviceID == "" {
		return d, errors.New("尚未确认设备身份")
	}
	if !icon.Valid() {
		return d, errors.New("不支持的设备图标")
	}
	if name = strings.TrimSpace(name); name != "" {
		var err error
		if name, err = app.NormalizeDeviceName(name); err != nil {
			return d, err
		}
	}
	d.Name, d.Icon = name, icon
	return d, nil
}

func (d SavedDevice) Endpoints() []Endpoint {
	if len(d.Routes) == 0 {
		return []Endpoint{d.Endpoint}
	}
	return d.Routes
}

func (d SavedDevice) WithRoute(endpoint Endpoint) (SavedDevice, error) {
	if endpoint.DeviceID == "" || endpoint.DeviceID != d.Endpoint.DeviceID {
		return d, errors.New("路径属于其他执行设备")
	}
	d.Routes = slices.Clone(d.Endpoints())
	for i, existing := range d.Routes {
		if !existing.sameRoute(endpoint) {
			continue
		}
		d.Routes[i] = endpoint
		if d.Endpoint.sameRoute(endpoint) {
			d.Endpoint = endpoint
		}
		return d, nil
	}
	d.Routes = append(d.Routes, endpoint)
	return d, nil
}

// WithoutRoute removes the verified route identified by id. The device keeps
// the same storage slot and uses the replacement as its preferred route if needed.
func (d SavedDevice) WithoutRoute(id, replacement string) (SavedDevice, error) {
	endpoints := d.Endpoints()
	index := slices.IndexFunc(endpoints, func(endpoint Endpoint) bool { return endpoint.RouteID() == id })
	if index < 0 {
		return d, errors.New("连接路径已变化，请重新选择")
	}
	if len(endpoints) < 2 {
		return d, errors.New("设备至少需要一条连接路径")
	}
	routes := slices.Delete(slices.Clone(endpoints), index, index+1)
	if d.Endpoint.RouteID() == id {
		preferred := slices.IndexFunc(routes, func(endpoint Endpoint) bool { return endpoint.RouteID() == replacement })
		if preferred < 0 {
			return d, errors.New("备用连接路径已变化，请重新选择")
		}
		d.Endpoint = routes[preferred]
	}
	d.Routes = routes
	return d, nil
}

func (e Endpoint) sameRoute(other Endpoint) bool {
	return e.Address == other.Address && e.User == other.User && e.TailnetPeerID == other.TailnetPeerID
}

// RouteID changes when this route's verified transport settings change.
func (e Endpoint) RouteID() string {
	encoded, _ := json.Marshal(e)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:16])
}
