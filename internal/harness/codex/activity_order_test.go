package codex

import (
	"encoding/json"
	"testing"
)

func TestOnlyItemStartsClaimActivity(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "s", true)
	for _, tc := range []struct {
		packet string
		start  bool
	}{
		{`{"method":"item/started","params":{"threadId":"thread","turnId":"t","item":{"id":"tool","type":"commandExecution","command":"check"}}}`, true},
		{`{"method":"item/commandExecution/outputDelta","params":{"threadId":"thread","turnId":"t","itemId":"tool","delta":"output"}}`, false},
		{`{"method":"item/completed","params":{"threadId":"thread","turnId":"t","item":{"id":"tool","type":"commandExecution","command":"check","status":"completed"}}}`, false},
		{`{"method":"item/started","params":{"threadId":"thread","turnId":"t","item":{"id":"compact","type":"contextCompaction"}}}`, true},
	} {
		events, _, err := n.Feed([]byte(tc.packet))
		if err != nil || len(events) != 1 {
			t.Fatal(events, err)
		}
		b, _ := json.Marshal(events[0])
		var e struct {
			ActivityStart bool `json:"activityStart"`
		}
		if err := json.Unmarshal(b, &e); err != nil {
			t.Fatal(err)
		}
		if e.ActivityStart != tc.start {
			t.Errorf("incorrect activity transition: %s", b)
		}
	}
}
