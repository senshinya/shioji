package app

import (
	"context"
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"strings"
	"sync"
	"time"
)

// The controller owns browser callbacks; the owning worker still owns login and
// credentials. Closing a view never retires a callback, closing its controller does.
type signInForward struct {
	device   string
	ssh      transport.SSH
	endpoint domain.LoopbackEndpoint
	ctx      context.Context
	cancel   context.CancelFunc
	ready    chan struct{}
	forward  *transport.LoopbackForward
	err      error
}
type signInForwards struct {
	mu      sync.Mutex
	entries map[string]*signInForward
	closed  bool
}

func (r *signInForwards) prepare(ctx context.Context, ssh transport.SSH, device string, in protocol.SignInInput, status domain.SignInStatus) (domain.SignInStatus, error) {
	if status.ID != in.ID || status.HarnessID != in.HarnessID {
		return domain.SignInStatus{}, errors.New("登录回执与请求不一致")
	}
	if !status.Phase.Active() {
		return status, r.retire(status.ID, device)
	}
	if ssh.Target == "" || status.Phase != domain.SignInWaiting || status.Challenge.Callback == nil {
		return status, nil
	}
	callback := status.Challenge.Callback
	if !callback.Valid() {
		status.Challenge.URL = ""
		status.Message = "该 Agent 未提供可转发的回调，请使用验证码登录"
		return status, nil
	}
	if err := r.ensure(ctx, status.ID, device, ssh, *callback); err != nil {
		status.Challenge.URL = ""
		status.Message = err.Error()
	}
	return status, nil
}
func (r *signInForwards) ensure(ctx context.Context, id, device string, ssh transport.SSH, endpoint domain.LoopbackEndpoint) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("登录回调已关闭，请重新登录")
	}
	if r.entries == nil {
		r.entries = map[string]*signInForward{}
	}
	entry := r.entries[id]
	fresh := entry == nil
	if fresh {
		lifetime, cancel := context.WithTimeout(ctx, 10*time.Minute)
		entry = &signInForward{device: device, ssh: ssh, endpoint: endpoint, ctx: lifetime, cancel: cancel, ready: make(chan struct{})}
		r.entries[id] = entry
	} else if entry.device != device || entry.ssh != ssh || entry.endpoint != endpoint {
		r.mu.Unlock()
		return errors.New("登录回调与所属设备不一致")
	}
	r.mu.Unlock()
	if fresh {
		forward, err := ssh.ForwardLoopback(entry.ctx, endpoint)
		if err == nil {
			err = forward.Check(entry.ctx)
		}
		if err != nil && forward != nil {
			forward.Close()
		}
		entry.forward, entry.err = forward, err
		close(entry.ready)
	} else {
		select {
		case <-entry.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if entry.ctx.Err() != nil {
		return errors.New("登录回调已关闭，请重新登录")
	}
	return entry.err
}
func (r *signInForwards) retire(id, device string) error {
	r.mu.Lock()
	entry := r.entries[id]
	if entry != nil && entry.device != device {
		r.mu.Unlock()
		return errors.New("登录回调不属于该设备")
	}
	delete(r.entries, id)
	r.mu.Unlock()
	stopSignInForward(entry)
	return nil
}
func stopSignInForward(entry *signInForward) {
	if entry == nil {
		return
	}
	entry.cancel()
	<-entry.ready
	if entry.forward != nil {
		entry.forward.Close()
	}
}
func (r *signInForwards) Close() {
	r.mu.Lock()
	r.closed = true
	entries := r.entries
	r.entries = nil
	r.mu.Unlock()
	for _, entry := range entries {
		stopSignInForward(entry)
	}
}
func (c *Controller) deliverSignInReply(ctx context.Context, job Job, ssh transport.SSH, device string, body json.RawMessage, err error, done func(Job, json.RawMessage, error)) {
	if strings.HasPrefix(job.Method, "runtime.tools.") && job.Method != "runtime.tools.change" && err == nil {
		in, ok := job.Input.(protocol.ToolServiceInput)
		if !ok {
			done(job, nil, errors.New("工具服务请求无效"))
			return
		}
		go func() {
			result, e := c.prepareToolServiceReply(ctx, ssh, device, job.Method, in, body)
			done(job, result, e)
		}()
		return
	}
	in, ok := job.Input.(protocol.SignInInput)
	if !ok || err != nil {
		done(job, body, err)
		return
	}
	// This callback can be invoked while the UI drains child updates. Network
	// setup and callback retirement must remain outside that UI turn.
	go func() {
		var status domain.SignInStatus
		if json.Unmarshal(body, &status) != nil {
			done(job, nil, errors.New("登录结果无效"))
			return
		}
		result, e := c.signInForwards.prepare(ctx, ssh, device, in, status)
		encoded, _ := json.Marshal(result)
		done(job, encoded, e)
	}()
}
