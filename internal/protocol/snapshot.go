package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"shioji/internal/domain"
)

// Leave room for base64 encoding and the authenticated result envelope.
const SnapshotChunkBytes = MaxFrame / 2

type SnapshotQuery struct {
	Offset *int                  `json:"offset,omitempty"`
	Cursor *domain.CatalogCursor `json:"cursor,omitempty"`
}

type SnapshotPage struct {
	Data    []byte                `json:"data"`
	Next    int                   `json:"next,omitempty"`
	Catalog *domain.Snapshot      `json:"catalog,omitempty"`
	Cursor  *domain.CatalogCursor `json:"cursor,omitempty"`
}

func (c *Client) ReadSnapshot(ctx context.Context) (domain.Snapshot, error) {
	var snapshot domain.Snapshot
	if c.Minor >= CatalogMinor {
		return c.readCatalog(ctx)
	}
	if c.Minor < RecoveryMinor {
		err := c.Call(ctx, "snapshot", nil, &snapshot)
		return snapshot, err
	}
	var data []byte
	for {
		var raw json.RawMessage
		offset := len(data)
		if err := c.Call(ctx, "snapshot", SnapshotQuery{Offset: &offset}, &raw); err != nil {
			return snapshot, err
		}
		var page SnapshotPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return snapshot, err
		}
		if len(data) == 0 && page.Data == nil && page.Next == 0 {
			// A peer can return the original single-frame snapshot.
			err := json.Unmarshal(raw, &snapshot)
			return snapshot, err
		}
		if len(page.Data) == 0 || len(page.Data) > SnapshotChunkBytes ||
			page.Next != 0 && (len(page.Data) != SnapshotChunkBytes || page.Next != len(data)+len(page.Data)) {
			return snapshot, errors.New("设备目录分页不连续")
		}
		data = append(data, page.Data...)
		if page.Next == 0 {
			err := json.Unmarshal(data, &snapshot)
			return snapshot, err
		}
	}
}

func (c *Client) readCatalog(ctx context.Context) (domain.Snapshot, error) {
	var all domain.Snapshot
	cursor := domain.CatalogCursor{}
	for {
		var raw json.RawMessage
		if err := c.Call(ctx, "snapshot", SnapshotQuery{Cursor: &cursor}, &raw); err != nil {
			return all, err
		}
		var page SnapshotPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return all, err
		}
		part := page.Catalog
		if part == nil && cursor == (domain.CatalogCursor{}) {
			err := json.Unmarshal(raw, &all) // Original snapshot response.
			return all, err
		}
		if part == nil {
			return all, errors.New("设备目录分页响应不完整")
		}
		if cursor == (domain.CatalogCursor{}) {
			all = *part
		} else {
			if part.DeviceID != all.DeviceID || part.Generation != all.Generation || part.Sequence != all.Sequence {
				return all, errors.New("设备目录分页身份或游标不一致")
			}
			all.Workspaces = append(all.Workspaces, part.Workspaces...)
			all.Sessions = append(all.Sessions, part.Sessions...)
			all.PendingRequests = append(all.PendingRequests, part.PendingRequests...)
			all.Operations = append(all.Operations, part.Operations...)
		}
		if page.Cursor == nil {
			slices.Reverse(all.Operations)
			return all, nil
		}
		next := *page.Cursor
		if next.Section < cursor.Section || next.Section == cursor.Section && next.Row <= cursor.Row {
			return all, errors.New("设备目录分页未前进")
		}
		cursor = next
	}
}
