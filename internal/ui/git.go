package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

type gitLoadPhase uint8

const (
	gitUnrequested gitLoadPhase = iota
	gitRequesting
	gitReady
	gitRefreshing
)

type gitLoadState struct {
	phase    gitLoadPhase
	tag      string
	previous *domain.GitStatus
}

func (v *View) gitLoading(key string) bool {
	s := v.gitLoads[key]
	return s != nil && (s.phase == gitRequesting || s.phase == gitRefreshing)
}
func (s *gitLoadState) begin() {
	if s.phase == gitReady {
		s.phase = gitRefreshing
	} else {
		s.phase = gitRequesting
	}
}
func (s *gitLoadState) finish() {
	if s.phase == gitRefreshing {
		s.phase = gitReady
	} else {
		s.phase = gitUnrequested
	}
}

func (v *View) gitQuery() (string, protocol.GitQuery) {
	m := v.Model
	q := protocol.GitQuery{WorkspaceID: m.WorkspaceID, SessionID: m.SessionID}
	if m.SessionID != "" {
		for _, s := range m.Snapshot.Sessions {
			if s.ID == m.SessionID {
				q.WorkspaceID = s.WorkspaceID
			}
		}
		return "session:" + m.SessionID, q
	}
	return "workspace:" + m.WorkspaceID, q
}
func (v *View) requestGit(force bool) {
	if v.Controller == nil || !v.Model.Connected {
		return
	}
	key, q := v.gitQuery()
	if q.WorkspaceID == "" {
		return
	}
	if v.gitLoads == nil {
		v.gitLoads = map[string]*gitLoadState{}
	}
	state := v.gitLoads[key]
	if state == nil {
		state = &gitLoadState{}
		// Home already displayed this checkout. Carry its branch row through
		// the receipt -> session-status handoff without treating it as fresh
		// permission to switch branches (GitEpochs still comes from the worker).
		if sent := v.sent; sent != nil && sent.origin == "" && sent.session == q.SessionID && q.SessionID != "" {
			if previous, ok := v.Model.GitStatuses["workspace:"+sent.workspace]; ok && previous.Applicable {
				if branch := v.Model.GitDrafts[sent.workspace].Branch; branch != "" {
					previous.Branch = branch
				}
				state.previous = &previous
			}
		}
		v.gitLoads[key] = state
	}
	revision := int64(0)
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == q.WorkspaceID {
			revision = w.Revision
		}
	}
	tag := fmt.Sprintf("%s:%d", v.Model.ConnectionEpoch, revision)
	if v.gitLoading(key) || (!force && state.tag == tag) {
		return
	}
	state.begin()
	epoch := v.Model.ConnectionEpoch
	accepted := v.do(app.Job{Method: "git.status", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		state.finish()
		if m.ConnectionEpoch != epoch {
			return
		}
		if err != nil {
			m.Error = err.Error()
			return
		}
		var status domain.GitStatus
		if err = json.Unmarshal(b, &status); err != nil {
			m.Error = err.Error()
			return
		}
		state.phase, state.tag, state.previous = gitReady, tag, nil
		m.SetGitStatus(key, status)
	}})
	if !accepted {
		state.finish()
	}
}
func (v *View) canSwitchBranch() bool {
	m := v.Model
	key, _ := v.gitQuery()
	if !m.Connected || m.GitEpochs[key] != m.ConnectionEpoch || v.gitLoading(key) {
		return false
	}
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID && (s.Status.BlocksCheckout()) {
			return false
		}
	}
	for _, r := range m.Details[m.SessionID].Requests {
		if r.State.AwaitingResolution() {
			return false
		}
	}
	for _, in := range m.Pending {
		if in.SessionID == m.SessionID {
			return false
		}
	}
	return !v.switchingBranch
}
func (v *View) chooseBranch(branch string) {
	m := v.Model
	v.branchOpen = false
	if m.SessionID == "" {
		choice := m.GitDrafts[m.WorkspaceID]
		choice.Branch = branch
		m.GitDrafts[m.WorkspaceID] = choice
		return
	}
	if !v.canSwitchBranch() || v.Controller == nil {
		return
	}
	key, q := v.gitQuery()
	v.switchingBranch = true
	if !v.do(app.Job{Method: "git.checkout", Input: protocol.CheckoutInput{GitQuery: q, Branch: branch}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		v.switchingBranch = false
		if err != nil {
			m.Error = err.Error()
			return
		}
		var status domain.GitStatus
		if err = json.Unmarshal(b, &status); err != nil {
			m.Error = err.Error()
			return
		}
		m.SetGitStatus(key, status)
	}}) {
		v.switchingBranch = false
	}
}
func (v *View) branchStrip(c *n.Context, t *n.Theme, home bool) {
	key, _ := v.gitQuery()
	status, known := v.composerGitStatus(key)
	visible := known && status.Applicable
	frame := n.Column(c.Key("branch-presence")).FillWidth().Clip().Margin(0, 0, -4, 0)
	previous := n.Local(frame, "last-status", func() domain.GitStatus { return status })
	if visible {
		*previous = status
	} else {
		status = *previous
	}
	target := float32(0)
	if visible {
		target = 30
	} // The existing 26-DIP row plus its composer gap.
	height := frame.AnimateTo("height", target, motionAnimation(c, 180*time.Millisecond))
	frame.Height(height).Opacity(height / 30)
	n.Inert(frame, !visible)
	if height == 0 {
		return
	}

	branch := status.Branch
	if home {
		branch = v.Model.GitDrafts[v.Model.WorkspaceID].Branch
	}
	label := branch
	if !home {
		for _, session := range v.Model.Snapshot.Sessions {
			if session.ID == v.Model.SessionID && session.ContextChanged {
				label += " · 待确认"
			}
		}
	}
	if label == "" {
		label = "分离 HEAD"
		if len(status.Head) >= 7 {
			label += " · " + status.Head[:7]
		}
	}
	frame.Children(func() {
		n.Row(c.Key("branch-strip")).Absolute().Top(0).Left(0).Right(0).FillWidth().Height(26).Gap(12).AlignItems(n.Center).Children(func() {
			trigger := control(c, 5).Height(26).MaxWidth(330).Padding(3, 10).Radius(5).Label("选择分支").Tooltip(label).Disabled(!v.canSwitchBranch()).Children(func() {
				inlineIconText(c, branchIcon, label, n.Font{Family: "Menlo", Size: 11, Weight: 400}, 12, 5, t.TextMuted)
			})
			if trigger.Clicked() {
				v.branchOpen = !v.branchOpen
				v.branchSearch = ""
				v.requestGit(true)
			}
			popover(c.Key("branches"), trigger, &v.branchOpen, func(panel n.Element) {
				panel.Width(260).Padding(4).Radius(8).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
					textField(c.Key("filter"), &v.branchSearch).FillWidth().Height(28).FontSize(12).Label("搜索分支").Placeholder("搜索分支…").Changed()
					choices := []string{}
					for _, name := range status.Branches {
						if strings.Contains(strings.ToLower(name), strings.ToLower(v.branchSearch)) {
							choices = append(choices, name)
						}
					}
					if !home {
						if ghost(c, "确认当前分支").FillWidth().FontSize(12).Clicked() {
							v.chooseBranch(status.Branch)
						}
					}
					if len(choices) == 0 {
						n.Text(c, "没有匹配的分支").FontSize(12).TextColor(t.TextMuted).Padding(8)
					}
					v.branchList.Key = func(i int) any { return choices[i] }
					n.List(c, &v.branchList, len(choices), func(i int) {
						name := choices[i]
						row := control(c, 4).FillWidth().MinHeight(26).Padding(3, 7).Radius(4).Label("分支:" + name).Children(func() {
							n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
								n.Text(c, name).Grow(1).FontSize(13).FixedLineHeight(20)
								if name == branch {
									inlineSymbol(c, check, name, n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 14, t.Text)
								}
							})
						})
						if row.Clicked() {
							v.chooseBranch(name)
						}
					}).FillWidth().Height(min(240, float32(len(choices))*28))
				})
			})
			if home {
				choice := v.Model.GitDrafts[v.Model.WorkspaceID]
				isolate := choice.NewWorktree
				if controlFeedback(c, n.Checkbox(c, &isolate, "新建 worktree").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).AlignItems(n.Center).Disabled(!v.Model.Connected || status.Head == ""), 4).Changed() {
					choice.NewWorktree = isolate
					v.Model.GitDrafts[v.Model.WorkspaceID] = choice
				}
			}
		})
	})
}

// Last accepted metadata keeps geometry stable; only a fresh worker status
// populates GitEpochs and authorizes checkout or submission.
func (v *View) composerGitStatus(key string) (domain.GitStatus, bool) {
	m := v.Model
	if status, ok := m.GitStatuses[key]; ok {
		return status, true
	}
	if load := v.gitLoads[key]; load != nil && load.previous != nil {
		return *load.previous, true
	}
	if m.SessionID != "" {
		for _, s := range m.Snapshot.Sessions {
			if s.ID == m.SessionID {
				return domain.GitStatus{Applicable: s.Context.Repository != "" || s.GitBranch != "" || s.GitHead != "", Repository: s.Context.Repository, Branch: s.GitBranch, Head: s.GitHead}, true
			}
		}
	}
	if repo, ok := m.GitRepositories[m.WorkspaceID]; ok {
		return domain.GitStatus{Applicable: repo != "", Repository: repo, Branch: m.GitDrafts[m.WorkspaceID].Branch}, true
	}
	return domain.GitStatus{}, false
}
