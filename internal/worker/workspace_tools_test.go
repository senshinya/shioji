package worker

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"sync/atomic"
	"testing"
)

type inspectingHarness struct {
	memoryHarness
	starts *atomic.Int32
}

func (a inspectingHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	a.starts.Add(1)
	r, e := a.memoryHarness.Start(ctx, o)
	return &inspectingRuntime{Runtime: r}, e
}

type inspectingRuntime struct{ harness.Runtime }

func (r *inspectingRuntime) InspectToolServices(_ context.Context, l domain.LaunchContext, ref string) (domain.ToolServiceInspection, error) {
	return domain.ToolServiceInspection{Directory: l.CWD, RuntimeKnown: ref != "", Warnings: []string{ref}}, nil
}

func TestWorkspaceToolInspectionOwnership(t *testing.T) {
	starts := &atomic.Int32{}
	s, w := memoryWorker(t, inspectingHarness{memoryHarness: memoryHarness{id: "custom", hold: true}, starts: starts})
	in := protocol.ToolInspectionInput{WorkspaceID: w.ID, HarnessID: "custom"}
	got, err := s.inspectToolServices(in)
	if err != nil || got.Directory != w.Folders[0] || got.RuntimeKnown {
		t.Fatal(got, err)
	}
	in.Directory = t.TempDir()
	if _, err = s.inspectToolServices(in); err == nil {
		t.Fatal("foreign folder accepted")
	}
	in.Directory = ""
	op, err := s.submit(domain.Submission{WorkspaceID: w.ID, HarnessID: "custom", OperationID: "inspect", Text: "hold", Config: domain.RunConfig{Model: "custom-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	in.SessionID = op.SessionID
	got, err = s.inspectToolServices(in)
	if err != nil || !got.RuntimeKnown {
		t.Fatal(got, err)
	}
	in.HarnessID = "foreign"
	if _, err = s.inspectToolServices(in); err == nil {
		t.Fatal("foreign harness accepted")
	}
	in.HarnessID = "custom"
	in.WorkspaceID = "foreign"
	if _, err = s.inspectToolServices(in); err == nil {
		t.Fatal("foreign workspace accepted")
	}
	in.WorkspaceID = w.ID
	r := s.currentRuntime(op.SessionID)
	if err = r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := starts.Load()
	got, err = s.inspectToolServices(in)
	if err != nil || got.RuntimeKnown || starts.Load() != before {
		t.Fatal("inspection started or resumed execution", got, err, starts.Load())
	}
}
