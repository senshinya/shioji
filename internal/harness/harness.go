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
	Comment         *domain.CodeCommentContent
	ExecutionBranch string
	Attachment      domain.Attachment
	Path            string
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
	ForkContext   *domain.ForkContext
	Goal          *domain.GoalProgress
	Objective     string
	Text          string
	Attachments   []AttachmentInput
	Skills        []domain.SkillReference
	QuestionReply *domain.QuestionReply
}

// InputHandoffRuntime can defer the worker's durable claim until the input
// is ready to cross the harness transport. An error from claim revokes it.
type InputHandoffRuntime interface {
	SubmitWithHandoff(context.Context, domain.Session, string, TurnInput, func() error) (string, error)
	SteerWithHandoff(context.Context, domain.Session, string, string, TurnInput, func() error) error
}

// DeferredInputRuntime releases the send order as soon as the input crosses
// the transport, while leaving the durable receipt to the actual native reply.
type DeferredInputRuntime interface {
	SubmitDeferred(context.Context, domain.Session, string, TurnInput, func() error) (string, <-chan error, error)
	SteerDeferred(context.Context, domain.Session, string, string, TurnInput, func() error) (<-chan error, error)
}

type SkillProvider interface {
	Skills(context.Context, domain.LaunchContext) (domain.SkillCatalog, error)
}

// Catalog discovery and preview are read-only; they must not resume or bind a
// source process. The worker validates folder membership before either call.
type ConversationReader interface {
	Conversations(context.Context, []string, string) (domain.ConversationCatalog, error)
	ReadConversation(context.Context, domain.LaunchContext, string) (domain.ConversationPreview, error)
}

// ImportConversation reads the complete source snapshot on the owning device.
// Unlike the bounded display preview, its result never crosses the client wire.
type ConversationImporter interface {
	ImportConversation(context.Context, domain.LaunchContext, string) (domain.ConversationPreview, error)
}

// PrepareConversation copies the pinned imported prefix into an independent,
// unbound execution context. It must never take ownership of the original.
type ConversationContinuation interface {
	PrepareConversation(context.Context, domain.Session) (string, error)
}

// ErrConversationSourceChanged means the imported snapshot must be checked
// again before any input can be handed off to the private copy.
var ErrConversationSourceChanged = errors.New("来源历史已变化，请重新核对历史后再发送")

// GoalController changes or reads the source-owned conversation objective.
// The harness owns continuation and reports each actual turn separately.
type GoalController interface {
	CreateGoalThread(context.Context, domain.Session) (string, error)
	ReadGoal(context.Context, domain.Session) (*domain.Goal, error)
	ChangeGoal(context.Context, domain.Session, domain.GoalChange) (*domain.Goal, error)
}

// ContextCompactor changes model context without adding a user message.
// Inspection is read-only and must never repeat an uncertain request.
type ContextCompactor interface {
	Compact(context.Context, domain.Session) error
	InspectCompaction(context.Context, domain.Session, domain.ContextAction) (domain.ContextObservation, error)
}

// ContextCompactionPreparer confirms that the bound session offers compaction
// before a maintenance operation is durably admitted.
type ContextCompactionPreparer interface {
	PrepareCompaction(context.Context, domain.Session) error
}

// An empty launch context means device-level Skills only. Concrete adapters
// resolve discovery roots, file reads, scopes and native enablement policies.
type SkillManager interface {
	SkillInventory(context.Context, domain.LaunchContext) (domain.SkillInventory, error)
	ReadSkill(context.Context, domain.LaunchContext, string) (domain.SkillDocument, error)
	ChangeSkill(context.Context, domain.LaunchContext, domain.SkillChange) (domain.SkillChangeResult, error)
}

// An empty context inspects global automation only. A nonempty source inspects
// that existing runtime, without starting or resuming it. The adapter owns
// definition encoding, native trust and configuration reload semantics.
type HookProvider interface {
	Hooks(context.Context, domain.LaunchContext, string) (domain.HookCatalog, error)
	ChangeHook(context.Context, domain.LaunchContext, domain.HookChange) (domain.HookChangeResult, error)
	RefreshHooks(context.Context) error
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
	// CatalogOnly requests a cached catalog controller. Concrete adapters release
	// discovery processes and acquire short-lived probes for later service calls.
	// Accepted conversations always use a separate owned execution scope.
	CatalogOnly bool
	// CatalogDirectory is worker-owned storage for the adapter’s discovery data.
	// RefreshCatalog allows probes that require explicit user action.
	CatalogDirectory string
	RefreshCatalog   bool
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

// SessionUnloader runs an adapter's normal session-end lifecycle before the
// owning worker retires its process. It never submits model input.
type SessionUnloader interface {
	Unload(context.Context, domain.Session) error
}

// ManualShutdownRuntime opts into normal-only teardown backed by the worker's
// durable managed-generation authorization, including loss and Close paths.
type ManualShutdownRuntime interface {
	ManualShutdownSafe() bool
}

// DeliveryError classifies an external send without confusing unknown delivery
// with explicit rejection. It is shared semantics, not a native RPC error.
type DeliveryError struct {
	Uncertain bool
	Message   string
}

func (e *DeliveryError) Error() string { return e.Message }
func IsUncertain(err error) bool       { var e *DeliveryError; return errors.As(err, &e) && e.Uncertain }

func IsRejected(err error) bool {
	var delivery *DeliveryError
	return errors.As(err, &delivery) && !delivery.Uncertain
}

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

// SignInCodeReceiver holds a one-time response on its original authorization process.
type SignInCodeReceiver interface {
	SubmitCode(context.Context, string) error
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

// Inspection never reloads services or creates/resumes history. A nonempty
// source reference must query that existing session, without global substitution.
type ToolServiceInspector interface {
	InspectToolServices(context.Context, domain.LaunchContext, string) (domain.ToolServiceInspection, error)
}

// Configuration writes are optional and operate on the owning harness's native
// settings. The opaque revision protects concurrent edits by other clients.
type ToolServiceEditor interface {
	ChangeToolService(context.Context, domain.ToolServiceChange) (domain.ToolServiceChangeResult, error)
}

// WorkRestorer reinstates opaque observation links after a resumed source is
// bound. It must not start work or read unrequested history.
type WorkRestorer interface {
	RestoreWork(domain.Session, []domain.WorkActivity)
}

// WorkController addresses a known independent run; it must never substitute
// interruption of its parent or process identifiers guessed from display text.
type WorkController interface {
	StopWork(context.Context, domain.Session, string) error
}

// WorkMessenger preserves independent ownership and the execution's existing
// configuration. Receipt inspection never sends or resumes an execution.
type WorkMessenger interface {
	SendWork(context.Context, domain.Session, domain.WorkInput) error
	WorkInputReceived(context.Context, domain.Session, domain.WorkInput) (bool, error)
}

// PendingRuntime exposes only unsettled native requests, without wire IDs.
type PendingRuntime interface{ PendingOperations() int }
