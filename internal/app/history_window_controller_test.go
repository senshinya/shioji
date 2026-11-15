package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"sync/atomic"
	"testing"
	"time"
)

func TestLatestHistoryReadUsesOneIndependentCancellableRequest(t *testing.T) {
	for _, switchAway := range []bool{false, true} {
		t.Run(fmt.Sprint(switchAway), func(t *testing.T) {
			dir, e := os.MkdirTemp("", "hwc-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(dir)
			listener, e := net.Listen("unix", filepath.Join(dir, "worker.sock"))
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release, interrupted := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
			queries := make(chan domain.HistoryQuery, 4)
			var calls atomic.Int32
			session := func(id string) domain.Session {
				return domain.Session{ID: id, WorkspaceID: "w", ThreadID: "source", Revision: 1, Context: domain.LaunchContext{DeviceID: "device"}}
			}
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					go func() {
						defer conn.Close()
						stop := context.AfterFunc(ctx, func() { conn.Close() })
						defer stop()
						codec, _, err := protocol.Welcome(conn, "device")
						if err != nil {
							return
						}
						for {
							var f protocol.Frame
							if codec.Read(&f) != nil {
								return
							}
							switch f.Method {
							case "snapshot":
								result(codec, f, domain.Snapshot{DeviceID: "device", Generation: "g", Sessions: []domain.Session{session("s"), session("other")}})
							case "readiness":
								result(codec, f, domain.Readiness{State: "ready"})
							case "events.watch":
								<-ctx.Done()
								return
							case "session.history":
								var q domain.HistoryQuery
								json.Unmarshal(f.Body, &q)
								calls.Add(1)
								if q.SessionID == "s" {
									queries <- q
									started <- struct{}{}
									select {
									case <-release:
									case <-ctx.Done():
										return
									}
								}
								d := domain.SessionDetail{Session: session(q.SessionID), History: &domain.HistoryWindow{First: 1001, Last: 1100, Older: true}}
								for i := int64(1001); i <= 1100; i++ {
									d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Order: i, Kind: "assistant", Text: "body", Final: true})
								}
								result(codec, f, d)
							case "session.interrupt":
								interrupted <- struct{}{}
								result(codec, f, struct{}{})
							default:
								result(codec, f, struct{}{})
							}
						}
					}()
				}
			}()
			c := NewController(dir)
			c.Select("s")
			done := make(chan struct{})
			go func() { defer close(done); c.Run(ctx) }()
			defer func() { cancel(); <-done }()
			m := NewModel()
			m.Preferences.HistoryPageRounds = 50
			startTimer := time.NewTimer(3 * time.Second)
		startLoop:
			for {
				select {
				case <-started:
					break startLoop
				case update := <-c.Updates:
					update(m)
				case <-startTimer.C:
					t.Fatal("latest history never started")
				}
			}
			startTimer.Stop()
			if q := <-queries; q.Rounds != 50 || q.Limit != 0 {
				t.Fatal("initial history request ignored round preference", q)
			}
			c.Do(Job{Method: "session.interrupt"})
			select {
			case <-interrupted:
			case <-time.After(time.Second):
				t.Fatal("old history blocked Stop on the command connection")
			}
			pump := func(ready func() bool) {
				t.Helper()
				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				for !ready() {
					select {
					case update := <-c.Updates:
						update(m)
					case <-timer.C:
						t.Fatal("history did not settle")
					}
				}
			}
			if switchAway {
				c.Select("other")
				pump(func() bool { return len(m.Details["other"].Items) == 100 })
			}
			close(release)
			if !switchAway {
				pump(func() bool { return len(m.Details["s"].Items) == 100 })
				if calls.Load() != 1 || !m.Details["s"].History.Older {
					t.Fatal("opening hydrated more than one page", calls.Load())
				}
			} else {
				timer := time.NewTimer(100 * time.Millisecond)
				defer timer.Stop()
			loop:
				for {
					select {
					case update := <-c.Updates:
						update(m)
					case <-timer.C:
						break loop
					}
				}
				if len(m.Details["s"].Items) != 0 {
					t.Fatal("canceled history populated an abandoned opening")
				}
			}
		})
	}
}
