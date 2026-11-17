package managedexec

import (
	"context"
	"os"
	"testing"
)

func TestPauseAdmissionRestoresOnlyCapturedActiveGeneration(t *testing.T) {
	m, err := New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := m.Begin("session")
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	undo, err := scope.PauseAdmission()
	if err != nil {
		t.Fatal(err)
	}
	if scope.Admitting() {
		t.Fatal("paused scope still admitting")
	}
	if _, err = scope.Prepare("worker", "new"); err == nil {
		t.Fatal("pause admitted a new tool")
	}
	if err = undo(); err != nil {
		t.Fatal(err)
	}
	if !scope.Admitting() {
		t.Fatal("active generation did not resume")
	}
	undo, err = scope.PauseAdmission()
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, err := m.Begin("session")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Stop(context.Background())
	if err = undo(); err == nil {
		t.Fatal("undo revived retired generation")
	}
	if !fresh.Admitting() {
		t.Fatal("old undo changed successor")
	}
}
