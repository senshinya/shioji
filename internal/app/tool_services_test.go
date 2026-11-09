package app

import (
	"context"
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestToolAuthorizationValidatesOwnerBeforeForwarding(t *testing.T) {
	c := NewController("")
	in := protocol.ToolServiceInput{ID: "flow", HarnessID: "custom", ServiceID: "private"}
	status := domain.ToolServiceAuthorization{ID: "flow", HarnessID: "other", ServiceID: "private", Phase: domain.SignInWaiting, Challenge: domain.SignInChallenge{URL: "https://example.test"}}
	body, _ := json.Marshal(status)
	if _, err := c.prepareToolServiceReply(context.Background(), transport.SSH{}, "device", "runtime.tools.auth.status", in, body); err == nil {
		t.Fatal("cross-harness authorization accepted")
	}
	status.HarnessID = "custom"
	status.ServiceID = "other"
	body, _ = json.Marshal(status)
	if _, err := c.prepareToolServiceReply(context.Background(), transport.SSH{}, "device", "runtime.tools.auth.status", in, body); err == nil {
		t.Fatal("cross-service authorization accepted")
	}
}

func TestToolAuthorizationDoesNotExposeBrowserURLBeforeRemoteForward(t *testing.T) {
	c := NewController("")
	defer c.signInForwards.Close()
	in := protocol.ToolServiceInput{ID: "flow", HarnessID: "custom", ServiceID: "private"}
	status := domain.ToolServiceAuthorization{ID: in.ID, HarnessID: in.HarnessID, ServiceID: in.ServiceID, Phase: domain.SignInWaiting, Challenge: domain.SignInChallenge{URL: "https://example.test/authorize", Callback: &domain.LoopbackEndpoint{Host: "localhost", Port: 19883}}}
	body, _ := json.Marshal(status)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result, err := c.prepareToolServiceReply(ctx, transport.SSH{Target: "nobody@127.0.0.1", Port: 1}, "remote", "runtime.tools.auth.status", in, body)
	if err != nil {
		t.Fatal(err)
	}
	status = domain.ToolServiceAuthorization{}
	if err := json.Unmarshal(result, &status); err != nil {
		t.Fatal(err)
	}
	if status.Challenge.URL != "" || status.Phase != domain.SignInWaiting || status.ID != in.ID {
		t.Fatal("unsafe browser URL or operation replay", status)
	}
}
