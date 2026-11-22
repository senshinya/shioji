package mobileclient

import (
	"context"
	"time"

	"golang.org/x/crypto/ssh"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type DeviceRequest struct {
	DeviceID, Address                string
	WorkspaceID, Workspace, ThreadID string
	SessionTitle                     string
	Request                          domain.PendingRequest
}

type DeviceCatalog struct {
	Snapshot    domain.Snapshot
	Address     string
	Name        string
	Minor       int
	RequestedAt time.Time
}

func (d DeviceCatalog) Label() string {
	return DeviceLabel(d.Name, d.Address)
}

func DeviceLabel(name, address string) string {
	if name == "" || name == address {
		return address
	}
	if address == "" {
		return name
	}
	return name + " · " + address
}

func ReadDeviceCatalog(ctx context.Context, device SavedDevice, signer ssh.Signer, route RouteDial) (DeviceCatalog, error) {
	return readVerifiedDevice(ctx, device, device.Endpoint, signer, route, func(ctx context.Context, client *protocol.Client, address string) (DeviceCatalog, error) {
		requestedAt := time.Now()
		catalog, err := readDeviceCatalog(ctx, client)
		return DeviceCatalog{Snapshot: catalog, Address: address, Minor: client.Minor, RequestedAt: requestedAt}, err
	})
}

// DeviceRequests projects each request with its owning session and workspace.
func DeviceRequests(catalog domain.Snapshot, address string) []DeviceRequest {
	if len(catalog.PendingRequests) == 0 {
		return nil
	}
	workspaces := make(map[string]domain.Workspace, len(catalog.Workspaces))
	for _, workspace := range catalog.Workspaces {
		if workspace.DeviceID == catalog.DeviceID {
			workspaces[workspace.ID] = workspace
		}
	}
	sessions := make(map[string]domain.Session, len(catalog.Sessions))
	for _, session := range catalog.Sessions {
		if session.Context.DeviceID == "" || session.Context.DeviceID == catalog.DeviceID {
			sessions[session.ID] = session
		}
	}
	var requests []DeviceRequest
	for _, request := range catalog.PendingRequests {
		if request.ID == "" || request.State != "" && !request.State.AwaitingResolution() {
			continue
		}
		session, found := sessions[request.SessionID]
		workspace, owned := workspaces[session.WorkspaceID]
		if !found || !owned {
			continue
		}
		requests = append(requests, DeviceRequest{DeviceID: catalog.DeviceID, Address: address, WorkspaceID: workspace.ID,
			Workspace: workspace.Name, SessionTitle: session.Title, ThreadID: session.ThreadID, Request: request})
	}
	return requests
}

// LivePendingRequests keeps the mobile catalog in sync with session
// projections, which receive request events between worker snapshots.
func LivePendingRequests(m *app.Model) []domain.PendingRequest {
	var current []domain.PendingRequest
	type requestKey struct{ session, id string }
	key := func(r domain.PendingRequest) requestKey { return requestKey{r.SessionID, r.ID} }
	open := make(map[requestKey]domain.PendingRequest)
	for _, session := range m.Snapshot.Sessions {
		for _, r := range m.Details[session.ID].Requests {
			if r.SessionID == session.ID && (r.State == "" || r.State.AwaitingResolution()) {
				current = append(current, r)
				open[key(r)] = r
			}
		}
	}
	// Keep existing pager and attention order when a history read reorders a
	// session's projection. Newly observed requests go at the end.
	result := make([]domain.PendingRequest, 0, len(current))
	for _, order := range [][]domain.PendingRequest{m.Snapshot.PendingRequests, current} {
		for _, r := range order {
			if latest, ok := open[key(r)]; ok {
				result = append(result, latest)
				delete(open, key(r))
			}
		}
	}
	return result
}
