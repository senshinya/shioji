package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
)

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
	if v.gitLoaded == nil {
		v.gitLoaded = map[string]string{}
		v.gitPending = map[string]bool{}
	}
	revision := int64(0)
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == q.WorkspaceID {
			revision = w.Revision
		}
	}
	tag := fmt.Sprintf("%s:%d", v.Model.ConnectionEpoch, revision)
	if v.gitPending[key] || (!force && v.gitLoaded[key] == tag) {
		return
	}
	v.gitPending[key] = true
	epoch := v.Model.ConnectionEpoch
	accepted := v.do(app.Job{Method: "git.status", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		delete(v.gitPending, key)
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
		v.gitLoaded[key] = tag
		m.SetGitStatus(key, status)
	}})
	if !accepted {
		delete(v.gitPending, key)
	}
}
func (v *View) canSwitchBranch() bool {
	m := v.Model
	key, _ := v.gitQuery()
	if !m.Connected || m.GitEpochs[key] != m.ConnectionEpoch || v.gitPending[key] {
		return false
	}
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID && (s.Status == "running" || s.Status == "starting" || s.Status == "unknown") {
			return false
		}
	}
	for _, r := range m.Details[m.SessionID].Requests {
		if r.State == "pending" || r.State == "submitting" {
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
	status, known := v.Model.GitStatuses[key]
	if !known || !status.Applicable {
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
	n.Row(c.Key("branch-strip")).FillWidth().Height(26).Gap(10).AlignItems(n.Center).Children(func() {
		trigger := n.ButtonBase(c).Height(26).MaxWidth(330).PaddingX(2).Radius(5).Label("选择分支").Tooltip(label).Disabled(!v.canSwitchBranch()).Children(func() {
			n.Row(c).Gap(6).AlignItems(n.Center).Children(func() {
				n.Icon(c, branchIcon).Size(12, 12).TextColor(t.TextMuted)
				n.Text(c, label).SingleLine().Font("Menlo").FontSize(11).TextColor(t.TextMuted)
				n.Icon(c, chevron).Size(10, 10).TextColor(t.TextMuted)
			})
		})
		if trigger.Clicked() {
			v.branchOpen = !v.branchOpen
			v.branchSearch = ""
			v.requestGit(true)
		}
		n.PopoverBase(c.Key("branches"), trigger, &v.branchOpen, func(panel n.Element) {
			panel.Width(260).Padding(4).Radius(8).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
				n.TextInput(c.Key("filter"), &v.branchSearch).FillWidth().Height(28).FontSize(12).Label("搜索分支").Placeholder("搜索分支…").Changed()
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
					row := n.ButtonBase(c).FillWidth().MinHeight(26).Padding(3, 7).Radius(4).Label("分支:" + name).Children(func() {
						n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
							n.Text(c, name).Grow(1).FontSize(13)
							if name == branch {
								n.Icon(c, check).Size(14, 14)
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
			if n.Checkbox(c, &isolate, "新建 worktree").FontSize(11).Disabled(!v.Model.Connected || status.Head == "").Changed() {
				choice.NewWorktree = isolate
				v.Model.GitDrafts[v.Model.WorkspaceID] = choice
			}
		}
	})
}
