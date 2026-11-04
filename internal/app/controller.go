package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type Update func(*Model)
type Job struct {
	ConnectionEpoch string
	Method          string
	Input           any
	Prepare         func() error
	Apply           func(*Model, json.RawMessage, error)
	epoch           string
}
type Controller struct {
	ExpectedDeviceID  string
	RestoreOperations []string
	Directory         string
	Jobs              chan Job
	Updates           chan Update
	mu                sync.Mutex
	epoch             string
	invalidate        func()
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
	c.mu.Lock()
	job.epoch = job.ConnectionEpoch
	if job.epoch == "" {
		job.epoch = c.epoch
	}
	c.mu.Unlock()
	select {
	case c.Jobs <- job:
		return true
	default:
		return false
	}
}
func (c *Controller) Select(id string) { c.Do(Job{Method: "$select", Input: id}) }
func executeJob(ctx context.Context, client *protocol.Client, job Job) (json.RawMessage, error) {
	if client == nil || job.epoch == "" || job.epoch != client.Epoch || ctx.Err() != nil {
		return nil, &protocol.RemoteError{Code: "notSent", Message: "连接已改变，操作未发送；请重新提交"}
	}
	if job.Prepare != nil {
		if err := job.Prepare(); err != nil {
			return nil, &protocol.RemoteError{Code: "notSent", Message: "无法保存草稿，操作未发送：" + err.Error()}
		}
	}
	var out json.RawMessage
	err := client.Call(ctx, job.Method, job.Input, &out)
	return out, err
}
func reconcileOperations(ctx context.Context, client *protocol.Client, ids map[string]bool, emit func(Update)) error {
	if client == nil || ctx.Err() != nil {
		return net.ErrClosed
	}
	for id := range ids {
		var op domain.Operation
		err := client.Call(ctx, "operation", struct {
			ID string `json:"id"`
		}{id}, &op)
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && remote.Code == "notFound" {
				delete(ids, id)
				key := id
				emit(func(m *Model) {
					if in, ok := m.Pending[key]; ok {
						m.Reject(in, "未找到已接纳的提交，草稿已保留")
					}
				})
				continue
			}
			return err
		}
		emit(func(m *Model) { m.ApplyOperation(op) })
		if op.State == "confirmed" || op.State == "rejected" || op.State == "indeterminate" {
			delete(ids, id)
		}
	}
	return nil
}

// Pages may overlap writes. Replay every event after the first page barrier before
// publishing the assembled history; a later page cannot vouch for an earlier page.
func hydrateSession(ctx context.Context, client *protocol.Client, id string, generation ...string) (domain.SessionDetail, error) {
	var all domain.SessionDetail
	var after, first int64
	turns := map[string]domain.Turn{}
	requests := map[string]domain.PendingRequest{}
	gen := ""
	if len(generation) > 0 {
		gen = generation[0]
	}
	for {
		var page domain.SessionDetail
		if err := client.Call(ctx, "session", protocol.SessionQuery{ID: id, After: after, Limit: 100}, &page); err != nil {
			return all, err
		}
		if first == 0 {
			first = page.Sequence
		}
		all.Session = page.Session
		all.Sequence = page.Sequence
		all.Items = append(all.Items, page.Items...)
		for _, t := range page.Turns {
			turns[t.ID] = t
		}
		for _, r := range page.Requests {
			requests[r.ID] = r
		}
		if !page.HasMore {
			break
		}
		if len(page.Items) == 0 || page.Items[len(page.Items)-1].Order <= after {
			return all, errors.New("history page did not advance")
		}
		after = page.Items[len(page.Items)-1].Order
	}
	for _, t := range turns {
		all.Turns = append(all.Turns, t)
	}
	for _, r := range requests {
		all.Requests = append(all.Requests, r)
	}
	barrier := all.Sequence
	all.Sequence = first
	projection := NewModel()
	projection.Sequence = first
	projection.Details[id] = all
	projection.Snapshot.Sessions = []domain.Session{all.Session}
	for projection.Sequence < barrier {
		var page protocol.EventPage
		if err := client.Call(ctx, "events", protocol.EventsQuery{After: projection.Sequence, Generation: gen}, &page); err != nil {
			return all, err
		}
		if len(page.Events) == 0 {
			return all, errors.New("history barrier requires missing journal events")
		}
		for _, e := range page.Events {
			if e.Sequence > barrier {
				break
			}
			if err := projection.ApplyEvent(e); err != nil {
				return all, err
			}
		}
	}
	all = projection.Details[id]
	all.Sequence = barrier
	sort.Slice(all.Items, func(i, j int) bool { return all.Items[i].Order < all.Items[j].Order })
	sort.Slice(all.Requests, func(i, j int) bool { return all.Requests[i].ID < all.Requests[j].ID })
	sort.Slice(all.Turns, func(i, j int) bool {
		a, b := all.Turns[i].StartedAt, all.Turns[j].StartedAt
		if a != nil && b != nil && !a.Equal(*b) {
			return a.Before(*b)
		}
		return all.Turns[i].ID < all.Turns[j].ID
	})
	return all, nil
}
func (c *Controller) Run(ctx context.Context) {
	var client *protocol.Client
	var generation string
	var sequence int64
	var selected, loaded string
	var lastReadiness time.Time
	unconfirmed := map[string]bool{}
	for _, id := range c.RestoreOperations {
		unconfirmed[id] = true
	}
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
		c.mu.Lock()
		c.epoch = ""
		c.mu.Unlock()
		c.post(func(m *Model) {
			m.Connected = false
			m.ConnectionEpoch = ""
		})
	}
	fetchDetail := func() error {
		if selected == "" || selected == loaded {
			return nil
		}
		all, err := hydrateSession(ctx, client, selected, generation)
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) {
				loaded = selected
				c.post(func(m *Model) { m.Error = err.Error() })
				return nil
			}
			return err
		}
		loaded = selected
		id := selected
		c.post(func(m *Model) {
			m.Details[id] = all
			for i, s := range m.Snapshot.Sessions {
				if s.ID == all.Session.ID && s.Revision <= all.Session.Revision {
					m.Snapshot.Sessions[i] = all.Session
				}
			}
		})
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
			var opID string
			if in, ok := job.Input.(domain.Submission); ok {
				opID = in.OperationID
				unconfirmed[opID] = true
			}
			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			out, err := executeJob(callCtx, client, job)
			cancel()
			var remote *protocol.RemoteError
			if err != nil && errors.As(err, &remote) {
				delete(unconfirmed, opID)
			}
			if job.Apply != nil {
				c.post(func(m *Model) { job.Apply(m, out, err) })
			}
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
				c.mu.Lock()
				c.epoch = client.Epoch
				c.mu.Unlock()
				epoch := client.Epoch
				c.post(func(m *Model) {
					m.Connected = true
					m.ConnectionEpoch = epoch
					m.Readiness = ready
					m.ApplySnapshot(snapshot)
				})
				loaded = ""
				if err := reconcileOperations(ctx, client, unconfirmed, c.post); err != nil {
					disconnected()
					continue
				}
			}
			if time.Since(lastReadiness) > 5*time.Second {
				var ready domain.Readiness
				if err := client.Call(ctx, "readiness", nil, &ready); err != nil {
					disconnected()
					continue
				}
				lastReadiness = time.Now()
				c.post(func(m *Model) { m.Readiness = ready })
			}
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
				for _, e := range events {
					if e.Operation != nil && (e.Operation.State == "confirmed" || e.Operation.State == "rejected" || e.Operation.State == "indeterminate") {
						delete(unconfirmed, e.Operation.ID)
					}
				}
				c.post(func(m *Model) {
					for _, e := range events {
						if err := m.ApplyEvent(e); err != nil {
							m.Error = err.Error()
							m.Connected = false
							m.ConnectionEpoch = ""
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
