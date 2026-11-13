package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"shioji/internal/transport"
	"strings"
)

// Routes are client-owned access paths to one authenticated worker identity.
// SSH settings remain transport data, never an agent/harness configuration.
type DeviceRoute struct {
	ID  string        `json:"id"`
	SSH transport.SSH `json:"ssh"`
}

func deviceRoute(s transport.SSH) DeviceRoute {
	s.Target = strings.TrimSpace(s.Target)
	s.ProxySocket = ""
	// Port zero intentionally means OpenSSH configuration, not necessarily 22.
	b, _ := json.Marshal(struct {
		Target string
		Port   int
		Peer   string
	}{s.Target, s.Port, s.TailnetPeer})
	sum := sha256.Sum256(b)
	return DeviceRoute{ID: hex.EncodeToString(sum[:16]), SSH: s}
}

func (c DeviceConnection) Normalized() DeviceConnection {
	c.Routes = append([]DeviceRoute(nil), c.Routes...)
	if len(c.Routes) == 0 {
		for _, s := range append([]transport.SSH{c.SSH}, c.OtherSSH...) {
			if s.Target == "" {
				continue
			}
			r := deviceRoute(s)
			found := false
			for _, old := range c.Routes {
				if old.ID == r.ID {
					found = true
					break
				}
			}
			if !found {
				c.Routes = append(c.Routes, r)
			}
		}
	}
	found := false
	for _, r := range c.Routes {
		if r.ID == c.ActiveRouteID {
			found = true
		}
	}
	if !found && len(c.Routes) > 0 {
		c.ActiveRouteID = c.Routes[0].ID
	}
	return c
}

func (c DeviceConnection) ActiveSSH() transport.SSH {
	c = c.Normalized()
	for _, route := range c.Routes {
		if route.ID == c.ActiveRouteID {
			return route.SSH
		}
	}
	return c.SSH
}

func normalizedConnections(in []DeviceConnection) []DeviceConnection {
	out := make([]DeviceConnection, len(in))
	for i, c := range in {
		out[i] = c.Normalized()
	}
	return out
}
