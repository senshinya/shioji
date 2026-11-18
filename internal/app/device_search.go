package app

import (
	"context"
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"sort"
)

// SearchProgress describes the results and the remaining work for one client
// search across its devices. Workers still return their ordinary result lists.
type SearchProgress struct {
	Results         []domain.SearchResult
	Pending, Failed int
	Error           error
}

// Search is read-only and spans only the devices this client explicitly knows.
// Publish available results while slower/offline devices finish independently.
func (c *Controller) searchDevices(ctx context.Context, job Job) {
	keys := make([]string, 0, len(c.fleet.sources))
	for key := range c.fleet.sources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	remaining, success, failed := len(keys), 0, 0
	results := map[string][]domain.SearchResult{}
	var firstError error
	if remaining == 0 {
		err := errors.New("没有可搜索的设备")
		c.postContext(ctx, func(m *Model) {
			if job.SearchProgress != nil {
				job.SearchProgress(m, SearchProgress{Error: err})
			} else if job.Apply != nil {
				job.Apply(m, nil, err)
			}
		})
		return
	}
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
			if err != nil {
				failed++
				if firstError == nil {
					firstError = err
				}
			}
			if job.SearchProgress != nil {
				progress := SearchProgress{Pending: remaining, Failed: failed}
				for _, key := range keys {
					progress.Results = append(progress.Results, results[key]...)
				}
				if remaining == 0 && success == 0 {
					progress.Error = firstError
				}
				job.SearchProgress(m, progress)
				return
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
