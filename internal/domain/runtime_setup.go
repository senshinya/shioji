package domain

import (
	"net"
	"time"
)

type RuntimeIssue string

const (
	RuntimeMissing               RuntimeIssue = "missing"
	RuntimeSignInRequired        RuntimeIssue = "signInRequired"
	RuntimeAuthenticationExpired RuntimeIssue = "authenticationExpired"
	RuntimeIncompatible          RuntimeIssue = "incompatible"
	RuntimeUnavailableIssue      RuntimeIssue = "unavailable"
)

type RuntimeInstallation struct {
	APISetup         bool                 `json:"apiSetup,omitempty"`
	SignInMethods    []SignInMethod       `json:"signInMethods,omitempty"`
	Options          RuntimeOptions       `json:"options"`
	OptionsSupported RuntimeOptionSupport `json:"optionsSupported"`
	Executable       string               `json:"executable,omitempty"`
	Override         string               `json:"override,omitempty"`
	Configurable     bool                 `json:"configurable"`
	Issue            RuntimeIssue         `json:"issue,omitempty"`
	Guide            string               `json:"guide,omitempty"`
	Documentation    string               `json:"documentation,omitempty"`
	SignInCommand    []string             `json:"signInCommand,omitempty"`
}

type SignInMethod string

const (
	SignInAPI        SignInMethod = "api"
	SignInBrowser    SignInMethod = "browser"
	SignInDeviceCode SignInMethod = "deviceCode"
)

// Authorization challenges are transient. Never persist them in client state,
// conversation history, diagnostics or the worker journal.
type SignInChallenge struct {
	ManualCode bool              `json:"manualCode,omitempty"`
	Callback   *LoopbackEndpoint `json:"callback,omitempty"`
	URL        string            `json:"url,omitempty"`
	Code       string            `json:"code,omitempty"`
}

// LoopbackEndpoint describes a temporary callback listener on the owning
// device. Concrete OAuth parameters and redirect parsing remain in adapters.
type LoopbackEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func (e LoopbackEndpoint) Valid() bool {
	ip := net.ParseIP(e.Host)
	return e.Port > 0 && e.Port <= 65535 && (e.Host == "localhost" || ip != nil && ip.IsLoopback())
}

type SignInPhase string

const (
	SignInStarting  SignInPhase = "starting"
	SignInWaiting   SignInPhase = "waiting"
	SignInCompleted SignInPhase = "completed"
	SignInFailed    SignInPhase = "failed"
	SignInCanceled  SignInPhase = "canceled"
	SignInExpired   SignInPhase = "expired"
)

func (p SignInPhase) Active() bool { return p == SignInStarting || p == SignInWaiting }

type SignInStatus struct {
	Profile   *HarnessInfo    `json:"profile,omitempty"`
	ID        string          `json:"id"`
	HarnessID string          `json:"harnessId"`
	Phase     SignInPhase     `json:"phase"`
	Challenge SignInChallenge `json:"challenge"`
	Message   string          `json:"message,omitempty"`
}

// RuntimeOptions are device-owned defaults for subsequent harness launches.
// Argument syntax, authentication and source-specific configuration stay in adapters.
type RuntimeOptions struct {
	Arguments    []string              `json:"arguments,omitempty"`
	Directory    string                `json:"directory,omitempty"`
	Environment  []EnvironmentVariable `json:"environment,omitempty"`
	Instructions string                `json:"instructions,omitempty"`
	TitleModel   string                `json:"titleModel,omitempty"`
}
type EnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type RuntimeOptionSupport struct {
	Arguments    bool `json:"arguments,omitempty"`
	Directory    bool `json:"directory,omitempty"`
	Environment  bool `json:"environment,omitempty"`
	Instructions bool `json:"instructions,omitempty"`
	TitleModel   bool `json:"titleModel,omitempty"`
}

// RuntimeAccount contains public account metadata only; never credentials.
type RuntimeQuotaWindow struct {
	Group           string     `json:"group"`
	DurationSeconds int        `json:"durationSeconds"`
	UsedPercent     float64    `json:"usedPercent"`
	ResetsAt        *time.Time `json:"resetsAt,omitempty"`
}

type RuntimeAccount struct {
	QuotaWindows      []RuntimeQuotaWindow `json:"quotaWindows,omitempty"`
	Mode              string               `json:"mode,omitempty"`
	Endpoint          string               `json:"endpoint,omitempty"`
	Model             string               `json:"model,omitempty"`
	Credential        string               `json:"credential,omitempty"`
	ConfigurationFile string               `json:"configurationFile,omitempty"`
	Profile           string               `json:"profile,omitempty"`
	Subscription      string               `json:"subscription,omitempty"`
	Label             string               `json:"label"`
	Identity          string               `json:"identity,omitempty"`
	State             string               `json:"state"`
}

// APIConfiguration is an explicit, transient setup input. Credentials never enter Shioji persistence.
type APIConfiguration struct {
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	Credential string `json:"credential"`
}
