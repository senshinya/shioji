package antigravity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
)

const nativeHistoryDirectory = "antigravity-cli/conversations"
const maxHistoryDatabase = 128 << 20

var historyID = regexp.MustCompile(`^[a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}$`)

type historyCursor struct {
	Folder int
	Page   string
	Offset int
}

type historyMetadata struct {
	SessionID, CWD, Title string
	UpdatedAt             time.Time
}

type historyPage struct {
	Sessions   []historyMetadata
	NextCursor string
}

func (r *runtime) historyAvailable() bool {
	return r.historyBin != "" && filepath.IsAbs(r.skillProfile) &&
		r.native.AgentCapabilities.LoadSession && r.native.AgentCapabilities.SessionCapabilities.List != nil
}

func (r *runtime) historyACP(ctx context.Context, home, scratch string, replay *historyReplay) (*runtime, error) {
	env := slices.DeleteFunc(slices.Clone(r.historyEnv), func(entry string) bool {
		return strings.HasPrefix(entry, "GEMINI_HOME=")
	})
	env = append(env, "GEMINI_HOME="+home)
	probe := &runtime{}
	var err error
	probe.process, err = startRPC(ctx, []string{r.historyBin, "acp", "--disable-telemetry", "--sandbox",
		"--include-default-customizations=false", "--gemini-dir", home, "--log-file", filepath.Join(scratch, "acp.log")},
		env, harness.StartOptions{}, func(p packet) error {
			if len(p.ID) != 0 {
				return probe.reply(ctx, p.ID, nil, &rpcError{Code: -32601, Message: "History viewer"})
			}
			if replay != nil && p.Method == "session/update" {
				replay.update(p.Params)
			}
			return nil
		}, scratch)
	if err != nil {
		return nil, err
	}
	go probe.process.read()
	var init initialization
	err = probe.process.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1, "clientInfo": map[string]string{"name": "Shioji", "version": "1"}, "clientCapabilities": map[string]any{},
	}, &init)
	if err == nil && (init.ProtocolVersion != 1 || init.AgentInfo.Name != "Antigravity ACP Server" ||
		!init.AgentCapabilities.LoadSession || init.AgentCapabilities.SessionCapabilities.List == nil) {
		err = harness.Unsupported("conversation-import")
	}
	if err != nil {
		closeHistoryACP(probe)
		return nil, setupError(err)
	}
	return probe, nil
}

func closeHistoryACP(probe *runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = probe.closeProcess(ctx)
}

func (r *runtime) Conversations(ctx context.Context, folders []string, token string) (domain.ConversationCatalog, error) {
	var out domain.ConversationCatalog
	var cursor historyCursor
	if token != "" {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Folder < 0 || cursor.Folder >= len(folders) ||
			cursor.Offset < 0 || cursor.Offset > 1024 || len(cursor.Page) > 2048 {
			return out, errors.New("Antigravity 历史分页标识无效")
		}
	}
	err := r.withHistoryCatalog(ctx, func(probe *runtime) error {
		var err error
		out, err = r.historyCandidates(ctx, probe, folders, cursor)
		return err
	})
	return out, err
}

func (r *runtime) withHistoryCatalog(ctx context.Context, fn func(*runtime) error) error {
	if !r.historyAvailable() {
		return harness.Unsupported("conversation-import")
	}
	scratch, err := os.MkdirTemp("", "shioji-agy-list-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	probe, err := r.historyACP(ctx, r.skillProfile, scratch, nil)
	if err != nil {
		return err
	}
	defer closeHistoryACP(probe)
	return fn(probe)
}

func (r *runtime) historyCandidates(ctx context.Context, probe *runtime, folders []string, cursor historyCursor) (domain.ConversationCatalog, error) {
	var out domain.ConversationCatalog
	seen := make(map[historyCursor]bool)
	for cursor.Folder < len(folders) {
		if seen[cursor] {
			return out, errors.New("Antigravity 返回了重复的历史分页")
		}
		seen[cursor] = true
		directory := folders[cursor.Folder]
		if !filepath.IsAbs(directory) {
			return out, errors.New("Antigravity 历史目录无效")
		}
		var page historyPage
		params := map[string]string{"cwd": directory}
		if cursor.Page != "" {
			params["cursor"] = cursor.Page
		}
		if err := probe.process.call(ctx, "session/list", params, &page); err != nil {
			return out, setupError(err)
		}
		if len(page.Sessions) > 1024 || cursor.Offset > len(page.Sessions) ||
			len(page.NextCursor) > 2048 || page.NextCursor != "" && page.NextCursor == cursor.Page {
			return out, errors.New("Antigravity 返回了无效的历史分页")
		}
		for cursor.Offset < len(page.Sessions) && len(out.Items) < 50 {
			entry := page.Sessions[cursor.Offset]
			cursor.Offset++
			if entry.CWD != directory || !historyID.MatchString(entry.SessionID) {
				continue
			}
			title := clip(strings.TrimSpace(entry.Title), 4096)
			if title == "" {
				title = "Antigravity 会话"
			}
			out.Items = append(out.Items, domain.ConversationCandidate{SourceRef: entry.SessionID,
				HarnessID: r.descriptor.ID, Directory: directory, Title: title, UpdatedAt: entry.UpdatedAt})
		}
		if cursor.Offset == len(page.Sessions) {
			if page.NextCursor == "" {
				cursor.Folder++
			}
			cursor.Page, cursor.Offset = page.NextCursor, 0
		}
		if len(out.Items) == 50 {
			if cursor.Folder < len(folders) {
				raw, _ := json.Marshal(cursor)
				out.Cursor = base64.RawURLEncoding.EncodeToString(raw)
			}
			break
		}
	}
	return out, nil
}

func (r *runtime) ReadConversation(ctx context.Context, launch domain.LaunchContext, ref string) (domain.ConversationPreview, error) {
	out, err := r.ImportConversation(ctx, launch, ref)
	if err != nil {
		return out, err
	}
	return boundHistoryPreview(out), nil
}

func boundHistoryPreview(out domain.ConversationPreview) domain.ConversationPreview {
	const budget = 480 << 10
	firstTurn := max(0, len(out.Turns)-50)
	within := make(map[string]bool, len(out.Turns)-firstTurn)
	for _, turn := range out.Turns[firstTurn:] {
		within[turn.ID] = true
	}
	bytes, firstItem := 0, len(out.Items)
	for firstItem > 0 && len(out.Items)-firstItem < 200 {
		item := out.Items[firstItem-1]
		if !within[item.TurnID] {
			break
		}
		raw, _ := json.Marshal(item)
		bytes += len(raw) + 1
		if bytes > budget {
			break
		}
		firstItem--
	}
	out.Truncated = out.Truncated || firstTurn > 0 || firstItem > 0
	out.Items, out.Turns = out.Items[firstItem:], out.Turns[firstTurn:]
	return out
}

func (r *runtime) ImportConversation(ctx context.Context, launch domain.LaunchContext, ref string) (domain.ConversationPreview, error) {
	var out domain.ConversationPreview
	if !historyID.MatchString(ref) || !filepath.IsAbs(launch.CWD) {
		return out, errors.New("Antigravity 历史来源无效")
	}
	dir := []string{launch.CWD}
	var found domain.ConversationCandidate
	err := r.withHistoryCatalog(ctx, func(probe *runtime) error {
		var cursor historyCursor
		seen := make(map[string]bool)
		for {
			page, err := r.historyCandidates(ctx, probe, dir, cursor)
			if err != nil {
				return err
			}
			for _, candidate := range page.Items {
				if candidate.SourceRef == ref {
					found = candidate
					return nil
				}
			}
			if page.Cursor == "" || seen[page.Cursor] {
				return errors.New("Antigravity 历史不属于所选目录")
			}
			seen[page.Cursor] = true
			raw, _ := base64.RawURLEncoding.DecodeString(page.Cursor)
			if err := json.Unmarshal(raw, &cursor); err != nil {
				return err
			}
		}
	})
	if err != nil {
		return out, err
	}
	return r.replayConversation(ctx, found)
}

func (r *runtime) replayConversation(ctx context.Context, candidate domain.ConversationCandidate) (domain.ConversationPreview, error) {
	out := domain.ConversationPreview{Candidate: candidate,
		Reason: "Antigravity 历史已保存供回看，当前运行时没有独立接续接口"}
	scratch, err := os.MkdirTemp("", "shioji-agy-replay-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(scratch)
	home := filepath.Join(scratch, "profile")
	cwd := filepath.Join(scratch, "workspace")
	if err := os.Mkdir(cwd, 0700); err != nil {
		return out, err
	}
	relative := filepath.Join(nativeHistoryDirectory, candidate.SourceRef+".db")
	target := filepath.Join(home, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return out, err
	}
	historyDir, err := filepath.EvalSymlinks(filepath.Join(r.skillProfile, nativeHistoryDirectory))
	if err != nil {
		return out, errors.New("Antigravity 历史目录不可读")
	}
	if relative, err := filepath.Rel(r.skillProfile, historyDir); err != nil || !filepath.IsLocal(relative) {
		return out, errors.New("Antigravity 历史目录超出所属 Profile")
	}
	if err := snapshotAGYHistory(ctx, filepath.Join(historyDir, filepath.Base(relative)), target); err != nil {
		return out, err
	}
	_, authFileErr := os.Lstat(filepath.Join(r.skillProfile, "antigravity-cli", "acp_token.json"))
	if authFileErr == nil || systemexec.EnvironmentValue(r.historyEnv, "AGY_ACP_FORCE_FILE_STORAGE") == "1" {
		if err := copyReplayLogin(r.skillProfile, home); err != nil {
			return out, err
		}
	}

	replay := historyReplay{source: candidate.SourceRef, tools: make(map[string]int)}
	probe, err := r.historyACP(ctx, home, scratch, &replay)
	if err != nil {
		return out, err
	}
	defer closeHistoryACP(probe)
	request := sessionRequest{SessionID: candidate.SourceRef, CWD: cwd, MCPServers: []mcpHTTPServer{}}
	reply := make(chan packet, 1)
	id, err := probe.process.request(ctx, "session/load", request, func(p packet) error {
		// The replay ends on the matching response, on the same reader that
		// processed every history notification before it.
		replay.finish()
		reply <- p
		return nil
	})
	if err != nil {
		return out, err
	}
	defer probe.process.pending.Delete(id)
	select {
	case p := <-reply:
		if p.Error != nil {
			return out, setupError(p.Error)
		}
		var loaded struct{ SessionID string }
		if json.Unmarshal(p.Result, &loaded) != nil || loaded.SessionID != "" && loaded.SessionID != candidate.SourceRef {
			return out, errors.New("Antigravity 回放返回了不同的会话")
		}
	case <-probe.process.done:
		return out, errors.New("Antigravity 历史回放中断")
	case <-ctx.Done():
		return out, ctx.Err()
	}
	if replay.tooLarge || len(replay.items) == 0 || len(replay.turns) == 0 {
		return out, errors.New("Antigravity 历史为空或超过导入上限")
	}
	out.Items, out.Turns = replay.items, replay.turns
	return out, nil
}

func snapshotAGYHistory(ctx context.Context, source, target string) error {
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxHistoryDatabase {
		return errors.New("Antigravity 会话快照不可读或超过上限")
	}
	if wal, err := os.Lstat(source + "-wal"); err == nil && (!wal.Mode().IsRegular() || wal.Size() > maxHistoryDatabase) {
		return errors.New("Antigravity 会话日志超过快照上限")
	}
	u := url.URL{Scheme: "file", Path: source, RawQuery: "mode=ro&_pragma=busy_timeout(1500)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	// Read a transactionally consistent snapshot, including committed WAL data.
	// The source's native schema is opaque to Shioji.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("无法读取 Antigravity 会话快照")
	}
	after, err := os.Lstat(source)
	if err != nil || !os.SameFile(before, after) {
		return errors.New("Antigravity 会话文件已更换，请重试")
	}
	info, err := os.Stat(target)
	if err != nil || info.Size() > maxHistoryDatabase {
		return errors.New("Antigravity 会话快照超过上限")
	}
	return os.Chmod(target, 0600)
}

func copyReplayLogin(source, home string) error {
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer root.Close()
	const relative = "antigravity-cli/acp_token.json"
	f, err := root.Open(relative)
	if err != nil {
		return errors.New("无法读取所属 Antigravity Profile 的登录状态")
	}
	defer f.Close()
	const limit = 1 << 20
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("Antigravity Profile 登录状态无效")
	}
	token, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(token) == 0 || len(token) > limit {
		return errors.New("无法读取所属 Antigravity Profile 的登录状态")
	}
	return os.WriteFile(filepath.Join(home, relative), token, 0600)
}

var _ harness.ConversationReader = (*runtime)(nil)
var _ harness.ConversationImporter = (*runtime)(nil)
