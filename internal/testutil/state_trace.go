package testutil

import (
	"encoding/json"
	"testing"
)

// StateTrace records observable behavior. The same probes run against the
// pre-refactor revision; they must not inspect the new lifecycle representation.
func StateTrace(t *testing.T, step string, value any) {
	t.Helper()
	b, err := json.Marshal(struct {
		Test  string `json:"test"`
		Step  string `json:"step"`
		Value any    `json:"value"`
	}{t.Name(), step, value})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("STATE_EQ", string(b))
}
