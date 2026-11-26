package claudecode

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

//go:embed history-sdk.mjs
var historySDK string

var nativeUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type historyRecord struct {
	UUID, Type, Digest string
	User               bool
	Timestamp          string
	ToolUseResult      json.RawMessage
	Message            struct {
		StopReason string `json:"stop_reason"`
		Content    contentBlocks
	}
}

type historyRequest struct {
	Action    string `json:"action"`
	Thread    string `json:"thread"`
	Directory string `json:"directory"`
	Through   string `json:"through,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

func (r *runtime) runHistorySDK(ctx context.Context, in historyRequest, result any) error {
	return r.historySDKAt(ctx, r.home, in, result)
}

func (r *runtime) historySDKAt(ctx context.Context, home string, in historyRequest, result any) error {
	if in.Action != "list" && !nativeUUID.MatchString(in.Thread) || !filepath.IsAbs(in.Directory) || !filepath.IsAbs(home) {
		return errors.New("Claude Code 历史来源无效")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, r.node, "--input-type=module", "--eval", historySDK, "--", r.sdk)
	cmd.Dir = in.Directory
	cmd.Env = slices.DeleteFunc(slices.Clone(r.env), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_") || strings.HasPrefix(name, "NODE_")
	})
	cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+home)
	cmd.Stdin = bytes.NewReader(append(raw, '\n'))
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("无法读取或分叉 Claude Code 的原生历史")
	}
	if err := json.Unmarshal(out, result); err != nil {
		return errors.New("Claude Code 原生历史结果无法核对")
	}
	return nil
}

type forkRecord struct {
	Source, Selected, Digest string
	Restart, Empty           bool
	IDs                      map[string]string // Shioji turn ID to the independent Claude user message ID
	Steers                   []string          // native user records attached to an earlier turn
}

func (r *runtime) forkPath(thread string) string {
	return filepath.Join(r.home, "shioji-history", thread+".json")
}

func (r *runtime) forkRecord(thread string) (forkRecord, error) {
	if !nativeUUID.MatchString(thread) {
		return forkRecord{}, errors.New("Claude Code 历史分叉标识无效")
	}
	raw, err := os.ReadFile(r.forkPath(thread))
	if err != nil {
		return forkRecord{}, err
	}
	var record forkRecord
	err = json.Unmarshal(raw, &record)
	return record, err
}

func (r *runtime) PrepareHistory(ctx context.Context, s domain.Session, boundary harness.HistoryBoundary) (string, error) {
	if !nativeUUID.MatchString(s.ThreadID) {
		return "", errors.New("Claude Code 历史来源无效")
	}
	var records []historyRecord
	query := historyRequest{Action: "boundaries", Thread: s.ThreadID, Directory: s.Context.CWD}
	if err := r.runHistorySDK(ctx, query, &records); err != nil {
		return "", err
	}
	mapping, err := r.forkRecord(s.ThreadID)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	receipts, err := r.Inspect(ctx, s)
	if err != nil {
		return "", err
	}
	steers := make(map[string]bool)
	for _, copied := range mapping.Steers {
		steers[copied] = true
	}
	for input, turn := range receipts.Inputs {
		native := nativeInputID(s.ThreadID, strings.TrimPrefix(input, "input:"))
		if native != turn {
			steers[native] = true
		}
	}
	selected := boundary.TurnID
	if prior := mapping.IDs[selected]; prior != "" {
		selected = prior
	}
	start := slices.IndexFunc(records, func(candidate historyRecord) bool {
		return candidate.User && candidate.UUID == selected && !steers[candidate.UUID]
	})
	if start < 0 {
		return "", errors.New("Claude Code 原生历史中找不到所选轮次")
	}
	end := start + 1
	for end < len(records) && (!records[end].User || steers[records[end].UUID]) {
		end++
	}
	if boundary.Restart {
		end = start
	}
	prefix := records[:end]
	if !boundary.Restart && (end == start+1 || prefix[end-1].Type != "assistant") {
		return "", errors.New("Claude Code 原生历史中无法确认完整的所选回复")
	}

	fork := boundary.PreparedRef
	digest := historyDigest(prefix)
	var previous forkRecord
	if fork != "" {
		record, err := r.forkRecord(fork)
		if err != nil || record.Source != s.ThreadID || record.Selected != boundary.TurnID ||
			record.Restart != boundary.Restart || record.Empty != (end == 0) || record.Digest != "" && record.Digest != digest {
			return "", errors.New("已准备的 Claude Code 历史分叉与当前请求不符")
		}
		previous = record
	} else if end == 0 {
		fork = domain.NewID()
	} else {
		var created struct{ SessionID string }
		request := historyRequest{Action: "fork", Thread: s.ThreadID, Directory: s.Context.CWD, Through: prefix[end-1].UUID}
		if err := r.runHistorySDK(ctx, request, &created); err != nil {
			return "", err
		}
		fork = created.SessionID
	}
	if !nativeUUID.MatchString(fork) || fork == s.ThreadID {
		return "", errors.New("Claude Code 未返回独立的历史分叉")
	}
	var copied []historyRecord
	query.Thread = fork
	if err := r.runHistorySDK(ctx, query, &copied); err != nil {
		return "", err
	}
	if len(copied) != len(prefix) {
		return "", errors.New("Claude Code 分叉的历史长度与所选轮次不符")
	}
	original := make(map[string]string, len(mapping.IDs))
	for shioji, native := range mapping.IDs {
		original[native] = shioji
	}
	ids := make(map[string]string)
	var copiedSteers []string
	for i, saved := range copied {
		entry := prefix[i]
		if entry.Digest != saved.Digest || entry.Type != saved.Type || entry.User != saved.User {
			return "", errors.New("Claude Code 分叉中的历史内容与源历史不符")
		}
		if entry.User {
			if steers[entry.UUID] {
				copiedSteers = append(copiedSteers, saved.UUID)
				continue
			}
			id := entry.UUID
			if virtual := original[id]; virtual != "" {
				id = virtual
			}
			ids[id] = saved.UUID
		}
	}
	if previous.Digest == "" {
		record, _ := json.Marshal(forkRecord{Source: s.ThreadID, Selected: boundary.TurnID, Digest: digest, Restart: boundary.Restart,
			Empty: end == 0, IDs: ids, Steers: copiedSteers})
		if err := savePrivate(r.forkPath(fork), record); err != nil {
			return "", err
		}
	}
	return fork, nil
}

func (r *runtime) ActivateHistory(ctx context.Context, s domain.Session, prepared string, commit func() error) error {
	record, err := r.forkRecord(prepared)
	if err != nil || !record.Restart || record.Source != s.ThreadID || prepared == s.ThreadID || record.Digest == "" {
		return errors.New("没有可激活的 Claude Code 历史替换")
	}
	// Verification is a read-only SDK operation. Opening a query here would
	// launch MCP and run SessionStart even if the worker rejects the commit.
	var copied []historyRecord
	if err := r.runHistorySDK(ctx, historyRequest{Action: "boundaries", Thread: prepared, Directory: s.Context.CWD}, &copied); err != nil {
		return err
	}
	if record.Digest != historyDigest(copied) || record.Empty != (len(copied) == 0) {
		return errors.New("准备好的 Claude Code 历史已改变")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.Stopped() || r.closing || r.turn != nil || len(r.approvals) != 0 || len(r.work) != 0 ||
		r.session.ID != "" && (r.session.ID != s.ID || r.session.ThreadID != s.ThreadID) {
		return errors.New("Claude Code 会话状态已改变")
	}
	if r.version != "" {
		version, err := profileVersion(r.home)
		if err != nil || version != r.version {
			return errors.New("Claude Code Profile 已变化，请重新核对")
		}
	}
	if err := commit(); err != nil {
		return err
	}
	if r.process == nil {
		// Dispatch binds the committed thread through its normal Resume path.
		return nil
	}
	r.session.ThreadID = prepared
	r.lastResult = completedResult{}
	r.lateToolTurns = nil
	r.workRunIDs = nil
	r.mcp = nil
	r.mcpRevision = ""
	// The next prompt is ordered behind activation in the bridge. A pipe
	// failure here cannot undo a committed history change or replay its input.
	if err := r.send(map[string]any{"type": "activate_history", "thread": prepared, "resume": !record.Empty}); err != nil {
		_ = r.input.Close()
	}
	return nil
}
