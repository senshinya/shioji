package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type sessionInfo struct {
	SessionID, Summary, CustomTitle, CWD string
	LastModified                         int64
}

func (r *runtime) candidate(info sessionInfo, folder string) (domain.ConversationCandidate, bool) {
	directory, dirErr := filepath.EvalSymlinks(info.CWD)
	allowed, allowedErr := filepath.EvalSymlinks(folder)
	if dirErr != nil || allowedErr != nil || !filepath.IsAbs(info.CWD) || directory != allowed || !nativeUUID.MatchString(info.SessionID) {
		return domain.ConversationCandidate{}, false
	}
	title := info.CustomTitle
	if title == "" {
		title = info.Summary
	}
	if title = strings.TrimSpace(r.redact(title)); title == "" {
		title = "Claude Code"
	}
	return domain.ConversationCandidate{
		SourceRef: info.SessionID, HarnessID: r.ready.HarnessID, Title: title, Directory: folder,
		UpdatedAt: time.UnixMilli(info.LastModified).UTC(),
	}, true
}

func (r *runtime) redact(text string) string {
	if r.config.Key != "" {
		return strings.ReplaceAll(text, r.config.Key, "••••")
	}
	return text
}

func (r *runtime) Conversations(ctx context.Context, folders []string, cursor string) (domain.ConversationCatalog, error) {
	var out domain.ConversationCatalog
	folderIndex, offset := 0, 0
	if cursor != "" {
		a, b, ok := strings.Cut(cursor, ":")
		var aErr, bErr error
		folderIndex, aErr = strconv.Atoi(a)
		offset, bErr = strconv.Atoi(b)
		if !ok || aErr != nil || bErr != nil || folderIndex < 0 || folderIndex >= len(folders) || offset < 0 {
			return out, errors.New("Claude Code 历史目录位置无效")
		}
	}
	for ; folderIndex < len(folders); folderIndex, offset = folderIndex+1, 0 {
		folder := folders[folderIndex]
		var listing []sessionInfo
		if err := r.historySDKAt(ctx, r.externalHome, historyRequest{Action: "list", Directory: folder, Offset: offset}, &listing); err != nil {
			return out, err
		}
		for i, info := range listing {
			if len(out.Items) == 50 {
				out.Cursor = strconv.Itoa(folderIndex) + ":" + strconv.Itoa(offset+i)
				return out, nil
			}
			if c, ok := r.candidate(info, folder); ok {
				out.Items = append(out.Items, c)
			}
		}
		if len(listing) == 51 {
			out.Cursor = strconv.Itoa(folderIndex) + ":" + strconv.Itoa(offset+51)
			return out, nil
		}
	}
	return out, nil
}

type sourceHistory struct {
	Info    sessionInfo
	Records []historyRecord
}

func (r *runtime) externalConversation(ctx context.Context, cwd, ref string) (sourceHistory, domain.ConversationCandidate, error) {
	var src sourceHistory
	err := r.historySDKAt(ctx, r.externalHome, historyRequest{Action: "read", Thread: ref, Directory: cwd}, &src)
	if err != nil {
		return src, domain.ConversationCandidate{}, err
	}
	candidate, ok := r.candidate(src.Info, cwd)
	if !ok || candidate.SourceRef != ref || len(src.Records) == 0 {
		return src, candidate, errors.New("Claude Code 未在所选目录找到完整历史")
	}
	return src, candidate, nil
}

func (r *runtime) ReadConversation(ctx context.Context, launch domain.LaunchContext, ref string) (domain.ConversationPreview, error) {
	return r.readConversation(ctx, launch, ref, true)
}

func (r *runtime) ImportConversation(ctx context.Context, launch domain.LaunchContext, ref string) (domain.ConversationPreview, error) {
	return r.readConversation(ctx, launch, ref, false)
}

func (r *runtime) readConversation(ctx context.Context, launch domain.LaunchContext, ref string, preview bool) (domain.ConversationPreview, error) {
	source, candidate, err := r.externalConversation(ctx, launch.CWD, ref)
	out := domain.ConversationPreview{Candidate: candidate}
	if err != nil {
		return out, err
	}
	var turn *domain.Turn
	for _, record := range source.Records {
		if record.User {
			if turn != nil && turn.Status == domain.ExecutionUnknown {
				turn.Status = domain.ExecutionInterrupted
			}
			out.Turns = append(out.Turns, domain.Turn{ID: record.UUID, Status: domain.ExecutionUnknown, StartedAt: record.timestamp()})
			turn = &out.Turns[len(out.Turns)-1]
		}
		if turn == nil {
			continue
		}
		if record.Type == "assistant" && record.Message.StopReason == "end_turn" {
			turn.Status = domain.ExecutionCompleted
			turn.CompletedAt = record.timestamp()
		}
		items := r.externalItems(record, turn.ID)
		out.Items = append(out.Items, items...)
	}
	if len(out.Items) == 0 || turn == nil {
		return out, errors.New("Claude Code 历史中没有可导入的消息")
	}
	if turn.Status == domain.ExecutionCompleted {
		out.ResumeRef = historyDigest(source.Records)
	} else {
		turn.Status = domain.ExecutionInterrupted
		out.Candidate.Active = true // The last native turn has no observed end boundary.
		out.Reason = "最后一轮缺少明确的完成边界，已保存可查看的历史"
	}
	if preview && (len(out.Turns) > 50 || len(out.Items) > 200) {
		out.Truncated = true
		out.Turns = out.Turns[max(0, len(out.Turns)-50):]
		first := out.Turns[0].ID
		if i := firstItemInTurn(out.Items, first); i >= 0 {
			out.Items = out.Items[i:]
		}
		out.Items = out.Items[max(0, len(out.Items)-200):]
	}
	return out, nil
}

func firstItemInTurn(items []domain.Item, turn string) int {
	for i, item := range items {
		if item.TurnID == turn {
			return i
		}
	}
	return -1
}

func (r *runtime) externalItems(record historyRecord, turn string) []domain.Item {
	var out []domain.Item
	for index, block := range record.Message.Content {
		item := domain.Item{ID: record.UUID + ":" + strconv.Itoa(index), TurnID: turn, Status: "completed", Final: true}
		switch {
		case record.User && block.Type == "text":
			item.Kind, item.Text = "user", block.Text
			item.SentAt = record.timestamp()
		case record.Type == "assistant" && block.Type == "text":
			item.Kind, item.Text = "assistant", block.Text
		case record.Type == "assistant" && block.Type == "thinking":
			item.Kind, item.Text = "reasoning", block.Thinking
		case record.Type == "assistant" && block.Type == "tool_use":
			item.Kind, item.Name, item.Text = "tool", block.Name, string(block.Input)
		default:
			continue
		}
		item.Text, item.Name = r.redact(item.Text), r.redact(item.Name)
		out = append(out, item)
	}
	return out
}

func (record historyRecord) timestamp() *time.Time {
	when, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return nil
	}
	return &when
}

func historyDigest(records []historyRecord) string {
	hash := sha256.New()
	for _, record := range records {
		io.WriteString(hash, record.Type+"\x00"+record.Digest+"\n")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (r *runtime) PrepareConversation(ctx context.Context, s domain.Session) (string, error) {
	if s.Origin == nil || s.Origin.ResumeRef == "" {
		return "", errors.New("此导入历史仅可查看")
	}
	source, _, err := r.externalConversation(ctx, s.Context.CWD, s.Origin.SourceRef)
	if err != nil {
		return "", err
	}
	if historyDigest(source.Records) != s.Origin.ResumeRef {
		return "", harness.ErrConversationSourceChanged
	}
	// Isolate the source file before asking the SDK to fork it. The native
	// CLI catalog won't see a duplicate and we never write to its profile.
	staging, err := os.MkdirTemp(r.home, ".import-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	relative, raw, err := nativeTranscript(r.externalHome, s.Origin.SourceRef)
	if err != nil {
		return "", err
	}
	if err := savePrivate(filepath.Join(staging, relative), raw); err != nil {
		return "", err
	}
	request := historyRequest{Action: "boundaries", Thread: s.Origin.SourceRef, Directory: s.Context.CWD}
	var staged []historyRecord
	if err := r.historySDKAt(ctx, staging, request, &staged); err != nil {
		return "", err
	}
	if historyDigest(staged) != s.Origin.ResumeRef {
		return "", harness.ErrConversationSourceChanged
	}
	var fork struct{ SessionID string }
	request.Action, request.Through = "fork", staged[len(staged)-1].UUID
	if err := r.historySDKAt(ctx, staging, request, &fork); err != nil {
		return "", err
	}
	if fork.SessionID == s.Origin.SourceRef || !nativeUUID.MatchString(fork.SessionID) {
		return "", errors.New("Claude Code 没有返回可独立接续的历史")
	}
	relative, raw, err = nativeTranscript(staging, fork.SessionID)
	if err != nil {
		return "", err
	}
	request.Action, request.Thread = "boundaries", fork.SessionID
	var copied []historyRecord
	if err := r.historySDKAt(ctx, staging, request, &copied); err != nil {
		return "", err
	}
	if historyDigest(copied) != s.Origin.ResumeRef || len(copied) != len(staged) {
		return "", harness.ErrConversationSourceChanged
	}
	ids := map[string]string{}
	for i, record := range staged {
		if record.User {
			ids[record.UUID] = copied[i].UUID
		}
	}
	meta, _ := json.Marshal(forkRecord{Source: s.Origin.SourceRef, IDs: ids})
	if err := savePrivate(r.forkPath(fork.SessionID), meta); err != nil {
		return "", err
	}
	if err := savePrivate(filepath.Join(r.home, relative), raw); err != nil {
		return "", err
	}
	return fork.SessionID, nil
}

func nativeTranscript(home, id string) (string, []byte, error) {
	projects := filepath.Join(home, "projects")
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return "", nil, err
	}
	var relative string
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		path := filepath.Join("projects", dir.Name(), id+".jsonl")
		info, err := os.Lstat(filepath.Join(home, path))
		if err == nil && info.Mode().IsRegular() {
			if relative != "" {
				return "", nil, errors.New("Claude Code 历史在多个目录中，无法确认来源")
			}
			relative = path
		}
	}
	if relative == "" {
		return "", nil, errors.New("Claude Code 历史文件不在原生目录中")
	}
	file, err := os.OpenFile(filepath.Join(home, relative), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	const limit = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(raw) > limit {
		return "", nil, errors.New("Claude Code 历史文件超过读取上限")
	}
	return relative, raw, nil
}

var _ harness.ConversationReader = (*runtime)(nil)
var _ harness.ConversationImporter = (*runtime)(nil)
var _ harness.ConversationContinuation = (*runtime)(nil)
