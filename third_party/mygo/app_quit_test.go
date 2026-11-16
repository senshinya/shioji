package mygo

import (
	"testing"

	"github.com/egoist/mygo/internal/fake"
)

func TestDeferredQuitPreservesOriginalAction(t *testing.T) {
	for _, phase := range []string{"before", "will"} {
		for _, restart := range []bool{false, true} {
			name := phase + "/quit"
			if restart {
				name = phase + "/relaunch"
			}
			t.Run(name, func(t *testing.T) { testDeferredQuit(t, phase, restart) })
		}
	}
}

func testDeferredQuit(t *testing.T, phase string, restart bool) {
	b := fake.New()
	backend()
	previousBackend, previousGrace, previousApp := theBackend, stopGrace, App
	previousStopped := nativeCalls.stopped
	theBackend, stopGrace = b, 0
	t.Cleanup(func() {
		theBackend, stopGrace, App = previousBackend, previousGrace, previousApp
		nativeCalls.stopped = previousStopped
	})
	a := &Application{initialized: true}
	App = a
	var cleanupReady bool
	var retry func()
	listen := a.OnWillQuit
	if phase == "before" {
		listen = a.OnBeforeQuit
	}
	listen(func(e *QuitEvent) {
		if !cleanupReady {
			e.PreventDefault()
			retry = e.Retry
		}
	})
	if restart {
		a.Relaunch()
	} else {
		a.Quit()
	}
	loop.drain()
	if a.stopping || a.relaunch || b.Quits != 0 {
		t.Fatal("quit did not wait for cleanup")
	}
	cleanupReady = true
	retry()
	loop.drain()
	if !a.stopping || a.relaunch != restart || b.Quits != 1 {
		t.Fatalf("cleanup lost the requested restart: stopping=%v relaunch=%v quits=%d", a.stopping, a.relaunch, b.Quits)
	}
}
