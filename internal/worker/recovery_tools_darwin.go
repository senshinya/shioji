package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"shioji/internal/domain"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Capture worker-owned descendant groups while the app-server is alive. The
// command/session association is only a candidate: stopping requires the user
// to explicitly select the actual argv/PID. It never confirms a turn ended.
type recoveryToolProcess struct {
	Session, Item, Command string
	Turn                   string
	Group                  int
	Root                   processRecord
	Overflow               bool
	SeenAt                 time.Time
	Members                []processRecord
}

func toolRecordPath(dir, session, item string) string {
	return filepath.Join(dir, "recovery-tools", fmt.Sprintf("%x.json", sha256.Sum256([]byte(session+"\x00"+item))))
}
func processIdentity(p unix.KinfoProc) processRecord {
	return processRecord{PID: int(p.Proc.P_pid), StartedSec: p.Proc.P_starttime.Sec, StartedUsec: p.Proc.P_starttime.Usec}
}
func sameProcess(p unix.KinfoProc, r processRecord) bool {
	return int(p.Proc.P_pid) == r.PID && p.Proc.P_starttime.Sec == r.StartedSec && p.Proc.P_starttime.Usec == r.StartedUsec
}
func processArgs(pid int) ([]string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return nil, errors.New("无法读取进程命令")
	}
	argc := int(binary.LittleEndian.Uint32(b[:4]))
	b = b[4:]
	i := bytes.IndexByte(b, 0) // executable path precedes argv
	if i < 0 {
		return nil, errors.New("进程参数不完整")
	}
	b = bytes.TrimLeft(b[i:], "\x00")
	args := bytes.Split(b, []byte{0})
	if argc < 1 || argc > len(args) {
		return nil, errors.New("进程参数不完整")
	}
	actual := make([]string, argc)
	for i := range actual {
		actual[i] = string(args[i])
	}
	return actual, nil
}
func commandArg(pid int, command string) bool {
	actual, err := processArgs(pid)
	if err != nil {
		return false
	}
	// The source formats the entire argv with shell quoting. Decode quoting
	// only; never execute it or expand variables, globs or substitutions.
	expected, ok := renderedCommandArgs(command)
	if !ok {
		return false
	}
	if slices.Equal(actual, expected) {
		return true
	}
	// A shell can exec its final simple command, retaining the PID and group.
	// Recognize only a literal final argv, never expanded shell expressions.
	if len(expected) != 3 || (expected[1] != "-c" && expected[1] != "-lc") {
		return false
	}
	start := 0
	var quote byte
	for i := 0; i < len(expected[2]); i++ {
		ch := expected[2][i]
		if ch == '\\' && quote != '\'' {
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			if quote == 0 {
				quote = ch
			} else if quote == ch {
				quote = 0
			}
			continue
		}
		if quote == 0 && ch == ';' {
			start = i + 1
		}
	}
	last := strings.TrimSpace(expected[2][start:])
	if strings.ContainsAny(last, "|&()<>\n$") {
		return false
	}
	tail, ok := renderedCommandArgs(last)
	if !ok || len(actual) != len(tail) {
		return false
	}
	return filepath.Base(actual[0]) == filepath.Base(tail[0]) && slices.Equal(actual[1:], tail[1:])
}

func renderedCommandArgs(command string) ([]string, bool) {
	var args []string
	var word []byte
	var quote byte
	started := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote == 0 && (ch == ' ' || ch == '\t' || ch == '\n') {
			if started {
				args = append(args, string(word))
				word = nil
				started = false
			}
			continue
		}
		started = true
		if ch == '\\' && quote != '\'' {
			if i+1 >= len(command) {
				return nil, false
			}
			next := command[i+1]
			if quote == '"' && next != '"' && next != '\\' && next != '$' && next != '`' && next != '\n' {
				word = append(word, ch)
				continue
			}
			i++
			if next != '\n' {
				word = append(word, next)
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			if quote == 0 {
				quote = ch
				continue
			}
			if quote == ch {
				quote = 0
				continue
			}
		}
		word = append(word, ch)
	}
	if quote != 0 {
		return nil, false
	}
	if started {
		args = append(args, string(word))
	}
	return args, len(args) > 0
}

func ownedToolMembers(r recoveryToolProcess, procs []unix.KinfoProc) ([]processRecord, bool) {
	owned := false
	for _, p := range procs {
		if int(p.Eproc.Pgid) != r.Group || p.Proc.P_stat == 5 { // zombies cannot execute
			continue
		}
		for _, member := range r.Members {
			if sameProcess(p, member) {
				owned = true
			}
		}
	}
	if !owned {
		return nil, false
	}
	var members []processRecord
	members = append(members, r.Members...)
	for _, p := range procs {
		if int(p.Eproc.Pgid) == r.Group && p.Proc.P_stat != 5 {
			member := processIdentity(p)
			if !slices.Contains(members, member) {
				members = append(members, member)
			}
		}
	}
	return members, true
}

func saveToolRecord(dir string, r recoveryToolProcess) error {
	path := toolRecordPath(dir, r.Session, r.Item)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tool-*")
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
	return os.Rename(f.Name(), path)
}

func (s *Server) trackRecoveryTool(e domain.Event) {
	if e.Item == nil || e.Item.Presentation != "command" {
		return
	}
	key := toolRecordPath(s.config.Directory, e.SessionID, e.Item.ID)
	s.toolMu.Lock()
	defer s.toolMu.Unlock()
	if e.Item.Status == "completed" || e.Item.Status == "failed" {
		delete(s.tools, key)
		os.Remove(key)
		return
	}
	if e.Item.Name != "" {
		if _, ok := s.tools[key]; !ok {
			s.tools[key] = recoveryToolProcess{Session: e.SessionID, Item: e.Item.ID, Turn: e.Item.TurnID, Command: e.Item.Name, SeenAt: time.Now()}
		}
	}
}

func (s *Server) watchRecoveryTools() {
	defer s.wg.Done()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		s.toolMu.Lock()
		if len(s.tools) == 0 {
			s.toolMu.Unlock()
			continue
		}
		procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
		needCapture := false
		for key, r := range s.tools {
			if r.Group == 0 {
				if time.Since(r.SeenAt) > 2*time.Second {
					delete(s.tools, key)
				} else {
					needCapture = true
				}
			}
		}
		var activeSessions []domain.Session
		var snapshotErr error
		if needCapture {
			activeSessions, snapshotErr = s.store.ActiveSessions()
		}
		active := ""
		for _, session := range activeSessions {
			if session.Status.Active() {
				if active != "" {
					active = "ambiguous"
					break
				}
				active = session.ID
			}
		}
		byPID := map[int]unix.KinfoProc{}
		for _, p := range procs {
			byPID[int(p.Proc.P_pid)] = p
		}
		for key, r := range s.tools {
			if err != nil {
				break
			}
			old := r
			var owner processRecord
			b, readErr := os.ReadFile(filepath.Join(s.sessionRuntimeDirectory(r.Session), "runtime-process.json"))
			legacy := os.IsNotExist(readErr)
			if os.IsNotExist(readErr) {
				b, readErr = os.ReadFile(filepath.Join(s.config.Directory, "runtime-process.json"))
			}
			json.Unmarshal(b, &owner)
			matchingOwner := owner.Session == r.Session || legacy && owner.Session == "" && active == r.Session
			if r.Group == 0 && snapshotErr == nil && matchingOwner && readErr == nil && sameProcess(byPID[owner.PID], owner) && owner.PID > 0 {
				var matches []unix.KinfoProc
				for _, p := range procs {
					pid := int(p.Proc.P_pid)
					if pid != int(p.Eproc.Pgid) || pid == owner.PID {
						continue
					}
					born := time.Unix(p.Proc.P_starttime.Sec, int64(p.Proc.P_starttime.Usec)*1000)
					if born.Before(r.SeenAt.Add(-time.Second)) || born.After(r.SeenAt.Add(time.Second)) {
						continue
					}
					ancestor := int(p.Eproc.Ppid)
					for steps := 0; ancestor > 1 && ancestor != owner.PID && steps < 64; steps++ {
						next, ok := byPID[ancestor]
						if !ok || int(next.Eproc.Ppid) == ancestor {
							break
						}
						ancestor = int(next.Eproc.Ppid)
					}
					if ancestor == owner.PID && commandArg(pid, r.Command) {
						matches = append(matches, p)
					}
				}
				if len(matches) == 1 {
					candidate := int(matches[0].Proc.P_pid)
					claimed := false
					for otherKey, other := range s.tools {
						if otherKey != key && (other.Group == candidate || other.Session == r.Session && other.Command == r.Command) {
							claimed = true
						}
					}
					if !claimed {
						r.Group, r.Root, r.Members = candidate, processIdentity(matches[0]), []processRecord{processIdentity(matches[0])}
					}
				}
			}
			if members, ok := ownedToolMembers(r, procs); ok {
				if len(members) > 1024 {
					r.Overflow = true
				} else {
					r.Members = members
				}
			} else if r.Group > 0 {
				delete(s.tools, key)
				continue
			}
			if r.Group > 0 && !reflect.DeepEqual(old, r) {
				if saveToolRecord(s.config.Directory, r) == nil {
					s.tools[key] = r
				}
			}
		}
		s.toolMu.Unlock()
	}
}

func (s *Server) recoveryTasks(d domain.SessionDetail, h domain.RecoveryHistory) ([]domain.RecoveryTask, bool) {
	turns := map[string]bool{}
	recoveryTurns, err := s.store.RecoveryTurns(d.Session.ID)
	if err != nil {
		return nil, false
	}
	for _, turn := range recoveryTurns {
		if turn.Status == "unknown" || turn.Status == "running" {
			turns[turn.ID] = true
		}
	}
	items := h.Items
	if !h.Available {
		items = d.Items
	}
	var tasks []domain.RecoveryTask
	canStop := false
	seen := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(s.config.Directory, "recovery-tools", "*.json"))
	for _, path := range files {
		var r recoveryToolProcess
		b, e := os.ReadFile(path)
		if e != nil || json.Unmarshal(b, &r) != nil || r.Session != d.Session.ID || !turns[r.Turn] {
			continue
		}
		if _, e = readRecoveryTool(s.config.Directory, r.Session, r.Item, r.Command); e != nil {
			continue
		}
		state := recoveryToolState(r)
		pid := 0
		var args []string
		lastCommand := ""
		if d.Session.Recovery != nil {
			for _, previous := range d.Session.Recovery.Tasks {
				if previous.ID == toolSelectionID(r) {
					lastCommand = previous.ProcessCommand
				}
			}
		}
		if state == "running" {
			for _, member := range r.Members {
				p, e := unix.SysctlKinfoProc("kern.proc.pid", member.PID)
				if e != nil || !sameProcess(*p, member) || int(p.Eproc.Pgid) != r.Group || p.Proc.P_stat == 5 {
					continue
				}
				args, e = processArgs(member.PID)
				if e == nil && strings.TrimSpace(strings.Join(args, " ")) != "" {
					pid = member.PID
					break
				}
			}
			if pid == 0 {
				state = "unknown"
			}
		}
		if len(args) > 0 {
			lastCommand = strings.Join(args, " ")
			if len(lastCommand) > 16<<10 {
				lastCommand = strings.ToValidUTF8(lastCommand[:16<<10], "�") + "…"
			}
		}
		tasks = append(tasks, domain.RecoveryTask{ID: toolSelectionID(r), ItemID: r.Item, PID: pid, ProcessGroup: r.Group, ProcessCommand: lastCommand, Command: r.Command, State: state})
		seen[r.Item] = true
		canStop = canStop || state == "running"
	}
	for _, item := range items {
		if seen[item.ID] || !turns[item.TurnID] || item.Kind != "tool" || item.Status == "completed" || item.Status == "failed" {
			continue
		}
		state := "unknown"
		if item.Presentation == "command" {
			if r, err := readRecoveryTool(s.config.Directory, d.Session.ID, item.ID, item.Name); err == nil {
				state = recoveryToolState(r)
			}
		}
		label := item.Name
		if label == "" {
			label = "未完成的工具调用"
		}
		tasks = append(tasks, domain.RecoveryTask{ID: item.ID, Command: label, State: state})
	}
	return tasks, canStop && len(tasks) > 0
}

func toolSelectionID(r recoveryToolProcess) string {
	return fmt.Sprintf("%s@%d:%d:%d", r.Item, r.Root.PID, r.Root.StartedSec, r.Root.StartedUsec)
}

func readRecoveryTool(dir, session, item, command string) (recoveryToolProcess, error) {
	var r recoveryToolProcess
	b, err := os.ReadFile(toolRecordPath(dir, session, item))
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.Session != session || r.Item != item || r.Command != command || r.Group <= 1 || r.Root.PID != r.Group || len(r.Members) == 0 || r.Overflow {
		return r, errors.New("任务进程归属记录不完整")
	}
	return r, nil
}

func recoveryToolState(r recoveryToolProcess) string {
	if r.Overflow {
		return "unknown"
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return "unknown"
	}
	for _, p := range procs {
		if p.Proc.P_stat == 5 {
			continue
		}
		for _, member := range r.Members {
			if sameProcess(p, member) && int(p.Eproc.Pgid) != r.Group {
				return "unknown"
			}
		}
	}
	if _, ok := ownedToolMembers(r, procs); ok {
		return "running"
	}
	for _, p := range procs {
		if int(p.Eproc.Pgid) == r.Group && p.Proc.P_stat != 5 {
			return "unknown"
		}
	}
	return "stopped"
}

func stopRecoveryTool(ctx context.Context, r recoveryToolProcess) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.Group == unix.Getpgrp() || r.Group <= 1 {
		return errors.New("不能停止 Shioji 所在的进程组")
	}
	switch recoveryToolState(r) {
	case "stopped":
		return nil
	case "unknown":
		return errors.New("无法确认任务进程归属，未停止任何进程")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Kill(-r.Group, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if recoveryToolState(r) == "stopped" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("任务仍未停止，可稍后再次尝试")
		case <-ticker.C:
		}
	}
}
