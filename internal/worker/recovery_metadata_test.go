package worker

import (
	"fmt"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestHealthyRecoveryCheckCostDoesNotGrowWithWorkHistory(t *testing.T) {
	s, w, _ := gitTestServer(t)
	op, err := s.store.Accept(domain.Submission{OperationID: "metadata-only", WorkspaceID: w.ID, Text: "keep running"})
	if err != nil {
		t.Fatal(err)
	}
	before := testing.AllocsPerRun(3, func() { s.recoverSession(op.SessionID) })
	for i := 0; i < 256; i++ {
		_, err := s.store.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{
			ID: fmt.Sprintf("old-%d", i), Kind: "agent", TurnID: "old-turn", State: "completed",
			Brief: strings.Repeat("old work ", 800), Latest: strings.Repeat("output ", 500),
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	after := testing.AllocsPerRun(3, func() { s.recoverSession(op.SessionID) })
	t.Logf("healthy recovery: empty %.0f allocs, retained history %.0f", before, after)
	// A healthy session needs only an ownership/status check. Allow fixed
	// bookkeeping overhead, but never decode a roster of old tool/agent output.
	if after > before+500 {
		t.Fatalf("healthy recovery decoded work history: empty %.0f allocs, retained history %.0f", before, after)
	}
}

func TestReferenceSearchCostDoesNotGrowWithWorkHistory(t *testing.T) {
	s, w, _ := gitTestServer(t)
	op, err := s.store.Accept(domain.Submission{OperationID: "reference-metadata", WorkspaceID: w.ID, Text: "search in accepted directories"})
	if err != nil {
		t.Fatal(err)
	}
	query := protocol.GitQuery{WorkspaceID: w.ID, SessionID: op.SessionID}
	measure := func() float64 {
		return testing.AllocsPerRun(3, func() {
			roots, err := s.referenceRoots(query)
			if err != nil || len(roots) != 1 || roots[0] != w.Folders[0] {
				t.Fatalf("reference search lost its owning execution directory: %v, %v", roots, err)
			}
		})
	}
	before := measure()
	for i := 0; i < 256; i++ {
		_, err := s.store.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{
			ID: fmt.Sprintf("old-%d", i), Kind: "agent", TurnID: "old-turn", State: "completed",
			Brief: strings.Repeat("old work ", 800), Latest: strings.Repeat("output ", 500),
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	after := measure()
	t.Logf("reference roots: empty %.0f allocs, retained history %.0f", before, after)
	if after > before+500 {
		t.Fatalf("file/Skill search decoded unrelated work history: empty %.0f allocs, retained history %.0f", before, after)
	}
}
