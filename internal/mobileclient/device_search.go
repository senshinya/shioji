package mobileclient

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"time"

	"golang.org/x/crypto/ssh"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type DeviceSearchHit struct {
	domain.SearchResult
	DeviceID      string
	DeviceAddress string
	WorkspaceName string
	ThreadID      string
}

// SearchDevice reads an enrolled worker without changing the client's active
// device. Both SSH and the worker handshake check the saved route's identity.
func SearchDevice(ctx context.Context, device SavedDevice, preferred Endpoint, signer ssh.Signer, route RouteDial, query string) ([]DeviceSearchHit, error) {
	return readVerifiedDevice(ctx, device, preferred, signer, route, func(ctx context.Context, client *protocol.Client, endpoint Endpoint) ([]DeviceSearchHit, error) {
		return searchConnectedDevice(ctx, client, endpoint.Address, query)
	})
}

// readVerifiedDevice retries only read paths with the saved worker identity.
func readVerifiedDevice[T any](ctx context.Context, device SavedDevice, preferred Endpoint, signer ssh.Signer, route RouteDial,
	read func(context.Context, *protocol.Client, Endpoint) (T, error),
) (T, error) {
	var zero T
	deviceID := device.Endpoint.DeviceID
	if deviceID == "" {
		return zero, errors.New("执行设备尚未验证")
	}
	routes := slices.Clone(device.Endpoints())
	if index := slices.Index(routes, preferred); index > 0 {
		routes[0], routes[index] = routes[index], routes[0]
	}
	var lastErr error
	for _, endpoint := range routes {
		if endpoint.DeviceID != deviceID {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		client, err := protocol.Dial(dialCtx, func(ctx context.Context) (net.Conn, error) {
			return endpoint.DialRoute(ctx, signer, route)
		}, deviceID)
		cancel()
		if err == nil {
			result, queryErr := read(ctx, client, endpoint)
			client.Close()
			if queryErr == nil {
				return result, nil
			}
			// An RPC rejection or identity mismatch must surface as-is.
			var networkErr net.Error
			if !errors.Is(queryErr, io.EOF) && !errors.Is(queryErr, io.ErrUnexpectedEOF) &&
				!errors.Is(queryErr, io.ErrClosedPipe) && !errors.As(queryErr, &networkErr) {
				return zero, queryErr
			}
			err = queryErr
		}
		lastErr = err
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有已验证的连接路径")
	}
	return zero, lastErr
}

func searchConnectedDevice(ctx context.Context, client *protocol.Client, address, query string) ([]DeviceSearchHit, error) {
	var results []domain.SearchResult
	if err := client.Call(ctx, "session.search", protocol.SearchQuery{Query: query}, &results); err != nil || len(results) == 0 {
		return nil, err
	}
	catalog, err := readDeviceCatalog(ctx, client)
	if err != nil {
		return nil, err
	}
	workspaces := make(map[string]domain.Workspace, len(catalog.Workspaces))
	for _, workspace := range catalog.Workspaces {
		if workspace.DeviceID == client.DeviceID {
			workspaces[workspace.ID] = workspace
		}
	}
	sessions := make(map[string]domain.Session, len(catalog.Sessions))
	for _, session := range catalog.Sessions {
		sessions[session.ID] = session
	}
	hits := make([]DeviceSearchHit, 0, len(results))
	for _, result := range results {
		session, ok := sessions[result.SessionID]
		workspace, owned := workspaces[result.WorkspaceID]
		if !ok || !owned || session.WorkspaceID != workspace.ID ||
			session.Context.DeviceID != "" && session.Context.DeviceID != client.DeviceID {
			// A result can disappear between searching and reading the catalog.
			continue
		}
		result.Title, result.Archived = session.Title, session.Archived || workspace.Archived
		hits = append(hits, DeviceSearchHit{SearchResult: result, DeviceID: client.DeviceID,
			DeviceAddress: address, WorkspaceName: workspace.Name, ThreadID: session.ThreadID})
	}
	return hits, nil
}

func readDeviceCatalog(ctx context.Context, client *protocol.Client) (domain.Snapshot, error) {
	catalog, err := client.ReadSnapshot(ctx)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if catalog.DeviceID != client.DeviceID {
		return domain.Snapshot{}, errors.New("目录不属于已验证的执行设备")
	}
	return catalog, nil
}
