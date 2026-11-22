package mobileclient

import (
	"context"
	"errors"

	"golang.org/x/crypto/ssh"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// ReadDeviceSessionHistory reads an enrolled device without taking over the app's
// current session or device connection.
func ReadDeviceSessionHistory(ctx context.Context, device SavedDevice, signer ssh.Signer, route RouteDial,
	location app.SessionLocation, query domain.HistoryQuery) (domain.SessionDetail, error) {
	if location.DeviceID == "" || location.DeviceID != device.Endpoint.DeviceID ||
		location.WorkspaceID == "" || location.SessionID == "" || query.SessionID != location.SessionID {
		return domain.SessionDetail{}, errors.New("会话归属已改变")
	}
	return readVerifiedDevice(ctx, device, device.Endpoint, signer, route,
		func(ctx context.Context, client *protocol.Client, _ Endpoint) (domain.SessionDetail, error) {
			var page domain.SessionDetail
			if err := client.Call(ctx, "session.history", query, &page); err != nil {
				return domain.SessionDetail{}, err
			}
			session := page.Session
			if session.ID != location.SessionID || session.WorkspaceID != location.WorkspaceID ||
				session.ThreadID != query.ThreadID || session.Context.DeviceID != "" && session.Context.DeviceID != location.DeviceID {
				return domain.SessionDetail{}, errors.New("会话记录不属于所选会话")
			}
			return page, nil
		})
}
