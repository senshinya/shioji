package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestQuitBarrierKeepsEventLoopAliveUntilControllerCleanup(t *testing.T) {
	var barrier quitBarrier
	var cancelled atomic.Int32
	done, retry := make(chan struct{}), make(chan struct{}, 1)
	cancel := func() { cancelled.Add(1) }
	quit := func() { retry <- struct{}{} }
	if barrier.request(cancel, done, quit) {
		t.Fatal("quit accepted before controller shutdown")
	}
	if barrier.request(cancel, done, quit) {
		t.Fatal("duplicate quit skipped cleanup")
	}
	if cancelled.Load() != 1 {
		t.Fatal("cancellation must begin once")
	}
	select {
	case <-retry:
		t.Fatal("quit retried before cleanup")
	default:
	}
	close(done)
	select {
	case <-retry:
	case <-time.After(time.Second):
		t.Fatal("quit was not retried after cleanup")
	}
	if !barrier.request(cancel, done, quit) {
		t.Fatal("completed shutdown blocked quit")
	}
}
