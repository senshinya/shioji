package codex

import (
	"shioji/internal/domain"
	"testing"
)

func TestSessionReloadCapabilityRequiresNativeUnloadAndHistory(t *testing.T) {
	for _, missing := range []string{"", "thread/read", "thread/turns/list", "thread/unsubscribe", "thread/loaded/list"} {
		t.Run("missing="+missing, func(t *testing.T) {
			methods := map[string]bool{"thread/read": true, "thread/turns/list": true, "thread/unsubscribe": true, "thread/loaded/list": true}
			delete(methods, missing)
			r := &adapterRuntime{Runtime: &Runtime{protocol: &protocolSupport{methods: methods}, done: make(chan struct{}), ready: domain.Readiness{State: domain.RuntimeReady}}, descriptor: domain.HarnessDescriptor{ID: "codex"}}
			ready := r.Readiness()
			if ready.Capabilities.Reload != (missing == "") || ready.Harnesses[0].Capabilities.Reload != (missing == "") {
				t.Fatal("reload advertised without its complete native lifecycle", ready.Capabilities)
			}
			if (missing == "thread/unsubscribe" || missing == "thread/loaded/list") && !ready.Capabilities.Continue {
				t.Fatal("missing unload unexpectedly disabled history continuation")
			}
		})
	}
}
