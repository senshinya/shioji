// Package harness is the device-side contract between Shioji orchestration and
// agent adapters. It contains no concrete harness protocol or implementation.
package harness

import (
	"context"
	"errors"
	"fmt"

	"shioji/internal/domain"
	"shioji/internal/managedexec"
)

type AttachmentInput struct {
	Attachment domain.Attachment
	Path       string
}

// HistoryBoundary stages a product history operation. PreparedRef resumes an
// already persisted preparation; it must not create a second history result.
// Restart selects the prefix before the edited input, Fork keeps the turn.
// Preparation must not mutate the source's accepted history. Activation invokes
// commit while excluding stale source events; Worker owns the durable commit.
type HistoryBoundary struct {
	TurnID      string
	Restart     bool
	PreparedRef string
}

type TurnInput struct {
	Text        string
	Attachments []AttachmentInput
	Skills      []domain.SkillReference
}
type SkillProvider interface {
	Skills(context.Context, domain.LaunchContext) (domain.SkillCatalog, error)
}

// TitleGenerator is an optional, independent auxiliary capability. The adapter
// owns its model selection, authentication and isolated execution mechanics.
// It must not append a turn to the user's conversation or run workspace tools.
type TitleGenerator interface {
	GenerateTitle(context.Context, TitleInput) (string, error)
}
type TitleInput struct {
	Prompt string
	Scope  *managedexec.Scope
}

// IncomingMessage is a host callback translated by an adapter. SourceRef is an
// opaque reference to the bound history, not a protocol payload.
type IncomingMessage struct {
	OperationID, SessionID, SourceRef, Text string
}

type StartOptions struct {
	// ImportResource snapshots device-local or inline image/text bytes in worker storage.
	// Adapters translate source encodings before calling this; journals carry only
	// the returned immutable reference, never bytes or a runtime storage path.
	ImportResource func(OutputResource) (domain.Attachment, error)
	OnMessage      func(context.Context, IncomingMessage) error
	Scope          *managedexec.Scope
	OnEvent        func(domain.Event) error
	OnStart        func(int) error
	OnLost         func() error
}

type OutputResource struct {
	Kind      string // image or file; Index is scoped to this kind within the item.
	SessionID string
	ItemID    string
	Index     int
	Name      string
	Path      string
	Data      []byte
}

// CatalogVersion is optional. An adapter reports the currently installed image;
// its runtime reports the immutable image captured at startup. Values are opaque
// to the worker, which only compares them before refreshing an idle catalog.
type CatalogVersion interface {
	CatalogVersion() (string, error)
}

type Adapter interface {
	Descriptor() domain.HarnessDescriptor
	Start(context.Context, StartOptions) (Runtime, error)
}

// Runtime is the accepted execution context, never the transport wire. Methods
// whose capabilities are absent return Unsupported without accepting work.
type Runtime interface {
	Readiness() domain.Readiness
	Validate(domain.RunConfig) error
	Create(context.Context, domain.Session) (string, error)
	Resume(context.Context, domain.Session) error
	Submit(context.Context, domain.Session, string, TurnInput) (string, error)
	Steer(context.Context, domain.Session, string, string, TurnInput) error
	Respond(domain.Answer) error
	Interrupt(context.Context, domain.Session, string) error
	Inspect(context.Context, domain.Session) (domain.RecoveryHistory, error)
	Observes(domain.Session) bool
	PrepareHistory(context.Context, domain.Session, HistoryBoundary) (string, error)
	ActivateHistory(context.Context, domain.Session, string, func() error) error
	ManagedCoverage() bool
	ExecutionReady() bool
	Stopped() bool
	// Done is a stable, non-nil channel unique to this started instance.
	Done() <-chan struct{}
	Close(context.Context) error
}

// DeliveryError classifies an external send without confusing unknown delivery
// with explicit rejection. It is shared semantics, not a native RPC error.
type DeliveryError struct {
	Uncertain bool
	Message   string
}

func (e *DeliveryError) Error() string { return e.Message }
func IsUncertain(err error) bool       { var e *DeliveryError; return errors.As(err, &e) && e.Uncertain }

type UnsupportedError struct{ Capability string }

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("harness does not support %s", e.Capability)
}
func Unsupported(capability string) error { return &UnsupportedError{Capability: capability} }

// InstallationProvider describes only the device-local executable and setup
// guidance. Native authentication and configuration encoding stay in adapters.
type InstallationProvider interface {
	Installation() domain.RuntimeInstallation
	WithExecutable(string) (Adapter, error)
}

// AuthenticationProvider owns the concrete authorization protocol and credential
// storage. Worker orchestration receives only a transient public challenge.
type AuthenticationProvider interface {
	BeginSignIn(context.Context, domain.SignInMethod) (SignIn, domain.SignInChallenge, error)
}
type SignIn interface {
	Wait(context.Context) (domain.Readiness, error)
	Cancel(context.Context) error
}
type APIConfigurationProvider interface {
	ConfigureAPI(context.Context, domain.APIConfiguration) (domain.Readiness, error)
}

type SetupError struct {
	Account *domain.RuntimeAccount
	Issue   domain.RuntimeIssue
	Message string
}

func (e *SetupError) Error() string { return e.Message }

// RuntimeOptionsProvider translates neutral launch defaults into an immutable adapter.
type RuntimeOptionsProvider interface {
	WithRuntimeOptions(domain.RuntimeOptions) (Adapter, error)
}

// ToolServiceRuntime manages the harness's effective device-owned services.
// Refresh never authorizes interrupting active turns; concrete adoption timing
// remains the adapter's responsibility.
type ToolServiceRuntime interface {
	ToolServices(context.Context) (domain.ToolServiceCatalog, error)
	RefreshToolServices(context.Context) error
	BeginToolServiceAuthorization(context.Context, string) (ToolServiceAuthorization, domain.SignInChallenge, error)
}
type ToolServiceAuthorization interface{ Wait(context.Context) error }
