package store

import (
	"encoding/json"
	"errors"
	"time"

	"shioji/internal/domain"
)

var catalogSections = [...]string{
	"SELECT rowid,body FROM workspaces WHERE rowid>? ORDER BY rowid LIMIT 33",
	"SELECT rowid,body FROM sessions WHERE rowid>? ORDER BY rowid LIMIT 33",
	"SELECT rowid,body FROM requests WHERE rowid>? AND json_extract(body,'$.state') IN ('pending','submitting') ORDER BY rowid LIMIT 33",
	`SELECT rowid,json_object('id',id,'sessionId',session_id,'state',state,'error',error,'kind',kind)
	 FROM operations WHERE rowid>? AND rowid IN (SELECT rowid FROM operations ORDER BY rowid DESC LIMIT 100) ORDER BY rowid LIMIT 33`,
}

func (h *HistorySnapshot) CatalogPage(cursor domain.CatalogCursor) (domain.Snapshot, *domain.CatalogCursor, error) {
	s := h.reader
	part := domain.Snapshot{DeviceID: s.deviceID, Generation: s.generation}
	if cursor.Section < 0 || cursor.Section >= len(catalogSections) || cursor.Row < 0 {
		return part, nil, errors.New("无效的设备目录分页游标")
	}
	read := s.historyQueries()
	if err := read.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&part.Sequence); err != nil {
		return part, nil, err
	}
	if cursor == (domain.CatalogCursor{}) {
		policy, err := load[domain.ArchivePolicy](read.QueryRow("SELECT body FROM archive_policy WHERE id=1"))
		if err != nil {
			return part, nil, err
		}
		part.ArchivePolicy, part.ObservedAt = &policy, time.Now().UTC()
	}
	rows, err := read.Query(catalogSections[cursor.Section], cursor.Row)
	if err != nil {
		return part, nil, err
	}
	defer rows.Close()
	size, last := 0, cursor.Row
	for i := 0; rows.Next(); i++ {
		var row int64
		var raw []byte
		if err = rows.Scan(&row, &raw); err != nil {
			return part, nil, err
		}
		if len(raw) > 512<<10 {
			return part, nil, errors.New("设备目录中的记录过大")
		}
		if i == 32 || size+len(raw) > 512<<10 {
			return part, &domain.CatalogCursor{Section: cursor.Section, Row: last}, nil
		}
		switch cursor.Section {
		case 0:
			err = appendCatalogRecord(&part.Workspaces, raw)
		case 1:
			err = appendCatalogRecord(&part.Sessions, raw)
		case 2:
			err = appendCatalogRecord(&part.PendingRequests, raw)
		case 3:
			err = appendCatalogRecord(&part.Operations, raw)
		}
		if err != nil {
			return part, nil, err
		}
		size, last = size+len(raw), row
	}
	if err := rows.Err(); err != nil {
		return part, nil, err
	}
	if cursor.Section == len(catalogSections)-1 {
		return part, nil, nil
	}
	return part, &domain.CatalogCursor{Section: cursor.Section + 1}, nil
}

func appendCatalogRecord[T any](entries *[]T, raw []byte) error {
	var entry T
	if err := json.Unmarshal(raw, &entry); err != nil {
		return err
	}
	*entries = append(*entries, entry)
	return nil
}
