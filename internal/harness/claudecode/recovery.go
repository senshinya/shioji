package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"shioji/internal/domain"
)

func (r *runtime) VerifyContinuation(ctx context.Context, s domain.Session, inputID string) error {
	if operation, ok := strings.CutPrefix(inputID, "input:"); ok && operation != "" {
		inputID = nativeInputID(s.ThreadID, operation)
	}
	var present bool
	if nativeUUID.MatchString(inputID) {
		if err := r.runHistorySDK(ctx, historyRequest{
			Action: "verify-input", Thread: s.ThreadID, Directory: s.Context.CWD, Through: inputID,
		}, &present); err != nil {
			return err
		}
	}
	if !present {
		return errors.New("Claude Code 原生历史中缺少最后一条输入，可在新会话接续")
	}
	return nil
}

type inputReceipt struct {
	Operation string
	Turn      domain.Turn
}

func (r *runtime) receiptDirectory(s domain.Session) string {
	if r.home == "" || s.ID == "" || !nativeUUID.MatchString(s.ThreadID) {
		return ""
	}
	owner := uuid.NewSHA1(uuid.NameSpaceOID, []byte(s.ID)).String()
	return filepath.Join(r.home, "shioji-receipts", owner, s.ThreadID)
}

// This stores only observed SDK acknowledgments and terminal state. The
// operation ID links back to the worker journal. No prompt or tool result is
// written here, and a write to SDK stdin is never recorded as an acknowledgment.
func (r *runtime) recordReceipt(operation string, turn domain.Turn) {
	if operation == "" {
		return
	}
	dir := r.receiptDirectory(r.session)
	if dir == "" {
		return
	}
	raw, err := json.Marshal(inputReceipt{Operation: operation, Turn: turn})
	if err != nil {
		return
	}
	_ = savePrivate(filepath.Join(dir, nativeInputID(r.session.ThreadID, operation)+".json"), raw)
}

func savePrivate(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".shioji-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func (r *runtime) Inspect(ctx context.Context, s domain.Session) (domain.RecoveryHistory, error) {
	source := domain.RecoveryHistory{State: domain.SourceUnavailable, Inputs: map[string]string{}}
	r.mu.Lock()
	defer r.mu.Unlock()
	source.Attached = r.process != nil && !r.Stopped() && r.session.ID == s.ID && r.session.ThreadID == s.ThreadID
	dir := r.receiptDirectory(s)
	if dir == "" {
		return source, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return source, err
	}
	turns := map[string]int{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return source, err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return source, err
		}
		var receipt inputReceipt
		if json.Unmarshal(raw, &receipt) != nil || receipt.Operation == "" || nativeInputID(s.ThreadID, receipt.Operation)+".json" != entry.Name() ||
			!nativeUUID.MatchString(receipt.Turn.ID) {
			return source, errors.New("Claude Code 的执行回执无法核对")
		}
		source.Inputs["input:"+receipt.Operation] = receipt.Turn.ID
		if at, ok := turns[receipt.Turn.ID]; !ok {
			turns[receipt.Turn.ID] = len(source.Turns)
			source.Turns = append(source.Turns, receipt.Turn)
		} else if source.Turns[at].Status == domain.ExecutionUnknown {
			source.Turns[at] = receipt.Turn
		}
	}
	source.Available = source.Attached || len(source.Turns) != 0
	if source.Available {
		source.State = domain.SourceIdle
	}
	if source.Attached && r.turn != nil {
		source.State = domain.SourceActive
		at := slices.IndexFunc(source.Turns, func(turn domain.Turn) bool { return turn.ID == r.turn.turn.ID })
		if at >= 0 {
			source.Turns[at] = r.turn.turn
		} else {
			source.Turns = append(source.Turns, r.turn.turn)
		}
	}
	return source, nil
}
