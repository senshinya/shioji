package app

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

// SSH processes live until their dial context is canceled. A Unix socket dial
// alone does not expose premature cancellation of the authenticated transport.
func TestHistoryReadKeepsTransportAliveAfterHandshake(t *testing.T) {
	for _, routed := range []bool{false, true} {
		for _, method := range []string{"session.history", "session.outline", "session.matches"} {
			t.Run(method+map[bool]string{false: "/direct", true: "/routed"}[routed], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				c := NewController("")
				dial := func(lifetime context.Context) (net.Conn, error) {
					client, server := net.Pipe()
					stop := context.AfterFunc(lifetime, func() { client.Close() })
					go func() {
						defer stop()
						defer server.Close()
						codec, _, err := protocol.Welcome(server, "device")
						if err != nil {
							return
						}
						var f protocol.Frame
						if codec.Read(&f) != nil {
							return
						}
						if lifetime.Err() != nil {
							return
						}
						switch f.Method {
						case "session.history":
							result(codec, f, domain.SessionDetail{Session: domain.Session{ID: "s"}, History: &domain.HistoryWindow{}, Items: []domain.Item{{ID: "old", Kind: "user", Text: "history"}}})
						case "session.outline":
							result(codec, f, domain.HistoryOutline{SessionID: "s", Prompts: []domain.HistoryPrompt{{ID: "old", Text: "history"}}})
						case "session.matches":
							result(codec, f, domain.HistoryMatches{SessionID: "s", Query: "history"})
						}
					}()
					return client, nil
				}
				c.Dial = dial
				if routed {
					c.paths = newDevicePaths(DeviceConnection{Routes: []DeviceRoute{{ID: "route", SSH: transport.SSH{Target: "host"}}}, ActiveRouteID: "route"}, func(ctx context.Context, _ transport.SSH) (net.Conn, error) { return dial(ctx) })
				}
				var body json.RawMessage
				var readErr error
				var input any = domain.HistoryQuery{SessionID: "s", Rounds: 20}
				if method == "session.matches" {
					input = domain.HistorySearchQuery{SessionID: "s", Query: "history"}
				}
				c.startHistoryRead(ctx, &protocol.Client{Epoch: "epoch", DeviceID: "device", Minor: 37}, Job{
					epoch: "epoch", Method: method, Input: input,
					Apply: func(_ *Model, b json.RawMessage, err error) { body, readErr = b, err },
				})
				select {
				case update := <-c.Updates:
					update(NewModel())
				case <-time.After(3 * time.Second):
					t.Fatal("history read did not finish")
				}
				if readErr != nil {
					t.Fatalf("transport died after handshake: %v", readErr)
				}
				var got struct {
					SessionID string         `json:"sessionId"`
					Session   domain.Session `json:"session"`
				}
				if err := json.Unmarshal(body, &got); err != nil || got.SessionID != "s" && got.Session.ID != "s" {
					t.Fatalf("missing history receipt: %s (%v)", body, err)
				}
			})
		}
	}
}

func TestHistoryReadCancellationClosesTransport(t *testing.T) {
	for _, duringHandshake := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "handshake"}[duringHandshake], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			readCtx, cancelRead := context.WithCancel(ctx)
			defer cancelRead()
			started, disconnected := make(chan struct{}), make(chan struct{})
			c := NewController("")
			c.Dial = func(lifetime context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				stop := context.AfterFunc(lifetime, func() { client.Close() })
				go func() {
					defer stop()
					defer server.Close()
					defer close(disconnected)
					if duringHandshake {
						close(started)
					} else {
						codec, _, err := protocol.Welcome(server, "device")
						if err != nil {
							return
						}
						var frame protocol.Frame
						if codec.Read(&frame) != nil {
							return
						}
						close(started)
					}
					// Wait for the real connection to close, not just its context.
					var b [1]byte
					for {
						if _, err := server.Read(b[:]); err != nil {
							return
						}
					}
				}()
				return client, nil
			}
			c.startHistoryRead(ctx, &protocol.Client{Epoch: "epoch", DeviceID: "device", Minor: 37}, Job{epoch: "epoch", Context: readCtx, Method: "session.outline"})
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("history request never started")
			}
			cancelRead()
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("abandoned history request left its transport open")
			}
		})
	}
}
