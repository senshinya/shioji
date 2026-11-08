package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupBuildAdmissionRunsAfterOwnershipBeforeDatabaseOpen(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	called := false
	wrong := errors.New("selected artifact changed")
	owner, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(context.Background(), Config{Directory: dir, CheckStartupBuild: func() error { called = true; return wrong }})
	if !errors.Is(err, ErrOwned) || called {
		t.Fatal("startup selection bypassed owner", err, called)
	}
	owner.Close()
	_, err = New(context.Background(), Config{Directory: dir, CheckStartupBuild: func() error { called = true; return wrong }})
	if !errors.Is(err, wrong) || !called {
		t.Fatal("wrong build was admitted", err, called)
	}
	if _, err = os.Stat(filepath.Join(dir, "state.sqlite")); !os.IsNotExist(err) {
		t.Fatal("unselected build opened database", err)
	}
	owner, err = Acquire(dir)
	if err != nil {
		t.Fatal("selection failure leaked owner", err)
	}
	owner.Close()
}
