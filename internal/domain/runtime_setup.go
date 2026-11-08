package domain

type RuntimeIssue string

const (
	RuntimeMissing               RuntimeIssue = "missing"
	RuntimeSignInRequired        RuntimeIssue = "signInRequired"
	RuntimeAuthenticationExpired RuntimeIssue = "authenticationExpired"
	RuntimeIncompatible          RuntimeIssue = "incompatible"
	RuntimeUnavailableIssue      RuntimeIssue = "unavailable"
)

type RuntimeInstallation struct {
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
type RuntimeAccount struct {
	Subscription string `json:"subscription,omitempty"`
	Label        string `json:"label"`
	Identity     string `json:"identity,omitempty"`
	State        string `json:"state"`
}
