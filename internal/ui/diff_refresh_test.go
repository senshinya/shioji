package ui

import (
	"encoding/json"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
)

func TestDiffRefreshHasOneAtomicOwner(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor, m.Connected, m.ConnectionEpoch = 33, true, "epoch"
	controller := app.NewController("")
	v := New(m, controller, "")
	s := v.changesFor(m.SessionID)
	s.Comparison = domain.DefaultDiffComparison()
	s.Path = "file.go"
	s.load = changesLoaded
	v.refreshChanges()
	job := <-controller.Jobs
	if job.Method != "git.compare" || len(controller.Jobs) != 0 {
		t.Fatal("refresh must own one atomic comparison", job.Method, len(controller.Jobs))
	}
}

func TestChangingFileDuringComparisonRejectsPreviousFile(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor, m.Connected, m.ConnectionEpoch = 33, true, "epoch"
	controller := app.NewController("")
	v := New(m, controller, "")
	s := v.changesFor(m.SessionID)
	s.Path = "first.go"
	s.load = changesLoaded
	pair := domain.DefaultDiffComparison()
	v.requestComparison(s, pair)
	old := <-controller.Jobs
	v.openChangedFile("last.go")
	latest := <-controller.Jobs
	if latest.Method != "git.compare" {
		t.Fatalf("file change must join pending comparison, got %s", latest.Method)
	}
	apply := func(job app.Job, path string) {
		b, _ := json.Marshal(domain.ComparisonResult{Comparison: pair, Versions: &domain.FileVersions{Path: path, Comparison: pair, Old: "old", New: path}})
		job.Apply(m, b, nil)
	}
	apply(latest, "last.go")
	apply(old, "first.go")
	if s.Path != "last.go" || s.Versions.Path != "last.go" {
		t.Fatal("old comparison replaced selected file", s.Path, s.Versions.Path)
	}
}
