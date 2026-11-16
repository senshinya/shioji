package store

import (
	"path/filepath"
	"testing"
)

func TestArchiveAcceptanceDurablySchedulesRuntimeRetirement(t *testing.T) {
	s, _, session := archiveFixture(t)
	archived, err := s.SetSessionArchived(session.ID, session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	err = s.db.QueryRow("SELECT count(*) FROM runtime_retirements WHERE session_id=?", session.ID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatal("archive accepted without durable runtime cleanup", count, err)
	}
	path := s.path
	s.Close()
	reopened, err := Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.SetSessionArchived(session.ID, archived.Revision, false); err != nil {
		t.Fatal(err)
	}
	err = reopened.db.QueryRow("SELECT count(*) FROM runtime_retirements WHERE session_id=?", session.ID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatal("restore discarded unfinished retirement", count, err)
	}
	d, err := reopened.Session(session.ID, 0, 100)
	if err != nil || len(d.Items) == 0 {
		t.Fatal("retirement deleted saved history", err)
	}
}
