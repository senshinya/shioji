package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os/exec"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
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
	phase            gitLoadPhase
	tag              string
	previous         *domain.GitStatus
	problem, details string
	recheckCheckout  bool
	showProblem      bool
}

type branchCheckout struct {
	connection, epoch      string
	branch, previousBranch string
	confirming             bool
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
	if v.Controller == nil || !v.Model.Connected || v.currentConnectionPhase() != app.ConnectionIdle {
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
	if v.Model.Details[q.SessionID].Session.ContextChanged {
		tag += ":review"
	}
	if force {
		state.showProblem = true
	}
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
		state.tag = tag
		var status domain.GitStatus
		if err == nil {
			err = json.Unmarshal(b, &status)
		}
		if err != nil {
			state.problem, state.details = "无法读取分支", gitFeedbackDetail(err)
			if state.recheckCheckout {
				state.problem = "分支切换结果待确认"
			}
			return
		}
		state.phase, state.previous = gitReady, nil
		state.recheckCheckout, state.showProblem = false, false
		m.SetGitStatus(key, status)
	}})
	if !accepted {
		state.finish()
	} else {
		state.problem, state.details = "", ""
	}
}
func (v *View) gitActionReady(key string) bool {
	m := v.Model
	return m.Connected && m.GitEpochs[key] == m.ConnectionEpoch && !v.gitLoading(key) && v.activeBranchCheckout(key) == nil
}

func (v *View) canSwitchBranch() bool {
	m := v.Model
	key, _ := v.gitQuery()
	if !v.gitActionReady(key) {
		return false
	}
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID && (s.Status.BlocksCheckout() || s.Goal.Active()) {
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
	return true
}

func (v *View) canConfirmBranch(key string) bool {
	m := v.Model
	session := m.Details[m.SessionID].Session
	if session.ID == "" || session.Status == domain.ExecutionUnknown || session.ExecutionGuard != "" || m.ArchivedContext() != "" ||
		!v.gitActionReady(key) || !m.GitStatuses[key].Applicable || m.WorkerMinor < protocol.GitConfirmationMinor {
		return false
	}
	return m.WorkerMinor >= protocol.ActiveGitConfirmationMinor || v.canSwitchBranch()
}

func (v *View) activeBranchCheckout(key string) *branchCheckout {
	if p := v.splitRoot().branchCheckouts[key]; p != nil && p.connection == v.Model.ConnectionID() && p.epoch == v.Model.ConnectionEpoch {
		return p
	}
	return nil
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
	v.submitBranchAction(branch, nil)
}

func (v *View) confirmBranch(status domain.GitStatus) {
	key, _ := v.gitQuery()
	if !v.canConfirmBranch(key) || v.Controller == nil {
		return
	}
	v.branchOpen = false
	v.submitBranchAction(status.Branch, &status.Head)
}

func (v *View) submitBranchAction(branch string, observedHead *string) {
	m := v.Model
	key, q := v.gitQuery()
	root := v.splitRoot()
	pending := &branchCheckout{connection: m.ConnectionID(), epoch: m.ConnectionEpoch, branch: branch, previousBranch: m.GitStatuses[key].Branch, confirming: observedHead != nil}
	checkout := protocol.CheckoutInput{GitQuery: q, Branch: branch}
	method, input := "git.checkout", any(checkout)
	if observedHead != nil {
		method, input = "git.confirm", protocol.ConfirmCheckoutInput{CheckoutInput: checkout, Head: *observedHead}
	}
	if root.branchCheckouts == nil {
		root.branchCheckouts = map[string]*branchCheckout{}
	}
	root.branchCheckouts[key] = pending
	if !v.do(app.Job{Method: method, Input: input, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if root.branchCheckouts[key] != pending {
			return
		}
		delete(root.branchCheckouts, key)
		if v.deviceState(pending.connection).ConnectionEpoch != pending.epoch {
			return
		}
		current := m.GitStatuses[key].Branch
		if current != pending.previousBranch && current == pending.branch {
			return // A newer, independently delivered checkout observation owns the row.
		}
		var status domain.GitStatus
		if err == nil {
			err = json.Unmarshal(b, &status)
		}
		if err != nil {
			v.branchCheckoutError(key, pending, err)
			return
		}
		if state := v.gitLoads[key]; state != nil {
			state.problem, state.details = "", ""
		}
		if m.ConnectionID() == pending.connection {
			m.SetGitStatus(key, status)
		} else {
			m.ApplyDeviceUpdate(pending.connection, func(p *app.Model) { p.SetGitStatus(key, status) })
		}
	}}) {
		delete(root.branchCheckouts, key)
		v.branchCheckoutError(key, pending, &protocol.RemoteError{Code: "notSent", Message: "设备操作繁忙"})
	}
}

func gitFeedbackDetail(err error) string {
	if isFeedbackTimeout(err.Error()) {
		return "等待设备响应超时。"
	}
	if strings.Contains(err.Error(), exec.ErrWaitDelay.Error()) {
		return "Git 未返回完整结果"
	}
	return err.Error()
}

func (v *View) branchCheckoutError(key string, pending *branchCheckout, err error) {
	if v.gitLoads == nil {
		v.gitLoads = make(map[string]*gitLoadState)
	}
	state := v.gitLoads[key]
	if state == nil {
		state = &gitLoadState{}
		v.gitLoads[key] = state
	}
	var remote *protocol.RemoteError
	state.recheckCheckout = !pending.confirming && (!errors.As(err, &remote) || remote.Code == "gitOutputIncomplete" || strings.Contains(err.Error(), exec.ErrWaitDelay.Error()))
	state.problem, state.details, state.showProblem = "无法切换分支", gitFeedbackDetail(err), true
	if pending.confirming {
		state.problem = "无法确认分支"
	}
	if state.recheckCheckout {
		state.problem = "分支切换结果待确认"
		if current, _ := v.gitQuery(); current == key && v.Model.ConnectionID() == pending.connection {
			v.requestGit(true)
		}
	}
}

func (v *View) branchStrip(c *n.Context, t *n.Theme, home bool) {
	key, _ := v.gitQuery()
	status, known := v.composerGitStatus(key)
	load := v.gitLoads[key]
	showIssue := load != nil && load.problem != "" && (!known || load.showProblem || v.feedbackGitWait == key)
	rechecking := load != nil && (load.recheckCheckout || v.feedbackGitWait == key || !known && load.showProblem) && v.gitLoading(key)
	visible := known && status.Applicable || showIssue || rechecking
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
	if label == "" {
		label = "分离 HEAD"
		if len(status.Head) >= 7 {
			label += " · " + status.Head[:7]
		}
	}
	contextChanged := !home && v.Model.Details[v.Model.SessionID].Session.ContextChanged
	if contextChanged {
		label += " · 待确认"
	}
	frame.Children(func() {
		n.Row(c.Key("branch-strip")).Absolute().Top(0).Left(0).Right(0).FillWidth().Height(26).Gap(12).AlignItems(n.Center).Children(func() {
			if showIssue || rechecking {
				if rechecking {
					label := "正在读取分支"
					if load.recheckCheckout || v.feedbackGitWait == key {
						label = "正在核对当前分支"
					}
					n.Text(c, label).FontSize(11).TextColor(t.TextMuted)
				} else {
					n.Text(c, load.problem).FontSize(11).TextColor(t.TextMuted).Tooltip(feedbackDetails(load.details))
					if ghost(c, "重新读取分支").FontSize(11).Height(26).Disabled(!v.Model.Connected).Clicked() {
						v.requestGit(true)
					}
				}
				return
			}
			canSwitch := v.canSwitchBranch()
			canReview := contextChanged && v.Model.Connected
			trigger := control(c, 5).Height(26).MaxWidth(330).MinWidth(0).Shrink(1).Padding(3, 10).Radius(5).Label("选择分支").Tooltip(label).Disabled(!canSwitch && !canReview).Children(func() {
				inlineIconText(c, branchIcon, label, n.Font{Family: "Menlo", Size: 11, Weight: 400}, 12, 5, t.TextMuted).MinWidth(0).Shrink(1).Ellipsis("…")
			})
			if trigger.Clicked() {
				v.branchOpen = !v.branchOpen
				v.branchSearch = ""
				v.requestGit(true)
			}
			popover(c.Key("branches"), trigger, &v.branchOpen, func(panel n.Element) {
				cursor := n.Local(panel, "branch-keyboard-choice", func() string { return "" })
				compactMenuSurface(panel, t).Width(260).Gap(4).Children(func() {
					search := textField(c.Key("filter"), &v.branchSearch).FillWidth().Height(28).FontSize(12).Label("搜索分支").Placeholder("搜索分支…").AutoFocus()
					if search.Changed() {
						*cursor = ""
					}
					choices := []string{}
					for _, name := range status.Branches {
						if strings.Contains(strings.ToLower(name), strings.ToLower(v.branchSearch)) {
							choices = append(choices, name)
						}
					}
					canConfirm := v.canConfirmBranch(key)
					enabled := func(name string) bool {
						if !home && name == status.Branch {
							return contextChanged && canConfirm
						}
						return canSwitch
					}
					choose := func(name string) {
						if !enabled(name) {
							return
						}
						if !home && name == status.Branch {
							v.confirmBranch(status)
						} else {
							v.chooseBranch(name)
						}
					}
					search.OnTextKey(func(key n.TextKey) bool {
						if key.Mods != 0 {
							return false
						}
						options := slices.DeleteFunc(slices.Clone(choices), func(name string) bool { return !enabled(name) })
						if len(options) == 0 {
							return false
						}
						index := slices.Index(options, *cursor)
						switch key.Key {
						case n.KeyDown:
							index = min(index+1, len(options)-1)
						case n.KeyUp:
							if index < 0 {
								index = len(options)
							}
							index = max(0, index-1)
						case n.KeyHome:
							index = 0
						case n.KeyEnd:
							index = len(options) - 1
						case n.KeyEnter:
							choose(options[max(0, index)])
							return true
						default:
							return false
						}
						*cursor = options[index]
						v.branchList.ScrollIntoView(slices.Index(choices, *cursor))
						return true
					})
					if !home && contextChanged {
						confirm := ghost(c, "确认当前分支").FillWidth().FontSize(12).Disabled(!canConfirm)
						if v.Model.WorkerMinor < protocol.GitConfirmationMinor || contextChanged && !canSwitch && v.Model.WorkerMinor < protocol.ActiveGitConfirmationMinor {
							confirm.Tooltip("更新执行服务后可确认当前分支")
						}
						if confirm.Clicked() {
							v.confirmBranch(status)
						}
					}
					if len(choices) == 0 {
						n.Text(c, "没有匹配的分支").FontSize(12).TextColor(t.TextMuted).Padding(8)
					}
					v.branchList.Key = func(i int) any { return choices[i] }
					n.List(c, &v.branchList, len(choices), func(i int) {
						name := choices[i]
						row := control(c, compactMenuRowRadius).FillWidth().MinHeight(compactMenuRowHeight).
							Padding(3, compactMenuRowInset).Radius(compactMenuRowRadius).Label("分支:" + name).
							Role(n.RoleMenuItemRadio).AccessSelected(name == branch).Disabled(!enabled(name))
						if search.Focused() && name == *cursor {
							row.Background(t.Selection)
						}
						row.Children(func() {
							n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
								n.Text(c, name).Grow(1).FontSize(compactMenuFontSize).FixedLineHeight(20)
								if name == branch {
									inlineSymbol(c, check, name, n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 14, t.Text)
								}
							})
						})
						if row.Clicked() {
							choose(name)
						}
					}).FillWidth().Height(min(240, float32(len(choices))*28))
				})
			})
			if pending := v.activeBranchCheckout(key); pending != nil && (pending.previousBranch == pending.branch || branch != pending.branch) {
				progress := "正在切换分支"
				if pending.confirming {
					progress = "正在确认分支"
				}
				n.Text(c.Key("branch-checkout"), progress).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).
					SingleLine().Tooltip(pending.branch)
			}
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
