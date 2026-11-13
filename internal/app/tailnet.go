package app

import (
	"errors"
	"net/netip"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"strings"
)

func (c *Controller) TailnetSSH(s transport.SSH) (transport.SSH, error) {
	if s.TailnetPeer == "" {
		return s, nil
	}
	s.ProxySocket = ""
	if c.Tailnet == nil {
		return s, errors.New("Tailnet 尚未启动")
	}
	host := strings.Trim(s.Target[strings.LastIndex(s.Target, "@")+1:], "[]")
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return s, errors.New("请选择 Tailnet 设备地址")
	}
	s.ProxySocket, err = c.Tailnet.Route(s.TailnetPeer, ip.String(), s.Port)
	return s, err
}

// Attach before Run, including saved routes. Identity and catalog IDs do not change.
func (c *Controller) AttachTailnet(node tailnet.Connection) {
	c.Tailnet = node
	if c.fleet == nil {
		return
	}
	// Each path resolves its private bridge on every new dial; no stale socket
	// is captured across node restarts or automatic path handoff.
}
