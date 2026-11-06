package managedexec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Identity struct {
	PID  int
	Sec  int64
	Usec int32
}
type Ticket struct {
	ID, Kind, Ref string
	Parent        Identity
	Root, Anchor  Identity
	Group         int
	State         string
}
type scopeState struct {
	Generation string
	Active     bool
	Parents    map[string]Identity
	Launches   map[string]bool
}
type Manager struct {
	Directory, Executable string
	mu                    sync.Mutex
	live                  map[string]liveLaunch
}
type liveLaunch struct {
	scope  *Scope
	ticket Ticket
}
type OwnershipError struct {
	Session, Generation string
	Cause               error
}

func (e *OwnershipError) Error() string { return e.Cause.Error() }
func (e *OwnershipError) Unwrap() error { return e.Cause }

type Scope struct {
	manager                        *Manager
	Session, Generation, Directory string
}

func (s *Scope) Executable() string { return s.manager.Executable }

func New(directory, executable string) (*Manager, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	return &Manager{Directory: directory, Executable: executable, live: map[string]liveLaunch{}}, nil
}
func nonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && len(id) <= 256
}
func (m *Manager) Existing(session string) (*Scope, error) {
	if !safeID(session) {
		return nil, errors.New("invalid managed session")
	}
	dir := filepath.Join(m.Directory, session)
	var state scopeState
	if err := readJSON(filepath.Join(dir, "scope.json"), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Stat(dir); errors.Is(statErr, os.ErrNotExist) {
				return nil, nil
			}
			return nil, errors.New("managed execution scope record is missing")
		}
		return nil, err
	}
	if len(state.Generation) != 32 || state.Parents == nil || state.Launches == nil {
		return nil, errors.New("managed scope record is incomplete")
	}
	if _, err := hex.DecodeString(state.Generation); err != nil {
		return nil, errors.New("invalid managed generation")
	}
	return &Scope{manager: m, Session: session, Generation: state.Generation, Directory: dir}, nil
}
func (m *Manager) Begin(session string) (*Scope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !safeID(session) {
		return nil, errors.New("invalid managed session")
	}
	old, err := m.Existing(session)
	if err != nil {
		return nil, err
	}
	if old != nil {
		state, err := old.load()
		if err != nil {
			return nil, err
		}
		if state.Active {
			return nil, errors.New("previous managed execution is not fenced")
		}
		if err = old.verifyStopped(); err != nil {
			return nil, err
		}
		for id, launch := range m.live {
			if launch.scope.Session == session && launch.scope.Generation == old.Generation {
				delete(m.live, id)
			}
		}
	}
	dir := filepath.Join(m.Directory, session)
	generation := nonce()
	scope := &Scope{manager: m, Session: session, Generation: generation, Directory: dir}
	if err := os.MkdirAll(filepath.Join(dir, generation, "launches"), 0700); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(dir, "scope.json"), scopeState{Generation: generation, Active: true, Parents: map[string]Identity{}, Launches: map[string]bool{}}); err != nil {
		return nil, err
	}
	return scope, nil
}
func (s *Scope) load() (scopeState, error) {
	var state scopeState
	err := readJSON(filepath.Join(s.Directory, "scope.json"), &state)
	if err == nil && state.Generation != s.Generation {
		err = errors.New("managed generation changed")
	}
	return state, err
}
func (s *Scope) launchPath(id string) string {
	return filepath.Join(s.Directory, s.Generation, "launches", id+".json")
}
func (s *Scope) Bind(kind string, pid int) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if !state.Active {
		return errors.New("managed generation is fenced")
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return err
	}
	if previous, ok := state.Parents[kind]; ok && previous != identity(*p) {
		return errors.New("managed owner replacement requires a new generation")
	}
	state.Parents[kind] = identity(*p)
	return writeJSON(filepath.Join(s.Directory, "scope.json"), state)
}
func (s *Scope) Prepare(kind, ref string) (Ticket, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return Ticket{}, err
	}
	parent, ok := state.Parents[kind]
	if !state.Active || !ok {
		return Ticket{}, errors.New("managed launch owner is unavailable")
	}
	t := Ticket{ID: nonce(), Kind: kind, Ref: ref, Parent: parent, State: "prepared"}
	if err = writeJSON(s.launchPath(t.ID), t); err != nil {
		return Ticket{}, err
	}
	state.Launches[t.ID] = false
	return t, writeJSON(filepath.Join(s.Directory, "scope.json"), state)
}
func (s *Scope) Register(packet Registration) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if !state.Active {
		return errors.New("managed generation is fenced")
	}
	if !safeID(packet.ID) {
		return errors.New("invalid launch registration")
	}
	var t Ticket
	if err = readJSON(s.launchPath(packet.ID), &t); err != nil {
		return err
	}
	root, err := unix.SysctlKinfoProc("kern.proc.pid", packet.Root)
	if err != nil {
		return err
	}
	anchor, err := unix.SysctlKinfoProc("kern.proc.pid", packet.Anchor)
	if err != nil {
		return err
	}
	if packet.Root <= 1 || packet.Root == os.Getpid() || int(root.Eproc.Pgid) != packet.Root || int(root.Eproc.Pgid) == unix.Getpgrp() || int(anchor.Eproc.Pgid) != packet.Root || int(anchor.Eproc.Ppid) != packet.Root || root.Proc.P_stat == 5 || anchor.Proc.P_stat == 5 || root.Eproc.Ucred.Uid != uint32(os.Getuid()) || anchor.Eproc.Ucred.Uid != uint32(os.Getuid()) {
		return errors.New("launch registration does not own a dedicated group")
	}
	ancestor := int(root.Eproc.Ppid)
	owned := false
	for i := 0; i < 64 && ancestor > 1; i++ {
		p, e := unix.SysctlKinfoProc("kern.proc.pid", ancestor)
		if e != nil {
			break
		}
		if identity(*p) == t.Parent {
			owned = true
			break
		}
		next := int(p.Eproc.Ppid)
		if next == ancestor {
			break
		}
		ancestor = next
	}
	if !owned {
		return errors.New("launch does not descend from its recorded session owner")
	}
	if t.State == "registered" {
		if t.Root == identity(*root) && t.Anchor == identity(*anchor) {
			state.Launches[t.ID] = true
			return writeJSON(filepath.Join(s.Directory, "scope.json"), state)
		}
		return errors.New("launch identity changed")
	}
	if t.State != "prepared" {
		return errors.New("launch was already retired")
	}
	t.Root, t.Anchor, t.Group, t.State = identity(*root), identity(*anchor), packet.Root, "registered"
	if err = writeJSON(s.launchPath(t.ID), t); err != nil {
		return err
	}
	state.Launches[t.ID] = true
	if err = writeJSON(filepath.Join(s.Directory, "scope.json"), state); err != nil {
		return err
	}
	s.manager.live[t.ID] = liveLaunch{s, t}
	return nil
}

// Reap retires only newly registered groups whose only executable member is the
// trusted anchor. Historical receipts are never reinterpreted as live PIDs.
func (m *Manager) Reap(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.live) == 0 {
		return nil
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return err
	}
	for id, launch := range m.live {
		s, t := launch.scope, launch.ticket
		state, err := s.load()
		if err != nil {
			return &OwnershipError{Session: s.Session, Generation: s.Generation, Cause: err}
		}
		if !state.Active {
			delete(m.live, id)
			continue
		}
		if err := knownMemberStayedInGroup(t); err != nil {
			return &OwnershipError{Session: s.Session, Generation: s.Generation, Cause: err}
		}
		var members []unix.KinfoProc
		for _, p := range procs {
			if int(p.Eproc.Pgid) == t.Group && p.Proc.P_stat != 5 {
				members = append(members, p)
			}
		}
		if len(members) > 0 && !anchorPresent(t, members) {
			return &OwnershipError{Session: s.Session, Generation: s.Generation, Cause: errors.New("managed group anchor identity is no longer verifiable")}
		}
		if len(members) > 1 || len(members) == 1 && !anchorPresent(t, members) {
			continue
		}
		// Refresh immediately before retiring an identity; a global snapshot may
		// precede another group's cleanup or an external process exit.
		members, err = groupMembers(t.Group)
		if err != nil {
			return err
		}
		if err = knownMemberStayedInGroup(t); err != nil {
			return &OwnershipError{Session: s.Session, Generation: s.Generation, Cause: err}
		}
		if len(members) > 1 || len(members) == 1 && !anchorPresent(t, members) {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if len(members) == 1 {
			anchor, e := unix.SysctlKinfoProc("kern.proc.pid", t.Anchor.PID)
			if e != nil || identity(*anchor) != t.Anchor || int(anchor.Eproc.Pgid) != t.Group || anchor.Proc.P_stat == 5 {
				return &OwnershipError{Session: s.Session, Generation: s.Generation, Cause: errors.New("managed anchor changed before retirement")}
			}
			if err = unix.Kill(t.Anchor.PID, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
			// The identity still occupied its group immediately before this signal;
			// wait for its exit before recording the end of this lifetime.
			for {
				p, e := unix.SysctlKinfoProc("kern.proc.pid", t.Anchor.PID)
				if errors.Is(e, unix.ESRCH) || errors.Is(e, unix.EIO) && errors.Is(unix.Kill(t.Anchor.PID, 0), unix.ESRCH) || e == nil && (identity(*p) != t.Anchor || p.Proc.P_stat == 5) {
					break
				}
				if e != nil {
					return e
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
			}
		}
		t.State = "stopped"
		if err = writeJSON(s.launchPath(t.ID), t); err != nil {
			return err
		}
		delete(m.live, id)
	}
	return nil
}
func (s *Scope) Fence() error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	state.Active = false
	return writeJSON(filepath.Join(s.Directory, "scope.json"), state)
}
func (s *Scope) tickets() ([]Ticket, error) {
	state, err := s.load()
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(s.Directory, s.Generation, "launches", "*.json"))
	if err != nil {
		return nil, err
	}
	var records []Ticket
	seen := map[string]bool{}
	for _, path := range paths {
		var t Ticket
		if err = readJSON(path, &t); err != nil {
			return nil, err
		}
		if t.State == "registered" || t.State == "stopped" {
			if t.State == "registered" {
				records = append(records, t)
			}
			seen[t.ID] = true
		}
	}
	for id, registered := range state.Launches {
		if registered && !seen[id] {
			return nil, errors.New("acknowledged launch ownership record is missing")
		}
	}
	return records, nil
}
func identity(p unix.KinfoProc) Identity {
	return Identity{PID: int(p.Proc.P_pid), Sec: p.Proc.P_starttime.Sec, Usec: p.Proc.P_starttime.Usec}
}
func groupMembers(group int) ([]unix.KinfoProc, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var members []unix.KinfoProc
	for _, p := range procs {
		if int(p.Eproc.Pgid) == group && p.Proc.P_stat != 5 {
			members = append(members, p)
		}
	}
	return members, nil
}
func anchorPresent(t Ticket, members []unix.KinfoProc) bool {
	for _, p := range members {
		if identity(p) == t.Anchor {
			return true
		}
	}
	return false
}
func (s *Scope) verifyStopped() error {
	records, err := s.tickets()
	if err != nil {
		return err
	}
	for _, t := range records {
		if t.Group <= 1 || t.Group == unix.Getpgrp() {
			return errors.New("refusing to signal controlling worker group")
		}
		if err := knownMemberStayedInGroup(t); err != nil {
			return err
		}
		members, e := groupMembers(t.Group)
		if e != nil {
			return e
		}
		if len(members) > 0 {
			return errors.New("managed process group is still present")
		}
	}
	return nil
}
func (s *Scope) VerifyStopped() error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Active {
		return errors.New("managed generation is not fenced")
	}
	return s.verifyStopped()
}
func (s *Scope) Stop(ctx context.Context) error {
	if err := s.Fence(); err != nil {
		return err
	}
	records, err := s.tickets()
	if err != nil {
		return err
	}
	for _, t := range records {
		if t.Group <= 1 || t.Group == unix.Getpgrp() {
			return errors.New("refusing to signal controlling worker group")
		}
		if err := knownMemberStayedInGroup(t); err != nil {
			return err
		}
		members, e := groupMembers(t.Group)
		if e != nil {
			return e
		}
		if len(members) == 0 {
			continue
		}
		if !anchorPresent(t, members) {
			return errors.New("managed group ownership is no longer verifiable")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = unix.Kill(-t.Group, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
			return err
		}
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for _, t := range records {
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			if err := knownMemberStayedInGroup(t); err != nil {
				return err
			}
			members, e := groupMembers(t.Group)
			if e != nil {
				return e
			}
			if len(members) == 0 {
				break
			}
			if !anchorPresent(t, members) {
				return errors.New("managed group ownership changed during stop")
			}
			if len(members) == 1 || time.Now().After(deadline) {
				if err = unix.Kill(-t.Group, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
					return err
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		t.State = "stopped"
		if err = writeJSON(s.launchPath(t.ID), t); err != nil {
			return err
		}
	}
	return s.VerifyStopped()
}

func knownMemberStayedInGroup(t Ticket) error {
	for _, member := range []Identity{t.Root, t.Anchor} {
		p, err := unix.SysctlKinfoProc("kern.proc.pid", member.PID)
		if err != nil {
			if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(unix.Kill(member.PID, 0), unix.ESRCH) {
				continue
			}
			return err
		}
		if identity(*p) == member && p.Proc.P_stat != 5 && int(p.Eproc.Pgid) != t.Group {
			return errors.New("registered process moved outside its managed group")
		}
	}
	return nil
}

func (s *Scope) Admitting() bool {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	return err == nil && state.Active
}
func (s *Scope) Cover(operation string) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if !state.Active || operation == "" {
		return errors.New("managed operation is not admissible")
	}
	return writeJSON(s.operationPath(operation), map[string]string{"generation": s.Generation})
}
func (s *Scope) operationPath(operation string) string {
	hash := sha256.Sum256([]byte(operation))
	return filepath.Join(s.Directory, s.Generation, "operations", hex.EncodeToString(hash[:])+".json")
}
func (s *Scope) Covered(operation string) bool {
	if operation == "" {
		return false
	}
	var receipt struct{ Generation string }
	return readJSON(s.operationPath(operation), &receipt) == nil && receipt.Generation == s.Generation
}
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".owned-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func readJSON(path string, value any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, value); err != nil {
		return fmt.Errorf("unreadable managed execution record: %w", err)
	}
	return nil
}
