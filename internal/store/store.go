// Package store owns the device's durable SQLite state and journal.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
	"shioji/internal/domain"
)

type Store struct {
	mu                   sync.Mutex
	db                   *sql.DB
	path                 string
	historyReader        historyQuerier
	deviceID, generation string
	changed              chan struct{}
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	for _, p := range []string{"foreign_keys=ON", "journal_mode=WAL", "synchronous=FULL", "fullfsync=ON", "busy_timeout=3000"} {
		if _, err = db.Exec("PRAGMA " + p); err != nil {
			return fail(err)
		}
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version > SchemaVersion {
		return fail(fmt.Errorf("database schema %d is newer than supported schema %d", version, SchemaVersion))
	}
	tx, err := db.Begin()
	if err != nil {
		return fail(err)
	}
	_, err = tx.Exec(`
 CREATE TABLE IF NOT EXISTS metadata (id INTEGER PRIMARY KEY CHECK(id=1), device_id TEXT NOT NULL, generation TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS workspaces (id TEXT PRIMARY KEY, body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, body BLOB NOT NULL);
 CREATE UNIQUE INDEX IF NOT EXISTS imported_conversation ON sessions(json_extract(body,'$.workspaceId'),json_extract(body,'$.harnessId'),json_extract(body,'$.origin.sourceRef')) WHERE json_extract(body,'$.origin.sourceRef') IS NOT NULL;
 CREATE TABLE IF NOT EXISTS operations (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), state TEXT NOT NULL, digest TEXT NOT NULL, error TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS items (session_id TEXT NOT NULL REFERENCES sessions(id), id TEXT NOT NULL, position INTEGER NOT NULL, body BLOB NOT NULL, PRIMARY KEY(session_id,id));
 CREATE INDEX IF NOT EXISTS items_order ON items(session_id,position);
 CREATE TABLE IF NOT EXISTS requests (session_id TEXT NOT NULL REFERENCES sessions(id), id TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(session_id,id));
 CREATE TABLE IF NOT EXISTS turns (session_id TEXT NOT NULL REFERENCES sessions(id), id TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(session_id,id));
 CREATE TABLE IF NOT EXISTS journal (sequence INTEGER PRIMARY KEY AUTOINCREMENT, body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS queues (session_id TEXT PRIMARY KEY REFERENCES sessions(id), body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS plans (session_id TEXT PRIMARY KEY REFERENCES sessions(id), body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS history_operations (id TEXT PRIMARY KEY, body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS runtime_retirements (session_id TEXT NOT NULL, generation TEXT NOT NULL, revision INTEGER NOT NULL, reason TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', PRIMARY KEY(session_id,generation,revision));
 CREATE TABLE IF NOT EXISTS worker_maintenance (id TEXT PRIMARY KEY, body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS title_jobs (session_id TEXT PRIMARY KEY REFERENCES sessions(id), state TEXT NOT NULL, body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS works (session_id TEXT NOT NULL REFERENCES sessions(id), id TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(session_id,id));
 CREATE TABLE IF NOT EXISTS work_items (session_id TEXT NOT NULL, work_id TEXT NOT NULL, id TEXT NOT NULL, position INTEGER NOT NULL, revision INTEGER NOT NULL, body BLOB NOT NULL, PRIMARY KEY(session_id,work_id,id), FOREIGN KEY(session_id,work_id) REFERENCES works(session_id,id) ON DELETE CASCADE);
 CREATE INDEX IF NOT EXISTS work_items_order ON work_items(session_id,work_id,position);
 CREATE INDEX IF NOT EXISTS work_items_prompts ON work_items(session_id,work_id,position) WHERE json_extract(body,'$.kind')='user';
 CREATE TABLE IF NOT EXISTS work_inputs (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), work_id TEXT NOT NULL, body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS archive_policy (id INTEGER PRIMARY KEY CHECK(id=1), body BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS deleted_operations (id TEXT PRIMARY KEY, session_id TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS deleted_sessions (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, cleaned INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS deleted_workspaces (id TEXT PRIMARY KEY, cleaned INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS archive_resources (workspace_id TEXT NOT NULL, attachment_id TEXT NOT NULL, PRIMARY KEY(workspace_id,attachment_id));
 CREATE TABLE IF NOT EXISTS session_reloads (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, body BLOB NOT NULL);
 CREATE INDEX IF NOT EXISTS operations_session_state ON operations(session_id,state);
 CREATE INDEX IF NOT EXISTS items_provisional_replies ON items(session_id,json_extract(body,'$.turnId')) WHERE json_extract(body,'$.kind')='assistant' AND COALESCE(json_extract(body,'$.final'),0)=0;
 CREATE INDEX IF NOT EXISTS work_inputs_unsettled_session ON work_inputs(session_id) WHERE json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') AND COALESCE(json_extract(body,'$.executionStopped'),0)=0;
 CREATE INDEX IF NOT EXISTS history_active_source ON history_operations(json_extract(body,'$.source.id')) WHERE json_extract(body,'$.state') IN ('prepared','forked');
 CREATE INDEX IF NOT EXISTS history_active_session ON history_operations(json_extract(body,'$.session.id')) WHERE json_extract(body,'$.state') IN ('prepared','forked');
 PRAGMA user_version=13;`)
	if err != nil {
		tx.Rollback()
		return fail(err)
	}
	if version < 6 {
		if _, err = tx.Exec(`WITH ranked AS(SELECT session_id,id,row_number() OVER(PARTITION BY session_id ORDER BY rowid) AS n FROM turns) UPDATE turns SET body=json_set(body,'$.order',(SELECT n FROM ranked WHERE ranked.session_id=turns.session_id AND ranked.id=turns.id))`); err != nil {
			tx.Rollback()
			return fail(err)
		}
	}
	if version < 10 {
		var present bool
		if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('operations') WHERE name='kind')").Scan(&present); err != nil {
			tx.Rollback()
			return fail(err)
		}
		if !present {
			_, err = tx.Exec("ALTER TABLE operations ADD COLUMN kind TEXT NOT NULL DEFAULT ''")
		}
		if err != nil {
			tx.Rollback()
			return fail(err)
		}
	}
	_, err = tx.Exec("INSERT OR IGNORE INTO metadata VALUES(1,?,?)", domain.NewID(), domain.NewID())
	if err != nil {
		tx.Rollback()
		return fail(err)
	}
	// Legacy archives have no reliable age. Start a complete grace period once,
	// including when an older worker archived an item after a downgrade.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, table := range []string{"sessions", "workspaces"} {
		if _, err = tx.Exec("UPDATE "+table+" SET body=json_set(body,'$.archivedAt',?) WHERE json_extract(body,'$.archived')=1 AND json_extract(body,'$.archivedAt') IS NULL", now); err != nil {
			tx.Rollback()
			return fail(err)
		}
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO archive_policy SELECT 1,json_object('deviceId',device_id,'days',30,'revision',1) FROM metadata WHERE id=1`); err != nil {
		tx.Rollback()
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	s := &Store{db: db, path: path, changed: make(chan struct{})}
	if err = db.QueryRow("SELECT device_id,generation FROM metadata WHERE id=1").Scan(&s.deviceID, &s.generation); err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *Store) Close() error     { return s.db.Close() }
func (s *Store) DeviceID() string { return s.deviceID }

func (s *Store) Item(sessionID, itemID string) (domain.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return load[domain.Item](s.db.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", sessionID, itemID))
}

// commit publishes a wake only after the journal and its projection are durable.
// Callers hold mu, so registering a waiter cannot miss a concurrent commit.
func (s *Store) commit(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	close(s.changed)
	s.changed = make(chan struct{})
	return nil
}

// WaitEvents waits for journal progress without holding the database connection.
// The journal cursor remains authoritative; the channel is only a wake-up hint.
func (s *Store) WaitEvents(ctx context.Context, after int64, generation string, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		s.mu.Lock()
		changed := s.changed
		var sequence int64
		err := s.db.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&sequence)
		valid := generation == s.generation && after >= 0 && after <= sequence
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if !valid {
			return errors.New("snapshot required: generation or cursor changed")
		}
		if sequence > after {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-changed:
		}
	}
}
func blob(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func load[T any](row *sql.Row) (T, error) {
	var v T
	var b []byte
	if err := row.Scan(&b); err != nil {
		return v, err
	}
	err := json.Unmarshal(b, &v)
	return v, err
}
func rowsOf[T any](q interface {
	Query(string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]T, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var b []byte
		var v T
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func journal(tx *sql.Tx, e domain.Event) (domain.Event, error) {
	encoded, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	// Leave room for page framing and source session metadata. Reject inside
	// the transaction so an unpageable event never blocks every later cursor.
	if len(encoded) > 512<<10 {
		return e, errors.New("event exceeds recoverable page budget")
	}
	r, err := tx.Exec("INSERT INTO journal(body) VALUES(?)", encoded)
	if err != nil {
		return e, err
	}
	e.Sequence, err = r.LastInsertId()
	return e, err
}
func (s *Store) CreateWorkspace(name string, folders []string) (domain.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := domain.Workspace{ID: domain.NewID(), DeviceID: s.deviceID, Name: strings.TrimSpace(name), Folders: folders, Revision: 1}
	if w.Name == "" || len(folders) == 0 {
		return w, errors.New("workspace requires a name and primary directory")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return w, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO workspaces VALUES(?,?)", w.ID, blob(w)); err != nil {
		return w, err
	}
	if _, err = journal(tx, domain.Event{Type: "workspace", Workspace: &w}); err != nil {
		return w, err
	}
	return w, s.commit(tx)
}
func (s *Store) UpdateWorkspace(w domain.Workspace) (domain.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", w.ID))
	if err != nil {
		return w, err
	}
	if w.Revision != old.Revision {
		return w, errors.New("workspace revision conflict")
	}
	if w.Archived != old.Archived {
		return w, errors.New("use the archive action to change workspace archive state")
	}
	if w.Name == "" || len(w.Folders) == 0 || w.DeviceID != s.deviceID {
		return w, errors.New("invalid workspace")
	}
	w.Revision++
	tx, err := s.db.Begin()
	if err != nil {
		return w, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE workspaces SET body=? WHERE id=?", blob(w), w.ID); err != nil {
		return w, err
	}
	if _, err = journal(tx, domain.Event{Type: "workspace", Workspace: &w}); err != nil {
		return w, err
	}
	return w, s.commit(tx)
}
func operation(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (domain.Operation, error) {
	var op domain.Operation
	var deletedSession string
	err := q.QueryRow("SELECT session_id FROM deleted_operations WHERE id=?", id).Scan(&deletedSession)
	if err == nil {
		return domain.Operation{ID: id, SessionID: deletedSession, State: "rejected", Deleted: true, Error: "所属会话已彻底删除，不能重放此操作"}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return op, err
	}
	err = q.QueryRow("SELECT id,session_id,state,digest,error,kind FROM operations WHERE id=?", id).Scan(&op.ID, &op.SessionID, &op.State, &op.Digest, &op.Error, &op.Kind)
	return op, err
}
func (s *Store) Operation(id string) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return operation(s.db, id)
}
func (s *Store) Accept(in domain.Submission, launch ...domain.LaunchContext) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptLocked(in, 0, false, "", launch...)
}
func (s *Store) AcceptForHarness(in domain.Submission, harnessID string, launch domain.LaunchContext) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptLocked(in, 0, false, harnessID, launch)
}
func (s *Store) acceptLocked(in domain.Submission, queueRevision int64, immediate bool, acceptedHarness string, launch ...domain.LaunchContext) (domain.Operation, error) {
	var none domain.Operation
	if in.ValidateContent() != nil || in.OperationID == "" {
		return none, errors.New("submission requires an operation ID and 1–131072 bytes of text")
	}
	h := sha256.Sum256(blob(in))
	digest := hex.EncodeToString(h[:])
	existing, err := operation(s.db, in.OperationID)
	if err == nil {
		if existing.Digest != digest {
			return none, errors.New("operation ID already used with different input")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	var queued, emptyQueue *domain.QueueState
	if queueRevision > 0 {
		q, e := s.queueLocked(in.SessionID)
		if e != nil {
			return none, e
		}
		index := 0
		if immediate {
			index = -1
			for i, item := range q.Items {
				if item.Input.OperationID == in.OperationID {
					index = i
					break
				}
			}
		}
		if q.Revision != queueRevision || (q.Paused && !immediate) || index < 0 || index >= len(q.Items) || q.Items[index].State != "" || string(blob(q.Items[index].Input)) != string(blob(in)) {
			return none, errors.New("queue changed or unavailable")
		}
		q.Items[index].State = "dispatching"
		q.Revision++
		queued = &q
	}
	w, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", in.WorkspaceID))
	if err != nil {
		return none, err
	}
	if w.Archived {
		return none, errors.New("restore the workspace before sending")
	}
	var session domain.Session
	if in.SessionID != "" {
		session, err = load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
		if err != nil {
			return none, err
		}
		if session.Archived {
			return none, errors.New("restore the session before sending")
		}
		if session.Origin != nil && session.Origin.ResumeRef == "" && session.ThreadID == "" {
			return none, errors.New("此导入历史仅可查看")
		}
		if session.WorkspaceID != in.WorkspaceID || !(session.Status.CanSubmit()) {
			return none, errors.New("session is unavailable for a new turn")
		}
		if session.Origin != nil && session.ThreadID == "" && session.Context.Repository == "" && len(launch) > 0 && launch[0].Repository != "" {
			accepted := launch[0]
			if accepted.DeviceID != session.Context.DeviceID || accepted.CWD != session.Context.CWD || !slices.Equal(accepted.AdditionalDirectories, session.Context.AdditionalDirectories) {
				return none, errors.New("imported execution scope changed")
			}
			session.Context = accepted
			session.GitBranch, session.GitHead = accepted.Branch, accepted.Head
		}
		if queued == nil {
			q, e := s.queueLocked(session.ID)
			if e != nil {
				return none, e
			}
			if q.Paused && len(q.Items) == 0 && !in.Continue {
				q.Paused = false
				q.Reason = ""
				q.Revision++
				emptyQueue = &q
			}
		}
		if session.Config.Model != in.Config.Model {
			session.ContextUsage = nil
			if err = s.db.QueryRow("SELECT COALESCE(MAX(json_extract(body,'$.order')),0) FROM turns WHERE session_id=?", session.ID).Scan(&session.ContextUsageAfterTurnOrder); err != nil {
				return none, err
			}
		}
		session.Config = in.Config
		session.Recovery = nil
		session.Status = "starting"
		session.Revision++
	} else {
		title := strings.SplitN(strings.TrimSpace(in.Text), "\n", 2)[0]
		if title == "" && len(in.Attachments) > 0 {
			title = in.Attachments[0].Name
		}
		if title == "" && len(in.Skills) > 0 {
			title = in.Skills[0].Name
		}
		if utf8.RuneCountInString(title) > 60 {
			title = string([]rune(title)[:60])
		}
		session = domain.Session{ID: domain.NewID(), WorkspaceID: w.ID, Title: title, HarnessID: in.HarnessID, Status: "starting", Revision: 1, Config: in.Config, Context: domain.LaunchContext{DeviceID: s.deviceID, CWD: w.Folders[0], AdditionalDirectories: append([]string(nil), w.Folders[1:]...)}}
	}
	if in.SessionID == "" && acceptedHarness != "" {
		session.HarnessID = acceptedHarness
	}
	if in.SessionID == "" && len(launch) > 0 {
		session.Context = launch[0]
		session.GitBranch = session.Context.Branch
		session.GitHead = session.Context.Head
	}
	op := domain.Operation{ID: in.OperationID, SessionID: session.ID, State: "accepted", Digest: digest}
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO sessions VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", session.ID, blob(session)); err != nil {
		return none, err
	}
	if in.SessionID == "" {
		job := TitleJob{Session: session, Prompt: in.Text, InputID: "input:" + op.ID}
		if _, err = tx.Exec("INSERT INTO title_jobs VALUES(?,?,?)", session.ID, titlePending, blob(job)); err != nil {
			return none, err
		}
	}
	if _, err = tx.Exec("INSERT INTO operations(id,session_id,state,digest) VALUES(?,?,?,?)", op.ID, op.SessionID, op.State, digest); err != nil {
		return none, err
	}
	var item *domain.Item
	if queued == nil {
		now := time.Now()
		item = &domain.Item{ID: "input:" + op.ID, Kind: "user", ReferencePositions: in.ReferencePositions, Skills: in.Skills, Attachments: in.Attachments, Text: in.Text, Final: true, Status: "dispatching", SentAt: &now}
		if err = putItem(tx, session.ID, item); err != nil {
			return none, err
		}
	}
	if _, err = journal(tx, domain.Event{Type: "submission", SessionID: session.ID, Session: &session, Operation: &op, Item: item}); err != nil {
		return none, err
	}
	if queued != nil {
		if err = putItem(tx, session.ID, &domain.Item{ID: "input:" + op.ID, Kind: "queued", ReferencePositions: in.ReferencePositions, Skills: in.Skills, Attachments: in.Attachments, Text: in.Text, Final: true}); err != nil {
			return none, err
		}
		if err = putQueue(tx, *queued); err != nil {
			return none, err
		}
	}
	if emptyQueue != nil {
		if err = putQueue(tx, *emptyQueue); err != nil {
			return none, err
		}
	}
	return op, s.commit(tx)
}
func (s *Store) SetOperation(id string, state domain.OperationState, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := operation(s.db, id)
	if err != nil {
		return err
	}
	op.State = state
	op.Error = message
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if op.Kind == "compact" {
		if err = settleContextAction(tx, &op); err != nil {
			return err
		}
		state, message = op.State, op.Error
	}
	if _, err = tx.Exec("UPDATE operations SET state=?,error=? WHERE id=?", state, message, id); err != nil {
		return err
	}
	if state == "indeterminate" && op.Kind != "compact" {
		session, e := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", op.SessionID))
		if e != nil {
			return e
		}
		session.Status = "unknown"
		session.Error = "有消息的送达结果尚待核对"
		session.Recovery = nil
		session.Revision++
		if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
			return err
		}
	}
	if err = queueOutcome(tx, op); err != nil {
		return err
	}
	if _, err = journal(tx, domain.Event{Type: "operation", SessionID: op.SessionID, Operation: &op}); err != nil {
		return err
	}
	return s.commit(tx)
}
func putItem(tx *sql.Tx, sid string, item *domain.Item) error {
	var pos int64
	var body []byte
	var err error
	if item.Kind == "user" && item.SentAt == nil {
		err = tx.QueryRow("SELECT position,body FROM items WHERE session_id=? AND id=?", sid, item.ID).Scan(&pos, &body)
	} else {
		err = tx.QueryRow("SELECT position FROM items WHERE session_id=? AND id=?", sid, item.ID).Scan(&pos)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if err = tx.QueryRow("SELECT COALESCE(MAX(position),0)+1 FROM items WHERE session_id=?", sid).Scan(&pos); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if len(body) > 0 {
		var previous domain.Item
		if err := json.Unmarshal(body, &previous); err != nil {
			return err
		}
		item.SentAt = previous.SentAt
		if previous.Kind == "queued" && item.Kind == "user" {
			now := time.Now()
			item.SentAt = &now
		}
	}
	item.Order = pos
	_, err = tx.Exec("INSERT INTO items VALUES(?,?,?,?) ON CONFLICT(session_id,id) DO UPDATE SET body=excluded.body", sid, item.ID, pos, blob(item))
	return err
}
func (s *Store) Apply(sid string, e domain.Event) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.SessionID = sid
	tx, err := s.db.Begin()
	if err != nil {
		return e, err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", sid))
	if err != nil {
		return e, err
	}
	switch e.Type {
	case "context.usage":
		if e.ContextUsage == nil || e.ContextUsage.TurnID == "" || e.ContextUsage.UsedTokens != nil && *e.ContextUsage.UsedTokens < 0 || e.ContextUsage.LimitTokens != nil && *e.ContextUsage.LimitTokens <= 0 {
			return e, errors.New("invalid context usage")
		}
		newer := true
		if session.ContextUsageAfterTurnOrder > 0 {
			var order int64
			if err = tx.QueryRow("SELECT COALESCE((SELECT json_extract(body,'$.order') FROM turns WHERE session_id=? AND id=?),0)", sid, e.ContextUsage.TurnID).Scan(&order); err != nil {
				return e, err
			}
			newer = order > session.ContextUsageAfterTurnOrder
		}
		if previous := session.ContextUsage; previous != nil && previous.TurnID != e.ContextUsage.TurnID {
			var before, after int64
			if err = tx.QueryRow(`SELECT COALESCE((SELECT json_extract(body,'$.order') FROM turns WHERE session_id=? AND id=?),0), COALESCE((SELECT json_extract(body,'$.order') FROM turns WHERE session_id=? AND id=?),0)`, sid, previous.TurnID, sid, e.ContextUsage.TurnID).Scan(&before, &after); err != nil {
				return e, err
			}
			newer = newer && (before == 0 || after == 0 || after >= before)
		}
		if newer {
			session.ContextUsage = e.ContextUsage
		}
		e.ContextUsage = session.ContextUsage
	case "session.title":
		if strings.TrimSpace(e.Text) == "" {
			return e, errors.New("empty session title")
		}
		session.SourceTitle = e.Text
		if !session.TitleOverride {
			if session.Title != e.Text {
				session.TitleRevision++
			}
			session.Title = e.Text
		}
	case "plan", "plan.text":
		if e.Plan == nil || e.Plan.TurnID == "" {
			return e, errors.New("missing plan or turn identity")
		}
		previous, _ := load[domain.Plan](tx.QueryRow("SELECT body FROM plans WHERE session_id=?", sid))
		if e.Type == "plan.text" {
			if previous.TurnID != e.Plan.TurnID {
				previous = domain.Plan{TurnID: e.Plan.TurnID}
			}
			previous.Narrative = e.Text
			e.Plan = &previous
			e.Type, e.Text = "plan", ""
		} else if previous.TurnID == e.Plan.TurnID {
			e.Plan.Narrative = previous.Narrative
		}
		if _, err = tx.Exec("INSERT INTO plans VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET body=excluded.body", sid, blob(e.Plan)); err != nil {
			return e, err
		}
	case "session":
		if e.Session == nil || e.Session.ID != sid {
			return e, errors.New("invalid session event")
		}
		completion := session.Completion
		if session.ThreadID != e.Session.ThreadID {
			completion = nil
		}
		recovery := session.Recovery
		guard, guardedError := session.ExecutionGuard, session.Error
		revision := session.Revision
		title := session.Title
		branch, head, changed := session.GitBranch, session.GitHead, session.ContextChanged
		archived, override, sourceTitle, titleRevision := session.Archived, session.TitleOverride, session.SourceTitle, session.TitleRevision
		archivedAt := session.ArchivedAt
		contextUsage := session.ContextUsage
		contextAction := session.ContextAction
		contextUsageAfter := session.ContextUsageAfterTurnOrder
		if session.ThreadID != e.Session.ThreadID || session.Config.Model != e.Session.Config.Model {
			contextUsage = nil
		}
		session = *e.Session
		session.Completion = completion
		session.ContextAction = contextAction
		if a := session.ContextAction; a != nil && a.State.Active() && session.Status == "unknown" {
			a.State = "unknown"
			a.Error = "压缩结果尚未确认"
		}
		session.ContextUsage = contextUsage
		session.ContextUsageAfterTurnOrder = contextUsageAfter
		session.Recovery = recovery
		session.ExecutionGuard = guard
		if guard != "" {
			session.Status, session.Error = "unknown", guardedError
		}
		session.Title = title
		session.Archived, session.TitleOverride, session.SourceTitle = archived, override, sourceTitle
		session.ArchivedAt = archivedAt
		session.TitleRevision = titleRevision
		session.GitBranch, session.GitHead, session.ContextChanged = branch, head, changed
		if session.Revision < revision {
			session.Revision = revision
		}
	case "session.git":
		if e.Session == nil || e.Session.ID != sid {
			return e, errors.New("invalid Git context event")
		}
		session.GitBranch = e.Session.GitBranch
		session.GitHead = e.Session.GitHead
		session.ContextChanged = e.Session.ContextChanged
		e.Type = "session"
	case "work.upsert", "work.item":
		if err = applyWork(tx, sid, &e); err != nil {
			return e, err
		}
	case "item.upsert":
		if e.Item == nil {
			return e, errors.New("missing item")
		}
		// A delayed delta/start cannot revive a stream that authoritative history
		// already discarded. A completed source item may still confirm it later.
		previous, readErr := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", sid, e.Item.ID))
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return e, readErr
		}
		if previous.Status == "superseded" && previous.Final && !e.Item.Final {
			return e, nil
		}
		if e.Item.WorkID != "" {
			if e.Item.Kind != "request_result" {
				return e, errors.New("unsupported sourced item")
			}
			if err = requestWork(tx, session, domain.PendingRequest{WorkID: e.Item.WorkID, TurnID: e.Item.TurnID}); err != nil {
				return e, err
			}
		}
		if err = putItem(tx, sid, e.Item); err != nil {
			return e, err
		}
		if err = mirrorRequestWork(tx, sid, &e, *e.Item); err != nil {
			return e, err
		}
	case "item.delta":
		item, readErr := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", sid, e.ItemID))
		if readErr != nil {
			return e, readErr
		}
		if item.Final {
			return e, nil
		}
		item.Text += e.Text
		if len(item.Text) > 256<<10 {
			item.Text = item.Text[:256<<10] + "\n[Output limit reached]"
			item.Final = true
		}
		if err = putItem(tx, sid, &item); err != nil {
			return e, err
		}
		e.Item = &item
		e.Type = "item.upsert"
		e.Text = ""
	case "request.open", "request.resolve":
		if e.Request == nil {
			return e, errors.New("missing request")
		}
		if err = validateRequest(tx, session, *e.Request); err != nil {
			return e, err
		}
		e.Request.SessionID = sid
		if _, err = tx.Exec("INSERT INTO requests VALUES(?,?,?) ON CONFLICT(session_id,id) DO UPDATE SET body=excluded.body", sid, e.Request.ID, blob(e.Request)); err != nil {
			return e, err
		}
		anchor := domain.Item{WorkID: e.Request.WorkID, ID: "request:" + e.Request.ID, TurnID: e.Request.TurnID, Kind: "request", Text: e.Request.Title, Status: string(e.Request.State), Final: e.Request.State != "pending"}
		if err = putItem(tx, sid, &anchor); err != nil {
			return e, err
		}
		e.Item = &anchor
		if err = mirrorRequestWork(tx, sid, &e, anchor); err != nil {
			return e, err
		}
	case "turn":
		if e.Turn == nil {
			return e, errors.New("missing turn")
		}
		previous, readErr := load[domain.Turn](tx.QueryRow("SELECT body FROM turns WHERE session_id=? AND id=?", sid, e.Turn.ID))
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			return e, readErr
		}
		if err = putTurn(tx, sid, e.Turn); err != nil {
			return e, err
		}
		observeReplyCompletion(&session, previous, *e.Turn)
		if session.ExecutionGuard == "" {
			session.Status = e.Turn.Status
			session.Recovery = nil
			session.Error = e.Turn.Error
		}
		var uncertain bool
		if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM operations WHERE session_id=? AND state='indeterminate')", sid).Scan(&uncertain); err != nil {
			return e, err
		}
		if uncertain {
			session.Status = "unknown"
			session.Error = "有消息的送达结果尚待核对"
		}
	default:
		return e, fmt.Errorf("unknown domain event: %s", e.Type)
	}
	if err = observeContextAction(tx, &session, &e); err != nil {
		return e, err
	}
	session.Revision++
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), sid); err != nil {
		return e, err
	}
	e.Session = &session
	e, err = journal(tx, e)
	if err != nil {
		return e, err
	}
	return e, s.commit(tx)
}
func (s *Store) Snapshot() (domain.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := domain.Snapshot{DeviceID: s.deviceID, Generation: s.generation, ObservedAt: time.Now().UTC()}
	var err error
	policy, err := s.archivePolicyLocked()
	if err != nil {
		return out, err
	}
	out.ArchivePolicy = &policy
	if err = s.db.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&out.Sequence); err != nil {
		return out, err
	}
	if out.Workspaces, err = rowsOf[domain.Workspace](s.db, "SELECT body FROM workspaces ORDER BY rowid"); err != nil {
		return out, err
	}
	if out.Sessions, err = rowsOf[domain.Session](s.db, "SELECT body FROM sessions ORDER BY rowid"); err != nil {
		return out, err
	}
	if out.PendingRequests, err = rowsOf[domain.PendingRequest](s.db, "SELECT body FROM requests WHERE json_extract(body,'$.state') IN ('pending','submitting')"); err != nil {
		return out, err
	}
	rows, err := s.db.Query("SELECT id,session_id,state,error,kind FROM operations ORDER BY rowid DESC LIMIT 100")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Operations = []domain.Operation{}
	for rows.Next() {
		var o domain.Operation
		if err = rows.Scan(&o.ID, &o.SessionID, &o.State, &o.Error, &o.Kind); err != nil {
			return out, err
		}
		out.Operations = append(out.Operations, o)
	}
	return out, rows.Err()
}
func (s *Store) Session(id string, after int64, limit int) (domain.SessionDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionLocked(id, after, limit, nil)
}

func (s *Store) sessionLocked(id string, after int64, limit int, window *domain.HistoryQuery) (domain.SessionDetail, error) {
	out := domain.SessionDetail{}
	var err error
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if out.Session, err = load[domain.Session](s.historyQueries().QueryRow("SELECT body FROM sessions WHERE id=?", id)); err != nil {
		return out, err
	}
	if out.Queue, err = s.queueLocked(id); err != nil {
		return out, err
	}
	plan, planErr := load[domain.Plan](s.historyQueries().QueryRow("SELECT body FROM plans WHERE session_id=?", id))
	if planErr != nil && !errors.Is(planErr, sql.ErrNoRows) {
		return out, planErr
	}
	if planErr == nil {
		out.Plan = &plan
	}
	if out.Works, err = rowsOf[domain.WorkActivity](s.historyQueries(), `SELECT body FROM works WHERE rowid IN(SELECT rowid FROM works WHERE session_id=? ORDER BY CASE WHEN json_extract(body,'$.state') IN('running','starting','waiting') THEN 0 ELSE 1 END,rowid DESC LIMIT 129) ORDER BY rowid`, id); err != nil {
		return out, err
	}
	out.Works, out.WorksTruncated = domain.LimitWorkRoster(out.Works)
	if window != nil && window.ThreadID != "" && window.ThreadID != out.Session.ThreadID {
		return out, errors.New("会话历史已改变，请重新加载")
	}
	queryItems := "SELECT body FROM items WHERE session_id=? AND position>? AND json_extract(body,'$.kind')!='queued' ORDER BY position LIMIT ?"
	itemArgs := []any{id, after, limit + 1}
	backwards := false
	rounds := window != nil && window.Rounds > 0
	if rounds {
		q := *window
		q.WindowFirst, q.WindowLast, err = s.historyRoundRangeLocked(q)
		if err != nil {
			return out, err
		}
		window = &q
		queryItems = "SELECT body FROM items WHERE " + historyVisibleSQL + " AND position>=? AND position<=? AND position>? ORDER BY position"
		itemArgs = []any{id, q.WindowFirst, q.WindowLast, q.After}
	} else if window != nil {
		queryItems, itemArgs, backwards, err = s.historyItemsQueryLocked(*window, limit)
		if err != nil {
			return out, err
		}
	}
	if rounds {
		out.Items, err = s.historyRoundChunkLocked(queryItems, itemArgs)
	} else {
		out.Items, err = rowsOf[domain.Item](s.historyQueries(), queryItems, itemArgs...)
	}
	if err != nil {
		return out, err
	}
	if !rounds && len(out.Items) > limit {
		out.HasMore = true
		out.Items = out.Items[:limit]
	}
	if backwards {
		slices.Reverse(out.Items)
	}
	contextID := ""
	if window != nil && len(out.Items) > 0 {
		first := out.Items[0]
		context, e := load[domain.Item](s.historyQueries().QueryRow("SELECT body FROM items WHERE session_id=? AND position<=? AND "+historyPromptSQL+" ORDER BY position DESC LIMIT 1", id, first.Order))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e == nil && context.ID != first.ID {
			contextID = context.ID
			out.Items = append([]domain.Item{context}, out.Items...)
		}
	}

	requestIDs := []string{}
	turnIDs := []string{}
	for _, item := range out.Items {
		if item.Kind == "request" {
			requestIDs = append(requestIDs, strings.TrimPrefix(item.ID, "request:"))
		}
		if item.TurnID != "" {
			turnIDs = append(turnIDs, item.TurnID)
		}
	}
	queryIDs := func(table string, ids []string, active string) (string, []any) {
		args := []any{id}
		condition := active
		if len(ids) > 0 {
			slots := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
			condition += " OR id IN (" + slots + ")"
			for _, v := range ids {
				args = append(args, v)
			}
		}
		return "SELECT body FROM " + table + " WHERE session_id=? AND (" + condition + ") ORDER BY rowid", args
	}
	query, args := queryIDs("requests", requestIDs, "json_extract(body,'$.state') IN ('pending','submitting')")
	requests, err := rowsOf[domain.PendingRequest](s.historyQueries(), query, args...)
	if err != nil {
		return out, err
	}
	query, args = queryIDs("turns", turnIDs, "json_extract(body,'$.status')='running'")
	turns, err := rowsOf[domain.Turn](s.historyQueries(), query, args...)
	if err != nil {
		return out, err
	}
	for i := range out.Items {
		if len(blob(out.Items[i])) > 256<<10 {
			out.Items[i].Text = strings.ToValidUTF8(out.Items[i].Text[:min(len(out.Items[i].Text), 128<<10)], "�") + "\n[Output too large for a history page; showing a bounded preview]"
			out.Items[i].Name = ""
		}
	}
	for {
		requestSet := map[string]bool{}
		turnSet := map[string]bool{}
		for _, item := range out.Items {
			if item.Kind == "request" {
				requestSet[strings.TrimPrefix(item.ID, "request:")] = true
			}
			turnSet[item.TurnID] = true
		}
		out.Requests = nil
		out.Turns = nil
		for _, r := range requests {
			if requestSet[r.ID] || r.State.AwaitingResolution() {
				out.Requests = append(out.Requests, r)
			}
		}
		for _, t := range turns {
			if turnSet[t.ID] || t.Status == "running" {
				out.Turns = append(out.Turns, t)
			}
		}
		budget := 768 << 10
		if window != nil {
			budget -= 4096
		}
		if len(blob(out)) <= budget {
			break
		}
		if len(out.Items) <= 1 {
			return out, errors.New("session metadata exceeds history page budget")
		}
		if backwards {
			at := 0
			if contextID != "" {
				at = 1
			}
			if out.Items[at].Kind == "user" {
				if contextID != "" {
					out.Items = out.Items[1:]
				}
				contextID = out.Items[0].ID
				at = 1
			}
			if len(out.Items) <= at+1 {
				return out, errors.New("session metadata exceeds history page budget")
			}
			out.Items = slices.Delete(out.Items, at, at+1)
		} else {
			out.Items = out.Items[:len(out.Items)-1]
		}
		out.HasMore = true
	}
	if window != nil {
		out.History, err = s.historyWindowLocked(id, out.Items, contextID)
		if err != nil {
			return out, err
		}
		if rounds && window.WindowFirst > 0 {
			out.History.WindowFirst, out.History.WindowLast = window.WindowFirst, window.WindowLast
			if err = s.historyQueries().QueryRow("SELECT EXISTS(SELECT 1 FROM items WHERE "+historyVisibleSQL+" AND position<?), EXISTS(SELECT 1 FROM items WHERE "+historyVisibleSQL+" AND position>?)", id, window.WindowFirst, id, window.WindowLast).Scan(&out.History.Older, &out.History.Newer); err != nil {
				return out, err
			}
		}
		out.HasMore = out.History.Older
		// Tool summaries are small, but still belong to the transport budget.
		if len(blob(out)) > 768<<10 {
			return out, errors.New("session metadata exceeds history page budget")
		}
	}

	err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&out.Sequence)
	return out, err
}
func (s *Store) Events(after int64, limit int) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query("SELECT sequence,body FROM journal WHERE sequence>? ORDER BY sequence LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		var b []byte
		if err = rows.Scan(&e.Sequence, &b); err != nil {
			return nil, err
		}
		seq := e.Sequence
		if err = json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		e.Sequence = seq
		out = append(out, e)
	}
	return out, rows.Err()
}

// Recover never dispatches saved input. An old dispatch may already have caused an external effect.
func (s *Store) Recover() error {
	s.mu.Lock()
	_, reloadErr := s.db.Exec(`UPDATE session_reloads SET body=json_set(body,'$.state','failed','$.error','重新加载期间执行服务重启；未发送消息，可重新操作') WHERE json_extract(body,'$.state')='running'`)
	s.mu.Unlock()
	if reloadErr != nil {
		return reloadErr
	}
	// A title-only model request interrupted by worker death is not replayed.
	// Pending requests remain safe to start; a running one retains its fallback.
	s.mu.Lock()
	_, titleErr := s.db.Exec("UPDATE title_jobs SET state=? WHERE state=?", titleFailed, titleRunning)
	s.mu.Unlock()
	if titleErr != nil {
		return titleErr
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, err = s.db.Exec(`UPDATE history_operations SET body=json_set(body,'$.state','failed','$.error','操作在确认分叉前中断，原会话保持不变，可重新尝试') WHERE json_extract(body,'$.state')='prepared'`); err != nil {
		s.mu.Unlock()
		return err
	}
	rows, err := s.db.Query("SELECT id FROM operations WHERE state IN ('accepted','dispatched')")
	if err != nil {
		s.mu.Unlock()
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.SetOperation(id, "indeterminate", "Worker restarted before external acceptance was confirmed"); err != nil {
			return err
		}
	}
	for _, session := range snapshot.Sessions {
		if _, err = s.PauseQueue(session.ID, "worker restarted"); err != nil {
			return err
		}
		unsettled, e := s.HasUnsettledOperation(session.ID)
		if e != nil {
			return e
		}
		if session.Status.Active() || unsettled {
			if _, e = s.SetRecovery(session.ID, nil); e != nil {
				return e
			}
			detail, e := s.Session(session.ID, 0, 1)
			if e != nil {
				return e
			}
			for _, turn := range detail.Turns {
				if turn.Status == "running" {
					turn.Status = "unknown"
					turn.Error = "Previous execution needs reconciliation; input was not replayed"
					if _, err = s.Apply(session.ID, domain.Event{Type: "turn", Turn: &turn}); err != nil {
						return err
					}
				}
			}
			session.Status = "unknown"
			session.Error = "Previous execution needs reconciliation; input was not replayed"
			if _, err = s.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
				return err
			}
		}
	}
	for _, req := range snapshot.PendingRequests {
		req.State = "invalidated"
		req.Revision++
		if _, err = s.Apply(req.SessionID, domain.Event{Type: "request.resolve", Request: &req}); err != nil {
			return err
		}
	}
	return nil
}
