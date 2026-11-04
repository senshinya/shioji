package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/store"
)

type Config struct{ Directory, CodexExecutable, CodexHome string }
type WorkspaceInput = protocol.WorkspaceInput
type SessionQuery = protocol.SessionQuery
type EventsQuery = protocol.EventsQuery
type EventPage = protocol.EventPage
type AnswerInput = protocol.AnswerInput
type Server struct {
	config     Config
	owner      *Owner
	listener   net.Listener
	store      *store.Store
	runtime    *codex.Runtime
	readiness  domain.Readiness
	dispatchMu sync.Mutex
	bound      map[string]bool
	mu         sync.Mutex
	conns      map[net.Conn]bool
	wg         sync.WaitGroup
	once       sync.Once
	ctx        context.Context
	cancel     context.CancelFunc
}

func New(ctx context.Context, config Config) (*Server, error) {
	owner, err := Acquire(config.Directory)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(filepath.Join(config.Directory, "state.sqlite"))
	if err != nil {
		owner.Close()
		return nil, err
	}
	if err = db.Recover(); err != nil {
		db.Close()
		owner.Close()
		return nil, err
	}
	listener, err := owner.Listen()
	if err != nil {
		db.Close()
		owner.Close()
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s := &Server{config: config, owner: owner, listener: listener, store: db, ctx: workerCtx, cancel: cancel, bound: map[string]bool{}, conns: map[net.Conn]bool{}}
	startup, cancelStartup := context.WithTimeout(ctx, 20*time.Second)
	defer cancelStartup()
	if err = checkPreviousProcess(config.Directory); err == nil {
		if err = recordProcess(config.Directory, 0); err == nil {
			s.runtime, err = codex.Start(startup, codex.Options{Executable: config.CodexExecutable, Home: config.CodexHome, OnEvent: s.onEvent, OnStart: func(pid int) error { return recordProcess(config.Directory, pid) }})
			if err != nil {
				os.Remove(filepath.Join(config.Directory, "runtime-process.json"))
			}
		}
	}
	if err != nil {
		s.readiness = domain.Readiness{State: "unavailable", Error: err.Error()}
	}
	return s, nil
}
func (s *Server) Directory() string { return s.config.Directory }
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			conn.Close()
			return nil
		}
		s.conns[conn] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			defer func() { s.mu.Lock(); delete(s.conns, conn); s.mu.Unlock() }()
			s.serveConnection(conn)
		}()
	}
}
func (s *Server) serveConnection(conn net.Conn) {
	if authorizeLocal(conn) != nil {
		return
	}
	codec, epoch, err := protocol.Welcome(conn, s.store.DeviceID())
	if err != nil {
		return
	}
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		var f protocol.Frame
		if err = codec.Read(&f); err != nil {
			return
		}
		if f.Type != "command" || f.Epoch != epoch || f.DeviceID != s.store.DeviceID() || f.ID == "" {
			return
		}
		out, callErr := s.handle(f.Method, f.Body)
		result := protocol.Frame{Type: "result", ID: f.ID, Epoch: epoch, DeviceID: s.store.DeviceID()}
		if callErr != nil {
			result.Error = callErr.Error()
			if errors.Is(callErr, sql.ErrNoRows) {
				result.ErrorCode = "notFound"
			}
		} else {
			result.Body, err = json.Marshal(out)
			if err != nil {
				result.Error = "Could not encode response"
			}
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err = codec.Write(result); err != nil {
			if len(result.Body) > protocol.MaxFrame {
				result.Body = nil
				result.Error = "Response exceeds frame limit; request a smaller page"
				codec.Write(result)
			} else {
				return
			}
		}
	}
}
func decode[T any](body []byte) (T, error) { var v T; err := json.Unmarshal(body, &v); return v, err }
func (s *Server) handle(method string, body []byte) (any, error) {
	switch method {
	case "snapshot":
		return s.store.Snapshot()
	case "readiness":
		if s.runtime == nil {
			return s.readiness, nil
		}
		return s.runtime.Readiness(), nil
	case "session":
		q, err := decode[SessionQuery](body)
		if err != nil {
			return nil, err
		}
		return s.store.Session(q.ID, q.After, q.Limit)
	case "operation":
		q, err := decode[struct {
			ID string `json:"id"`
		}](body)
		if err != nil {
			return nil, err
		}
		return s.store.Operation(q.ID)
	case "events":
		q, err := decode[EventsQuery](body)
		if err != nil {
			return nil, err
		}
		snap, err := s.store.Snapshot()
		if err != nil {
			return nil, err
		}
		if q.Generation != snap.Generation || q.After > snap.Sequence {
			return nil, errors.New("snapshot required: generation or cursor changed")
		}
		events, err := s.store.Events(q.After, 100)
		if err != nil {
			return nil, err
		}
		page := EventPage{Generation: snap.Generation}
		size := 0
		for _, e := range events {
			b, _ := json.Marshal(e)
			if size+len(b) > 768<<10 {
				break
			}
			size += len(b)
			page.Events = append(page.Events, e)
		}
		return page, nil
	case "workspace.create":
		in, err := decode[WorkspaceInput](body)
		if err != nil {
			return nil, err
		}
		folders, err := validateFolders(in.Folders)
		if err != nil {
			return nil, err
		}
		return s.store.CreateWorkspace(in.Name, folders)
	case "workspace.update":
		in, err := decode[domain.Workspace](body)
		if err != nil {
			return nil, err
		}
		folders, err := validateFolders(in.Folders)
		if err != nil {
			return nil, err
		}
		in.Folders = folders
		return s.store.UpdateWorkspace(in)
	case "session.submit":
		in, err := decode[domain.Submission](body)
		if err != nil {
			return nil, err
		}
		return s.submit(in)
	case "request.respond":
		in, err := decode[AnswerInput](body)
		if err != nil {
			return nil, err
		}
		if s.runtime == nil {
			return nil, errors.New("runtime unavailable")
		}
		detail, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return nil, err
		}
		for _, req := range detail.Requests {
			if req.ID == in.Answer.RequestID {
				return nil, s.runtime.Respond(in.Answer)
			}
		}
		return nil, errors.New("request does not belong to this session")
	case "turn.interrupt":
		in, err := decode[struct {
			SessionID string `json:"sessionId"`
			TurnID    string `json:"turnId"`
		}](body)
		if err != nil {
			return nil, err
		}
		if s.runtime == nil {
			return nil, errors.New("runtime unavailable")
		}
		detail, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return nil, err
		}
		for _, turn := range detail.Turns {
			if turn.ID == in.TurnID && turn.Status == "running" {
				ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
				defer cancel()
				return nil, s.runtime.Interrupt(ctx, detail.Session, in.TurnID)
			}
		}
		return nil, errors.New("turn is not active")
	default:
		return nil, errors.New("unsupported worker command")
	}
}
func validateFolders(paths []string) ([]string, error) {
	if len(paths) == 0 || len(paths) > 32 {
		return nil, errors.New("select a primary directory and at most 31 additional directories")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, errors.New("directory must be absolute on the owning device")
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return nil, errors.New("workspace directory is unavailable")
		}
		dir, err := os.Open(canonical)
		if err != nil {
			return nil, err
		}
		_, err = dir.Readdirnames(1)
		dir.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out, nil
}
func (s *Server) submit(in domain.Submission) (domain.Operation, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	existing, err := s.store.Operation(in.OperationID)
	if err == nil {
		_ = existing
		return s.store.Accept(in)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, err
	}
	if s.ctx.Err() != nil || s.runtime == nil {
		return domain.Operation{}, errors.New("runtime unavailable")
	}
	if err = s.runtime.Validate(in.Config); err != nil {
		return domain.Operation{}, err
	}
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return domain.Operation{}, err
	}
	var paths []string
	found := false
	for _, w := range snapshot.Workspaces {
		if w.ID == in.WorkspaceID {
			paths = w.Folders
			found = true
			break
		}
	}
	if !found {
		return domain.Operation{}, errors.New("workspace unavailable")
	}
	if in.SessionID != "" {
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return domain.Operation{}, err
		}
		paths = append([]string{d.Session.Context.CWD}, d.Session.Context.AdditionalDirectories...)
	}
	validated, err := validateFolders(paths)
	if err != nil {
		return domain.Operation{}, err
	}
	if strings.Join(validated, "\x00") != strings.Join(paths, "\x00") {
		return domain.Operation{}, errors.New("execution directory identity changed")
	}
	op, err := s.store.Accept(in)
	if err != nil {
		return op, err
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.dispatch(in, op) }()
	return op, nil
}
func (s *Server) dispatch(in domain.Submission, op domain.Operation) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if err := s.store.SetOperation(op.ID, "dispatched", ""); err != nil {
		return
	}
	detail, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		return
	}
	session := detail.Session
	var turnID string
	if session.ThreadID == "" {
		session.ThreadID, err = s.runtime.Create(ctx, session)
		if err == nil {
			_, err = s.store.Apply(session.ID, domain.Event{Type: "session", Session: &session})
		}
	} else {
		s.mu.Lock()
		bound := s.bound[session.ID]
		s.mu.Unlock()
		if !bound {
			err = s.runtime.Resume(ctx, session)
		}
	}
	if err == nil {
		s.mu.Lock()
		s.bound[session.ID] = true
		s.mu.Unlock()
		turnID, err = s.runtime.Submit(ctx, session, op.ID, in.Text)
	}
	state := "confirmed"
	delivery := "sent"
	if err != nil {
		state = "rejected"
		delivery = "failed"
		if codex.IsUncertain(err) {
			state = "indeterminate"
			delivery = "unknown"
		}
		session.Status = "failed"
		if state == "indeterminate" {
			session.Status = "unknown"
		}
		session.Error = err.Error()
		if _, writeErr := s.store.Apply(session.ID, domain.Event{Type: "session", Session: &session}); writeErr != nil {
			return
		}
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if err = s.store.SetOperation(op.ID, state, message); err != nil {
		return
	}
	// Update the existing accepted input; a wire userMessage never creates a duplicate.
	_, err = s.store.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + op.ID, TurnID: turnID, Kind: "user", Text: in.Text, Final: true, Status: delivery}})
	if err != nil {
		s.cancel()
	}
}
func (s *Server) onEvent(e domain.Event) error {
	if e.SessionID == "" {
		return nil
	}
	if e.Type == "runtime.lost" {
		detail, err := s.store.Session(e.SessionID, 0, 1)
		if err != nil {
			return err
		}
		session := detail.Session
		if session.Status != "running" && session.Status != "starting" {
			return nil
		}
		for _, turn := range detail.Turns {
			if turn.Status == "running" {
				turn.Status = "unknown"
				turn.Error = "Codex process stopped; the in-flight turn needs reconciliation"
				if _, err := s.store.Apply(e.SessionID, domain.Event{Type: "turn", Turn: &turn}); err != nil {
					return err
				}
			}
		}
		session.Status = "unknown"
		session.Error = "Codex process stopped; the in-flight turn needs reconciliation"
		e.Type = "session"
		e.Session = &session
	}
	_, err := s.store.Apply(e.SessionID, e)
	return err
}
func (s *Server) Close() error {
	var result error
	s.once.Do(func() {
		s.cancel()
		s.listener.Close()
		s.mu.Lock()
		for conn := range s.conns {
			conn.Close()
		}
		s.mu.Unlock()
		if s.runtime != nil {
			s.runtime.Close()
			os.Remove(filepath.Join(s.config.Directory, "runtime-process.json"))
		}
		s.wg.Wait()
		result = s.store.Close()
		s.owner.Close()
	})
	return result
}
func DefaultDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Shioji", "worker"), nil
}
func (s *Server) String() string { return fmt.Sprintf("Shioji worker %s", s.store.DeviceID()) }
