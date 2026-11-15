package domain

import "time"

// DeviceHealth is sampled by the owning worker, never by the viewing client.
// A missing percentage is unknown; a reported zero is an idle measurement.
type DeviceLoad struct {
	At     time.Time     `json:"at"`
	CPU    *float64      `json:"cpu,omitempty"`
	Memory *DeviceMemory `json:"memory,omitempty"`
}
type KeepAwakeState struct {
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Revision  int64  `json:"revision"`
	Error     string `json:"error,omitempty"`
}
type DeviceHealth struct {
	DeviceID   string         `json:"deviceId"`
	ObservedAt time.Time      `json:"observedAt"`
	Samples    []DeviceLoad   `json:"samples"`
	KeepAwake  KeepAwakeState `json:"keepAwake"`
}
type KeepAwakeInput struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

type DeviceMemory struct {
	UsedBytes  uint64 `json:"usedBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}
