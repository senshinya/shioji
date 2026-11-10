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
	Transport      string                    `json:"transport,omitempty"`
	Editable       bool                      `json:"editable,omitempty"`
	ReadOnlyReason string                    `json:"readOnlyReason,omitempty"`
	Configuration  *ToolServiceConfiguration `json:"configuration,omitempty"`
	ID             string                    `json:"id"`
	Name           string                    `json:"name"`
	Origin         string                    `json:"origin,omitempty"`
	State          ToolServiceState          `json:"state"`
	Tools          int                       `json:"tools"`
	CanAuthorize   bool                      `json:"canAuthorize"`
}
type ToolServiceCatalog struct {
	CanAdd        bool                      `json:"canAdd,omitempty"`
	Revision      string                    `json:"revision,omitempty"`
	Services      []ToolService             `json:"services"`
	CanRefresh    bool                      `json:"canRefresh"`
	Authorization *ToolServiceAuthorization `json:"authorization,omitempty"`
}

type ToolServiceScope string

const (
	ToolServiceProject ToolServiceScope = "project"
	ToolServiceUser    ToolServiceScope = "user"
	ToolServiceManaged ToolServiceScope = "managed"
	ToolServiceOther   ToolServiceScope = "other"
)

// Inspection is read-only. EntryID identifies a source row; ID remains the
// adapter's opaque service reference. RuntimeKnown requires a session observation.
type InspectedToolService struct {
	ToolService
	EntryID           string                  `json:"entryId"`
	Scope             ToolServiceScope        `json:"scope"`
	ConfigurationPath string                  `json:"configurationPath,omitempty"`
	IgnoredReason     string                  `json:"ignoredReason,omitempty"`
	Definitions       []ToolServiceDefinition `json:"definitions,omitempty"`
}
type ToolServiceDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type ToolServiceInspection struct {
	Directory    string                 `json:"directory"`
	RuntimeKnown bool                   `json:"runtimeKnown"`
	Entries      []InspectedToolService `json:"entries"`
	Warnings     []string               `json:"warnings,omitempty"`
}

// Retained values are opaque existing secrets. An unchanged row refers back to
// its owning adapter; catalog reads never send the secret to another device.
type ToolServiceValue struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Retained bool   `json:"retained,omitempty"`
}
type ToolServiceConfiguration struct {
	Name                 string             `json:"name"`
	Transport            string             `json:"transport"`
	Enabled              bool               `json:"enabled"`
	Command              string             `json:"command,omitempty"`
	Arguments            []string           `json:"arguments,omitempty"`
	Environment          []ToolServiceValue `json:"environment,omitempty"`
	InheritedEnvironment []string           `json:"inheritedEnvironment,omitempty"`
	Directory            string             `json:"directory,omitempty"`
	URL                  string             `json:"url,omitempty"`
	TokenEnvironment     string             `json:"tokenEnvironment,omitempty"`
	Headers              []ToolServiceValue `json:"headers,omitempty"`
	StartupSeconds       float64            `json:"startupSeconds,omitempty"`
}
type ToolServiceChange struct {
	ServiceID     string                    `json:"serviceId,omitempty"`
	Revision      string                    `json:"revision"`
	Action        string                    `json:"action"` // save, delete, or setEnabled
	Enabled       bool                      `json:"enabled,omitempty"`
	Configuration *ToolServiceConfiguration `json:"configuration,omitempty"`
}
type ToolServiceChangeResult struct {
	ServiceID string `json:"serviceId,omitempty"`
	Saved     bool   `json:"saved"`
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
