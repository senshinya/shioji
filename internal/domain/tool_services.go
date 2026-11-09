package domain

import (
	"net/url"
	"strconv"
)

// Tool-service identities belong to an owning harness/runtime. Consumers must
// return them unchanged rather than parse source-specific server identifiers.
type ToolServiceState string

const (
	ToolServiceUnknown               ToolServiceState = "unknown"
	ToolServiceConnecting            ToolServiceState = "connecting"
	ToolServiceReady                 ToolServiceState = "ready"
	ToolServiceAuthorizationRequired ToolServiceState = "authorizationRequired"
	ToolServiceFailed                ToolServiceState = "failed"
	ToolServiceDisabled              ToolServiceState = "disabled"
)

type ToolService struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Origin       string           `json:"origin,omitempty"`
	State        ToolServiceState `json:"state"`
	Tools        int              `json:"tools"`
	CanAuthorize bool             `json:"canAuthorize"`
}
type ToolServiceCatalog struct {
	Services      []ToolService             `json:"services"`
	CanRefresh    bool                      `json:"canRefresh"`
	Authorization *ToolServiceAuthorization `json:"authorization,omitempty"`
}

// Authorization challenges are transient, like primary account challenges.
type ToolServiceAuthorization struct {
	ID        string          `json:"id"`
	HarnessID string          `json:"harnessId"`
	ServiceID string          `json:"serviceId"`
	Phase     SignInPhase     `json:"phase"`
	Challenge SignInChallenge `json:"challenge"`
	Message   string          `json:"message,omitempty"`
}

// Browser authorization may use HTTPS, or an explicitly local development
// provider. Credentials and non-loopback plaintext endpoints are rejected.
func ValidAuthorizationURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil || len(value) > 16<<10 || u.Hostname() == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	port := 80
	if u.Port() != "" {
		port, _ = strconv.Atoi(u.Port())
	}
	return (LoopbackEndpoint{Host: u.Hostname(), Port: port}).Valid()
}
