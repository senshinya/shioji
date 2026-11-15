package main

import "sync/atomic"

// Keep the native event loop available while cleanup waits: an already queued
// browser-open callback can still need that loop before its stream closes.
type quitBarrier struct {
	started bool // Main thread only.
	ready   atomic.Bool
}

func (b *quitBarrier) request(cancel func(), done <-chan struct{}, quit func()) bool {
	if b.ready.Load() {
		return true
	}
	if !b.started {
		b.started = true
		cancel()
		go func() {
			<-done
			b.ready.Store(true)
			quit()
		}()
	}
	return false
}
