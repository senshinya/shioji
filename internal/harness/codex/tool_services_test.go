package codex

import (
	"context"
	"shioji/internal/domain"
	"testing"
	"time"
)

func toolPipe(t *testing.T, mode string) *Runtime {
	r := loginPipe(t, mode)
	r.protocol.methods["mcpServerStatus/list"] = true
	r.protocol.methods["mcpServer/oauth/login"] = true
	r.protocol.methods["config/mcpServer/reload"] = true
	return r
}
func TestToolServicesUseNativeDiscoveryAndPreserveUnknownState(t *testing.T) {
	r := toolPipe(t, "tools")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Services) != 4 {
		t.Fatal(catalog)
	}
	states := map[string]domain.ToolServiceState{}
	for _, s := range catalog.Services {
		states[s.ID] = s.State
	}
	if states["docs"] != domain.ToolServiceReady || states["private"] != domain.ToolServiceAuthorizationRequired || states["empty"] != domain.ToolServiceUnknown || states["disabled"] != domain.ToolServiceDisabled {
		t.Fatal(states)
	}
	for _, service := range catalog.Services {
		if service.ID == "docs" && service.Tools != 1 {
			t.Fatal("tool inventory count lost")
		}
	}
}
func TestToolAuthorizationMatchesEarlySourceCompletionAndLoopback(t *testing.T) {
	r := toolPipe(t, "tools-early")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	login, challenge, err := r.BeginToolServiceAuthorization(ctx, "private")
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Callback == nil || challenge.Callback.Port != 1459 {
		t.Fatal("remote callback not exposed", challenge)
	}
	if err := login.Wait(ctx); err != nil {
		t.Fatal("early source completion lost", err)
	}
}
