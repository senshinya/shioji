package codex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
)

type conversationThread struct {
	ID, CWD, Preview string
	Name             *string
	Model            *string
	UpdatedAt        int64
	ParentThreadID   *string
	Status           struct{ Type string }
}

func conversationCandidate(t conversationThread) domain.ConversationCandidate {
	title := t.Preview
	if t.Name != nil && strings.TrimSpace(*t.Name) != "" {
		title = *t.Name
	}
	model := ""
	if t.Model != nil {
		model = *t.Model
	}
	return domain.ConversationCandidate{SourceRef: t.ID, HarnessID: "codex", Title: boundedTo(strings.TrimSpace(title), 512), Model: model, Directory: conversationDirectory(t.CWD), UpdatedAt: time.Unix(t.UpdatedAt, 0).UTC(), Active: t.Status.Type == "active"}
}
func conversationDirectory(value string) string {
	value = sourceExecutionPath(value)
	if canonical, err := filepath.EvalSymlinks(value); err == nil {
		return canonical
	}
	return filepath.Clean(value)
}

const (
	conversationIndexCursor = "index:"
	conversationScanCursor  = "scan:"
)

func (r *Runtime) Conversations(ctx context.Context, roots []string, cursor string) (domain.ConversationCatalog, error) {
	var out domain.ConversationCatalog
	if !r.supports("thread/list") {
		return out, harness.Unsupported("conversation-import")
	}
	if len(roots) == 0 || len(cursor) > 4096 {
		return out, errors.New("历史查询范围无效")
	}
	// Index metadata can retain a logical/symlink cwd while thread/read returns
	// its physical path. Filter locally so a literal cwd filter cannot hide it.
	args := map[string]any{"limit": 50, "sortKey": "updated_at", "modelProviders": []string{}, "sourceKinds": []string{"cli", "vscode", "appServer"}}
	maxPages := 1
	cursorPrefix := ""
	if r.protocol.indexedThreadList {
		var scan bool
		cursor, scan = strings.CutPrefix(cursor, conversationScanCursor)
		if scan {
			cursorPrefix = conversationScanCursor
		} else {
			cursor, _ = strings.CutPrefix(cursor, conversationIndexCursor)
			cursorPrefix = conversationIndexCursor
			// Skip unrelated index pages within one probe. Reading the index
			// directly avoids scanning JSONL rollouts to repair metadata.
			args["useStateDbOnly"] = true
			maxPages = 20
		}
	}
	seen := map[string]bool{cursor: true}
	for range maxPages {
		if cursor != "" {
			args["cursor"] = cursor
		}
		var page struct {
			Data       []conversationThread
			NextCursor *string
		}
		if err := r.call(ctx, "thread/list", args, &page); err != nil {
			return out, err
		}
		if len(page.Data) > 50 {
			return out, errors.New("历史目录超过分页上限")
		}
		for _, t := range page.Data {
			c := conversationCandidate(t)
			if c.SourceRef != "" && c.Title != "" && t.ParentThreadID == nil && slices.Contains(roots, c.Directory) {
				out.Items = append(out.Items, c)
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			out.Cursor = ""
			if cursorPrefix == conversationIndexCursor {
				out.Cursor = conversationScanCursor
				out.UnindexedScan = true
			}
			break
		}
		cursor = *page.NextCursor
		if len(cursorPrefix)+len(cursor) > 4096 || seen[cursor] {
			return out, errors.New("历史分页游标无效")
		}
		out.Cursor = cursorPrefix + cursor
		if len(out.Items) > 0 {
			break
		}
		seen[cursor] = true
	}
	return out, nil
}

type conversationPrefix struct{ Source, Directory, Turn, Digest string }
type conversationTurn struct {
	wire.Turn
	Items     []wire.Item
	ItemsView string
}

func (r *Runtime) ReadConversation(ctx context.Context, launch domain.LaunchContext, source string) (domain.ConversationPreview, error) {
	out, _, err := r.readConversation(ctx, launch, source, "", false)
	return out, err
}
func (r *Runtime) ImportConversation(ctx context.Context, launch domain.LaunchContext, source string) (domain.ConversationPreview, error) {
	out, _, err := r.readConversation(ctx, launch, source, "", true)
	return out, err
}
func (r *Runtime) readConversation(ctx context.Context, launch domain.LaunchContext, source, through string, complete bool) (domain.ConversationPreview, string, error) {
	var out domain.ConversationPreview
	if !r.supports("thread/read") || !r.supports("thread/turns/list") {
		return out, "", harness.Unsupported("conversation-import")
	}
	if source == "" || len(source) > 512 {
		return out, "", errors.New("历史来源无效")
	}
	var meta struct{ Thread conversationThread }
	if err := r.call(ctx, "thread/read", map[string]any{"threadId": source}, &meta); err != nil {
		return out, "", err
	}
	out.Candidate = conversationCandidate(meta.Thread)
	if meta.Thread.ID != source || out.Candidate.Directory != launch.CWD {
		return out, "", errors.New("历史来源不属于此目录")
	}
	cursor := ""
	seen := map[string]bool{}
	var turns []conversationTurn
	itemCount, byteCount := 0, 0
	matched := through == ""
	for {
		var page struct {
			Data       []conversationTurn
			NextCursor *string
		}
		direction := "desc"
		if through != "" || complete {
			direction = "asc"
		}
		args := map[string]any{"threadId": source, "limit": 10, "itemsView": "full", "sortDirection": direction}
		if cursor != "" {
			args["cursor"] = cursor
		}
		if err := r.call(ctx, "thread/turns/list", args, &page); err != nil {
			return out, "", err
		}
		for _, t := range page.Data {
			if t.ID == "" || t.ItemsView != "" && t.ItemsView != "full" {
				return out, "", errors.New("历史内容不完整")
			}
			encoded := conversationSourceBytes(t)
			itemCount += len(t.Items)
			byteCount += len(encoded)
			if complete && (len(turns) >= 10000 || itemCount > 100000 || byteCount > 64<<20) {
				return out, "", errors.New("来源历史超过完整导入容量，原会话保持不变")
			}
			if !complete && (len(turns) >= 50 || itemCount > 200 || byteCount > 384<<10) {
				out.Truncated = true
				remaining := max(0, 200-(itemCount-len(t.Items)))
				if len(turns) < 50 && remaining > 0 && len(t.Items) > 0 {
					offset := max(0, len(t.Items)-remaining)
					tail := append([]wire.Item(nil), t.Items[offset:]...)
					if offset > 0 && t.Items[0].Type == "userMessage" && len(tail) > 1 {
						tail[0] = t.Items[0]
					}
					t.Items = tail
					turns = append(turns, t)
				}
				break
			}
			turns = append(turns, t)
			if t.ID == through {
				matched = true
				break
			}
		}
		if out.Truncated || through != "" && matched || page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
		if seen[cursor] || !complete && len(seen) >= 50 {
			return out, "", errors.New("历史分页未前进")
		}
		seen[cursor] = true
	}
	if !matched {
		return out, "", errors.New("已导入的历史边界已变化")
	}
	if through == "" && !complete {
		slices.Reverse(turns)
	}
	last := ""
	running := out.Candidate.Active
	for _, t := range turns {
		state := domain.ExecutionState(t.Status)
		if state == "inProgress" {
			state = "interrupted"
			running = true
		}
		if state != "completed" && state != "failed" && state != "interrupted" {
			state = "interrupted"
			running = true
		}
		out.Turns = append(out.Turns, domain.Turn{ID: t.ID, Status: state, StartedAt: stamp(t.StartedAt), CompletedAt: stamp(t.CompletedAt), DurationMS: t.DurationMS})
		for _, i := range t.Items {
			if i.Type == "plan" {
				continue
			}
			n := normalizeItem(i, true)
			if i.Type == "userMessage" {
				var content []wire.Input
				if err := json.Unmarshal(i.Content, &content); err != nil {
					return out, "", errors.New("用户历史内容无效")
				}
				var text []string
				for _, p := range content {
					if p.Type == "text" {
						text = append(text, p.Text)
					} else {
						text = append(text, "[附件未导入]")
					}
				}
				n = domain.Item{Kind: "user", Text: boundedJSONText(strings.Join(text, "\n"), 96<<10), Status: "sent", Final: true, SentAt: stamp(t.StartedAt)}
			}
			// Imported tools remain inert history. Never reconstruct live requests,
			// work ownership or active command output from a stored source item.
			n.Agents = nil
			n.ID = itemKey(t.ID, i.ID)
			n.TurnID = t.ID
			if n.Status == "running" || n.Status == "pending" || n.Status == "unknown" {
				n.Status = "interrupted"
			}
			out.Items = append(out.Items, n)
		}
		if t.Status == "completed" {
			last = t.ID
		} else {
			last = ""
		}
	}
	if !complete {
		boundConversationPreview(&out)
	}
	if out.Truncated {
		out.Reason = "仅导入部分历史，无法接续原上下文"
	} else if running {
		out.Reason = "来源仍在运行，仅可查看历史"
	} else if last == "" {
		out.Reason = "来源没有可接续的完整轮次"
	} else if !r.supports("thread/fork") || !r.supports("thread/unsubscribe") || !r.supports("thread/loaded/list") {
		out.Reason = "当前 Agent 不支持接续导入历史"
	} else {
		ref := conversationPrefix{Source: source, Directory: launch.CWD, Turn: last, Digest: conversationSourceDigest(turns)}
		b, _ := json.Marshal(ref)
		out.ResumeRef = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, conversationSourceDigest(turns), nil
}
func conversationSourceBytes(t conversationTurn) []byte {
	items := make([]json.RawMessage, len(t.Items))
	for i, item := range t.Items {
		items[i], _ = json.Marshal(item.Fields)
	}
	b, _ := json.Marshal(struct {
		Turn  wire.Turn
		Items []json.RawMessage
	}{t.Turn, items})
	return b
}
func conversationSourceDigest(turns []conversationTurn) string {
	digest := sha256.New()
	for _, turn := range turns {
		digest.Write(conversationSourceBytes(turn))
		digest.Write([]byte{'\n'})
	}
	return hex.EncodeToString(digest.Sum(nil))
}
func boundConversationPreview(p *domain.ConversationPreview) {
	size := 0
	for _, item := range p.Items {
		b, _ := json.Marshal(item)
		size += len(b)
	}
	if size <= 384<<10 {
		return
	}
	p.Truncated = true
	first := -1
	for i, item := range p.Items {
		if item.Kind == "user" {
			first = i
			break
		}
	}
	budget := 384 << 10
	var head, tail []domain.Item
	if first >= 0 {
		item := p.Items[first]
		b, _ := json.Marshal(item)
		if len(b) <= budget {
			head = append(head, item)
			budget -= len(b)
		}
	}
	for i := len(p.Items) - 1; i >= 0; i-- {
		if i == first {
			continue
		}
		b, _ := json.Marshal(p.Items[i])
		if len(b) > budget {
			continue
		}
		tail = append(tail, p.Items[i])
		budget -= len(b)
	}
	slices.Reverse(tail)
	p.Items = append(head, tail...)
	p.Turns = slices.DeleteFunc(p.Turns, func(turn domain.Turn) bool {
		return !slices.ContainsFunc(p.Items, func(item domain.Item) bool { return item.TurnID == turn.ID })
	})
}
func (r *Runtime) PrepareConversation(ctx context.Context, s domain.Session) (string, error) {
	if s.Origin == nil || s.Origin.ResumeRef == "" {
		return "", errors.New("此导入历史仅可查看")
	}
	b, err := base64.RawURLEncoding.DecodeString(s.Origin.ResumeRef)
	var ref conversationPrefix
	if err != nil || json.Unmarshal(b, &ref) != nil || ref.Source != s.Origin.SourceRef || ref.Directory != s.Context.CWD || ref.Turn == "" || ref.Digest == "" {
		return "", errors.New("导入历史边界无效")
	}
	p, digest, err := r.readConversation(ctx, s.Context, ref.Source, ref.Turn, true)
	if err != nil {
		return "", err
	}
	if p.Truncated || digest != ref.Digest {
		return "", errors.New("来源历史已变化，无法从此副本接续")
	}
	s.ThreadID = ref.Source
	return r.Fork(ctx, s, ref.Turn, false)
}
