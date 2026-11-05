package app

import (
	"context"
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"sort"
)

// Search is read-only and spans only the devices this client explicitly knows.
// Publish available results while slower/offline devices finish independently.
func (c *Controller) searchDevices(ctx context.Context, job Job) {
	keys := make([]string, 0, len(c.fleet.sources))
	for key := range c.fleet.sources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	remaining, success := len(keys), 0
	results := map[string][]domain.SearchResult{}
	var firstError error
	finish := func(key string, body json.RawMessage, err error) {
		c.postContext(ctx, func(m *Model) {
			remaining--
			if err == nil {
				var found []domain.SearchResult
				err = json.Unmarshal(body, &found)
				if err == nil {
					err = m.validateSearchResults(key, found)
				}
				if err == nil {
					results[key] = found
					success++
				}
			}
			if err != nil && firstError == nil {
				firstError = err
			}
			if job.Apply == nil {
				return
			}
			if success > 0 {
				all := []domain.SearchResult{}
				for _, key := range keys {
					all = append(all, results[key]...)
				}
				b, _ := json.Marshal(all)
				job.Apply(m, b, nil)
			} else if remaining == 0 {
				job.Apply(m, nil, firstError)
			}
		})
	}
	for _, key := range keys {
		source := c.fleet.sources[key]
		delegated := Job{Method: "session.search", Input: job.Input, Apply: func(_ *Model, b json.RawMessage, err error) { finish(key, b, err) }}
		if !source.controller.Do(delegated) {
			finish(key, nil, &protocol.RemoteError{Code: "notSent", Message: "设备搜索繁忙"})
		}
	}
}
