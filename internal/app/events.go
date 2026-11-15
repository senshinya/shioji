package app

import (
	"context"
	"net"
	"time"

	"shioji/internal/protocol"
)

type eventResult struct {
	page protocol.EventPage
	err  error
}

// Event observation has its own authenticated connection so a parked watch
// never holds the command client's lock. Cursors, not wake notifications,
// carry delivery across bursts, slow readers and reconnects.
func (c *Controller) observeEvents(ctx context.Context, device, generation string, after int64) (<-chan eventResult, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	results := make(chan eventResult, 1)
	dial := c.dial
	if c.paths != nil {
		id := c.paths.active()
		dial = func(ctx context.Context) (net.Conn, error) { return c.paths.dialRoute(ctx, id) }
	}
	c.networkWG.Add(1)
	go func() {
		defer c.networkWG.Done()
		send := func(result eventResult) bool {
			select {
			case results <- result:
				return true
			case <-ctx.Done():
				return false
			}
		}
		conn, err := dial(ctx)
		if err != nil {
			send(eventResult{err: err})
			return
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		client, err := protocol.Connect(ctx, conn, device)
		if err != nil {
			send(eventResult{err: err})
			return
		}
		for ctx.Err() == nil {
			var page protocol.EventPage
			callCtx, done := context.WithTimeout(ctx, 20*time.Second)
			err = client.Call(callCtx, "events.watch", protocol.EventsQuery{After: after, Generation: generation, WaitMS: 15_000}, &page)
			done()
			if !send(eventResult{page: page, err: err}) || err != nil {
				return
			}
			if len(page.Events) > 0 {
				after = page.Events[len(page.Events)-1].Sequence
			}
		}
	}()
	return results, cancel
}
