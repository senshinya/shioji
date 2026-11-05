package codex

import "testing"

func TestThreadNameNotificationUpdatesOnlyBoundSession(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", false)
	events, _, err := n.Feed([]byte(`{"method":"thread/name/updated","params":{"threadId":"thread","threadName":" 接入任务侧栏 "}}`))
	if err != nil || len(events) != 1 || events[0].Type != "session.title" || events[0].SessionID != "session" || events[0].Text != "接入任务侧栏" || events[0].Item != nil {
		t.Fatal("name notification was not normalized", events, err)
	}
	for _, source := range []string{
		`{"method":"thread/name/updated","params":{"threadId":"other","threadName":"Other"}}`,
		`{"method":"thread/name/updated","params":{"threadId":"thread","threadName":null}}`,
		`{"method":"thread/name/updated","params":{"threadId":"thread","threadName":"  "}}`,
	} {
		events, _, err = n.Feed([]byte(source))
		if err != nil || len(events) != 0 {
			t.Fatal("unknown/empty name changed session", events, err)
		}
	}
}
