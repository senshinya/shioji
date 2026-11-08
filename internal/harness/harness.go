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
