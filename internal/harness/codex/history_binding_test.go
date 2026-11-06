package codex

import (
	"testing"
)

func TestHistorySwitchFencesOldThreadEvents(t *testing.T) {
	r := &Runtime{normalizer: NewNormalizer()}
	r.normalizer.Bind("old", "session", false)
	if err := r.SwitchHistory("old", "new", "session", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, thread := range []string{"old", "new"} {
		raw := []byte(`{"method":"turn/started","params":{"threadId":"` + thread + `","turn":{"id":"turn-` + thread + `","status":"inProgress","items":[]}}}`)
		events, _, err := r.normalizer.Feed(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			if thread == "old" && e.SessionID != "" {
				t.Fatal("old source resurrected history", e)
			}
			if thread == "new" && e.SessionID != "session" {
				t.Fatal("new source not bound")
			}
		}
	}

}
