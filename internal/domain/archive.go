package domain

// ArchivePolicy is persisted and enforced by each owning worker. Zero days
// explicitly disables automatic deletion; an absent policy is a legacy worker.
type ArchivePolicy struct {
	DeviceID string `json:"deviceId"`
	Days     int    `json:"days"`
	Revision int64  `json:"revision"`
}

func ValidArchiveDays(days int) bool {
	return days == 0 || days == 7 || days == 14 || days == 30
}

// ArchiveDeletion is the authoritative removal of Shioji-owned catalog/history.
// WorkspaceID is populated only when the workspace itself was removed.
type ArchiveDeletion struct {
	DeviceID    string   `json:"deviceId"`
	WorkspaceID string   `json:"workspaceId,omitempty"`
	SessionIDs  []string `json:"sessionIds"`
}
