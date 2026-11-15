package worker

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"shioji/internal/portforward"
	"shioji/internal/protocol"
)

func (s *Server) serveTCP(conn net.Conn, codec *protocol.Codec, f protocol.Frame) {
	var target portforward.Target
	err := json.Unmarshal(f.Body, &target)
	if err == nil {
		target, err = target.Normalize()
	}
	var remote net.Conn
	if err == nil {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		remote, err = (&net.Dialer{}).DialContext(ctx, "tcp", target.Address())
		cancel()
	}
	result := protocol.Frame{Type: "result", ID: f.ID, Epoch: f.Epoch, DeviceID: s.store.DeviceID()}
	if err != nil {
		result.Error = "无法连接转发目标：" + err.Error()
		result.ErrorCode = "tcpDialFailed"
	} else {
		defer remote.Close()
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if codec.Write(result) != nil || err != nil {
		return
	}
	conn.SetDeadline(time.Time{})
	portforward.Relay(s.ctx, conn, remote)
}
