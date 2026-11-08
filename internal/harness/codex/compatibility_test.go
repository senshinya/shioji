package codex

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"shioji/internal/domain"
)

// Reduced from the installed 0.167.0 schema; these are actual required fields
// and property names, independent of the adapter's list of expected fields.
func compatibilityFixture(t *testing.T) protocolSchema {
	t.Helper()
	data, err := os.ReadFile("testdata/request-shapes-0167.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema protocolSchema
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}
func changeRequest(t *testing.T, schema *protocolSchema, method string, edit func(*requestShape)) {
	t.Helper()
	for i := range schema.OneOf {
		var value struct{ Enum []string }
		json.Unmarshal(schema.OneOf[i].Properties["method"], &value)
		if len(value.Enum) == 1 && value.Enum[0] == method {
			var params requestShape
			json.Unmarshal(schema.OneOf[i].Properties["params"], &params)
			edit(&params)
			schema.OneOf[i].Properties["params"], _ = json.Marshal(params)
			return
		}
	}
	t.Fatal("fixture lacks method", method)
}
func TestCompatibleUpgradeAllowsAdditionsButRejectsCoreContractChanges(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		edit         func(*requestShape)
		rejected     bool
	}{
		{"additive", "turn/start", func(s *requestShape) { s.Properties["futureOptional"] = json.RawMessage(`{}`) }, false},
		{"sandbox removed", "turn/start", func(s *requestShape) { delete(s.Properties, "sandboxPolicy") }, true},
		{"extra required", "thread/start", func(s *requestShape) {
			s.Properties["futureRequired"] = json.RawMessage(`{}`)
			s.Required = append(s.Required, "futureRequired")
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := compatibilityFixture(t)
			changeRequest(t, &schema, tc.method, tc.edit)
			data, _ := json.Marshal(schema)
			_, err := protocolFromSchema(data)
			if tc.rejected && (err == nil || !strings.Contains(err.Error(), tc.method)) {
				t.Fatal("missing useful incompatibility error", err)
			}
			if !tc.rejected && err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOptionalProtocolChangesDisableOnlyTheirCapabilities(t *testing.T) {
	schema := compatibilityFixture(t)
	for _, method := range []string{"skills/list", "turn/steer", "thread/fork", "collaborationMode/list"} {
		changeRequest(t, &schema, method, func(s *requestShape) { s.Required = append(s.Required, "futureRequired") })
	}
	data, _ := json.Marshal(schema)
	support, err := protocolFromSchema(data)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &adapterRuntime{Runtime: &Runtime{protocol: support, done: make(chan struct{}), ready: domain.Readiness{State: domain.RuntimeReady}}, descriptor: domain.HarnessDescriptor{ID: "codex"}}
	caps := runtime.Readiness().Capabilities
	if caps.Skills || caps.Steer || caps.Fork || caps.Restart || !caps.Interrupt || !caps.Recovery || !caps.Continue {
		t.Fatal("capabilities do not match actual protocol", caps)
	}
}
