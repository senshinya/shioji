package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type Update func(*Model)
type Job struct {
	StatePath string
	State     []byte
	Method    string
	Input     any
	Apply     func(*Model, json.RawMessage, error)
}
type Controller struct {
	ExpectedDeviceID string
	Directory        string
	Jobs             chan Job
	Updates          chan Update
	mu               sync.Mutex
	invalidate       func()
}

func NewController(dir string) *Controller {
	return &Controller{Directory: dir, Jobs: make(chan Job, 16), Updates: make(chan Update, 256)}
}
func (c *Controller) SetInvalidate(fn func()) { c.mu.Lock(); c.invalidate = fn; c.mu.Unlock() }
func (c *Controller) post(update Update) {
	c.Updates <- update
	c.mu.Lock()
	fn := c.invalidate
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}
func (c *Controller) Do(job Job) bool {
	select {
	case c.Jobs <- job:
		return true
	default:
		return false
	}
}
func (c *Controller) Select(id string) { c.Do(Job{Method: "$select", Input: id}) }
func (c *Controller) Run(ctx context.Context) {
	var client *protocol.Client
	var generation string
	var sequence int64
	var selected string
	var loaded string
	defer func() {
		if client != nil {
			client.Close()
		}
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastAttempt := time.Time{}
	disconnected := func() {
		if client != nil {
			client.Close()
		}
		client = nil
		loaded = ""
		c.post(func(m *Model) { m.Connected = false })
	}
	fetchDetail := func() error {
		if selected == "" || selected == loaded {
			return nil
		}
		var all domain.SessionDetail
		var after int64
		for {
			var page domain.SessionDetail
			if err := client.Call(ctx, "session", protocol.SessionQuery{ID: selected, After: after, Limit: 100}, &page); err != nil {
				return err
			}
			all.Session = page.Session
			all.Requests = page.Requests
			all.Turns = page.Turns
			all.Sequence = page.Sequence
			all.Items = append(all.Items, page.Items...)
			if !page.HasMore {
				break
			}
			after = page.Items[len(page.Items)-1].Order
		}
		loaded = selected
		id := selected
		c.post(func(m *Model) { m.Details[id] = all })
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-c.Jobs:
			if job.Method == "$select" {
				selected = job.Input.(string)
				loaded = ""
				if client != nil {
					if err := fetchDetail(); err != nil {
						disconnected()
					}
				}
				continue
			}
			if job.StatePath != "" {
				if err := WriteState(job.StatePath, job.State); err != nil {
					if job.Apply != nil {
						c.post(func(m *Model) { job.Apply(m, nil, err) })
					} else {
						c.post(func(m *Model) { m.Error = "无法保存草稿：" + err.Error() })
					}
					continue
				}
			}
			if job.Method == "$save" {
				continue
			}
			var out json.RawMessage
			var err error
			if client == nil {
				err = net.ErrClosed
			} else {
				callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err = client.Call(callCtx, job.Method, job.Input, &out)
				cancel()
			}
			if job.Apply != nil {
				c.post(func(m *Model) { job.Apply(m, out, err) })
			}
			var remote *protocol.RemoteError
			if err != nil && !errors.As(err, &remote) {
				disconnected()
			}
		case <-ticker.C:
			if client == nil {
				if time.Since(lastAttempt) < time.Second {
					continue
				}
				lastAttempt = time.Now()
				conn, err := net.DialTimeout("unix", filepath.Join(c.Directory, "worker.sock"), time.Second)
				if err != nil {
					continue
				}
				client, err = protocol.Connect(ctx, conn, c.ExpectedDeviceID)
				if err != nil {
					conn.Close()
					client = nil
					c.post(func(m *Model) { m.Error = err.Error() })
					continue
				}
				var snapshot domain.Snapshot
				var ready domain.Readiness
				if client.Call(ctx, "snapshot", nil, &snapshot) != nil || client.Call(ctx, "readiness", nil, &ready) != nil {
					disconnected()
					continue
				}
				c.ExpectedDeviceID = snapshot.DeviceID
				generation = snapshot.Generation
				sequence = snapshot.Sequence
				c.post(func(m *Model) { m.Connected = true; m.Readiness = ready; m.ApplySnapshot(snapshot) })
				loaded = ""
			}
			// Pull the journal before hydrating a selection so the detail's barrier cannot lose an event.
			var page protocol.EventPage
			if err := client.Call(ctx, "events", protocol.EventsQuery{After: sequence, Generation: generation}, &page); err != nil {
				disconnected()
				continue
			}
			if page.Generation != generation {
				disconnected()
				continue
			}
			if len(page.Events) > 0 {
				events := page.Events
				sequence = events[len(events)-1].Sequence
				c.post(func(m *Model) {
					for _, e := range events {
						if err := m.ApplyEvent(e); err != nil {
							m.Error = err.Error()
							m.Connected = false
							return
						}
					}
				})
			}
			if err := fetchDetail(); err != nil {
				disconnected()
			}
		}
	}
}
