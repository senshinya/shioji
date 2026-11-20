package domain

import "time"

// StorageUsage describes Shioji-owned files on one execution device or client.
// Retained bytes include originals; they are never part of cleanup.
type StorageUsage struct {
	RetainedBytes int64 `json:"retainedBytes"`
	PreviewBytes  int64 `json:"previewBytes"`
	UploadBytes   int64 `json:"uploadBytes"`
	LogBytes      int64 `json:"logBytes"`
}

func (u StorageUsage) Reclaimable() int64 { return u.PreviewBytes + u.UploadBytes + u.LogBytes }
func (u StorageUsage) Total() int64       { return u.RetainedBytes + u.Reclaimable() }

type StorageFailure struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type StorageReport struct {
	MeasuredAt time.Time        `json:"measuredAt,omitempty"`
	Usage      StorageUsage     `json:"usage"`
	FreedBytes int64            `json:"freedBytes"`
	Failures   []StorageFailure `json:"failures,omitempty"`
}
