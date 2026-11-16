package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// Periodic per-session polling must not multiply full-history scans while
// holding the shared Store/dispatch locks. Check the real production query.
func TestRuntimeIdleUsesSessionIndexesForHistoricalOperations(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rows, e := s.db.Query("EXPLAIN QUERY PLAN "+runtimeIdleQuery, "session", false)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var scans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(detail, "SCAN operations") || strings.Contains(detail, "SCAN work_inputs") || strings.Contains(detail, "SCAN history_operations") {
			scans = append(scans, detail)
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if len(scans) > 0 {
		t.Fatal("idle polling scans unrelated historical operations", scans)
	}
}
