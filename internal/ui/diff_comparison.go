package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"time"
)

var diffSwapIcon = svg("M3 6h14m-3-3 3 3-3 3M17 14H3m3-3-3 3 3 3")

func diffEndpointName(endpoint domain.DiffEndpoint) string {
	if endpoint.Kind == "worktree" {
		return "工作目录"
	}
	if endpoint.Kind == "empty" {
		return "空版本"
	}
	if endpoint.Name != "" {
		return endpoint.Name
	}
	return "HEAD"
}
func diffEndpointDescription(endpoint domain.DiffEndpoint) string {
	name := diffEndpointName(endpoint)
	if endpoint.Kind == "worktree" {
		return name + " · " + endpoint.Branch + "，基于 " + endpoint.Commit
	}
	if endpoint.Commit != "" {
		name += " @ " + endpoint.Commit
	}
	return name
}
func unpinDiffComparison(pair domain.DiffComparison) domain.DiffComparison {
	pair.Repository = ""
	for _, endpoint := range []*domain.DiffEndpoint{&pair.Base, &pair.Target} {
		endpoint.Commit = ""
		if endpoint.Kind == "empty" {
			endpoint.Kind = "head"
		}
	}
	return pair
}
func (s *changesState) installDiff(versions domain.FileVersions) {
	previous := s.Versions
	s.receiveFile(versions)
	if s.Diff == nil || s.Diff.Old == nil || previous.Path != versions.Path || previous.Comparison != versions.Comparison || versions.Unchanged || versions.Old == versions.New {
		s.Diff = &editor.DiffState{Old: editor.NewBuffer(versions.Old), New: editor.NewBuffer(versions.New), Layout: editor.DiffUnified, Wrap: true}
	} else {
		if previous.Old != versions.Old {
			s.Diff.Old.Set(versions.Old)
		}
		if previous.New != versions.New {
			s.Diff.New.Set(versions.New)
		}
	}
}
func (v *View) requestComparison(state *changesState, pair domain.DiffComparison) {
	if v.Controller == nil || !v.Model.Connected {
		return
	}
	state.ComparisonSequence++
	sequence, epoch := state.ComparisonSequence, v.Model.ConnectionEpoch
	state.FileSequence++
	state.PendingComparison = &pair
	state.beginLoad()
	_, q := v.gitQuery()
	q.Comparison = &pair
	q.ReviewPath = state.Path
	if !v.do(app.Job{Method: "git.compare", Input: q, Apply: func(m *app.Model, data json.RawMessage, err error) {
		if sequence != state.ComparisonSequence || epoch != m.ConnectionEpoch {
			return
		}
		state.PendingComparison = nil
		state.Updated = time.Now()
		state.endLoad(false)
		if err != nil {
			state.Error = err.Error()
			state.FailedComparison = &pair
			return
		}
		var result domain.ComparisonResult
		if err = json.Unmarshal(data, &result); err != nil {
			state.Error = err.Error()
			return
		}
		state.Error = ""
		state.FailedComparison = nil
		state.Comparison = result.Comparison
		state.Files = result.Files
		state.endLoad(true)
		state.PinnedSnapshot = false
		if result.Versions != nil {
			state.acceptComparedFile(*result.Versions)
		}
	}}) {
		state.PendingComparison = nil
		state.endLoad(false)
	}
}
func (v *View) requestDiffRefs(state *changesState) {
	if v.Controller == nil || !v.Model.Connected || state.refsLoading {
		return
	}
	epoch := v.Model.ConnectionEpoch
	state.refsLoading = true
	_, q := v.gitQuery()
	if !v.do(app.Job{Method: "git.refs", Input: q, Apply: func(m *app.Model, data json.RawMessage, err error) {
		state.refsLoading = false
		if epoch != m.ConnectionEpoch {
			return
		}
		if err != nil {
			state.Error = err.Error()
			return
		}
		var catalog domain.DiffRefCatalog
		if err = json.Unmarshal(data, &catalog); err != nil {
			state.Error = err.Error()
			return
		}
		state.Refs = catalog.Refs
		state.refsEpoch = epoch
	}}) {
		state.refsLoading = false
	}
}
func (v *View) chooseDiffEndpoint(state *changesState, base bool, endpoint domain.DiffEndpoint) {
	pair := state.Comparison
	if state.PendingComparison != nil {
		pair = *state.PendingComparison
	}
	if base {
		pair.Base = endpoint
	} else {
		pair.Target = endpoint
	}
	state.BaseOpen, state.TargetOpen = false, false
	v.requestComparison(state, unpinDiffComparison(pair))
}
func (v *View) diffComparisonBar(c *n.Context, t *n.Theme, state *changesState, width float32) {
	if state.Comparison.Base.Kind == "" {
		state.Comparison = domain.DefaultDiffComparison()
	}
	row := n.Row(c.Key("diff-comparison")).FillWidth().Height(38).Shrink(0).PaddingX(12).Gap(4).AlignItems(n.Center)
	slot := max(float32(40), (width-24-28-16-12)/2)
	row.Children(func() {
		n.Column(c).Absolute().Bottom(0).Left(0).Right(0).Height(1).Background(t.Border).PassThrough()
		for i, endpoint := range []domain.DiffEndpoint{state.Comparison.Base, state.Comparison.Target} {
			base := i == 0
			label := "选择比较目标"
			open := &state.TargetOpen
			if base {
				label = "选择比较基准"
				open = &state.BaseOpen
			}
			button := control(c.Key(label), 5).Width(slot).Height(28).Radius(5).Label(label).Tooltip(diffEndpointDescription(endpoint)).Children(func() {
				n.Icon(c, disclosureDown).Size(10, 10).TextColor(t.TextMuted).Absolute().Right(6).Top(9)
			})
			if button.Clicked() {
				*open = !*open
				state.RefSearch = ""
				if base {
					state.TargetOpen = false
				} else {
					state.BaseOpen = false
				}
				v.requestDiffRefs(state)
			}
			v.diffEndpointMenu(c.Key(label+"menu"), t, button, state, base, open)
			if base {
				if state.loading() {
					n.Column(c.Key("compare-loading")).Width(16).Height(20).Children(func() { serviceSpinner(c, "", n.Font{Family: t.Font, Size: 12}, t.TextMuted) })
				} else if state.Error != "" {
					retry := control(c, 3).Size(16, 20).Label("重试比较").Tooltip(state.Error).Children(func() { n.Text(c, "!").FontSize(12).TextColor(t.Danger) })
					if retry.Clicked() {
						pair := state.Comparison
						if state.FailedComparison != nil {
							pair = *state.FailedComparison
						}
						v.requestComparison(state, pair)
					}
				} else {
					n.Text(c, "→").Width(16).FontSize(12).TextAlign(n.Center).TextColor(t.TextMuted)
				}
			}
		}
		if iconButton(c, diffSwapIcon, "交换比较方向").Size(28, 28).Clicked() {
			pair := state.Comparison
			if state.PendingComparison != nil {
				pair = *state.PendingComparison
			}
			pair.Base, pair.Target = pair.Target, pair.Base
			v.requestComparison(state, pair)
		}
		// Both names share this parent. Stable endpoint identities survive moving
		// between slots, so rapid reversal continues from the current position.
		for i, endpoint := range []domain.DiffEndpoint{state.Comparison.Base, state.Comparison.Target} {
			identity := endpoint.Kind + ":" + endpoint.Name
			if state.Comparison.Base.Kind == state.Comparison.Target.Kind && state.Comparison.Base.Name == state.Comparison.Target.Name {
				identity += string(rune('0' + i))
			}
			label := n.Row(c.Key("endpoint-name:" + identity)).Absolute().Width(slot - 20).Height(20).PassThrough().Label("比较名称:" + diffEndpointName(endpoint))
			origin := n.Local(label, "origin", func() int { return i })
			position := label.AnimateTo("slot", float32(i), motionAnimation(c, 260*time.Millisecond))
			arc := float32(math.Sin(math.Pi*float64(position))) * 5
			if *origin == 0 {
				arc = -arc
			}
			label.Left(18 + position*(slot+20)).Top(9 + arc).Gap(6).AlignItems(n.Baseline).Children(func() {
				icon := branchIcon
				if endpoint.Kind == "worktree" {
					icon = workspaceIcon
				}
				font := n.Font{Family: t.Font, Size: 12, Weight: 400}
				inlineSymbol(c, icon, diffEndpointName(endpoint), font, 14, t.TextMuted)
				n.Text(c, diffEndpointName(endpoint)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(12).FixedLineHeight(20)
			})
		}
	})
}
func (v *View) diffEndpointMenu(c *n.Context, t *n.Theme, trigger n.Element, state *changesState, base bool, open *bool) {
	popover(c, trigger, open, func(panel n.Element) {
		panel.Width(280).Padding(3).Radius(7).Background(raised(t)).Border(1, t.Border).Shadow(0, 4, 16, 0, n.RGBA(0, 0, 0, .12)).Children(func() {
			choices := []domain.DiffEndpoint{}
			for _, ref := range state.Refs {
				if strings.Contains(strings.ToLower(diffEndpointName(ref)+" "+ref.Branch), strings.ToLower(state.RefSearch)) {
					choices = append(choices, ref)
				}
			}
			search := textField(c.Key("filter"), &state.RefSearch).FillWidth().Height(28).FontSize(12).Label("搜索比较分支").Placeholder("搜索分支…").AutoFocus()
			search.OnTextKey(func(key n.TextKey) bool {

				if key.Key == n.KeyDown {
					state.RefFocusFirst = true
					return true
				}
				if key.Key == n.KeyEscape {
					*open = false
					return true
				}
				if key.Key == n.KeyEnter && len(choices) > 0 {
					v.chooseDiffEndpoint(state, base, choices[0])
					return true
				}
				return false
			})
			search.Changed()
			if len(choices) == 0 {
				message := "没有匹配的分支"
				if state.refsLoading {
					message = "读取分支…"
				}
				n.Text(c, message).FontSize(12).TextColor(t.TextMuted).Padding(6, 7)
				return
			}
			type pickerRow struct {
				group string
				ref   domain.DiffEndpoint
			}
			rows := []pickerRow{}
			lastGroup := ""
			for _, ref := range choices {
				group := "当前仓库"
				if ref.Kind == "branch" {
					group = "本地分支"
				}
				if ref.Kind == "remote" {
					group = "远程分支"
				}
				if group != lastGroup {
					rows = append(rows, pickerRow{group: group})
					lastGroup = group
				}
				rows = append(rows, pickerRow{ref: ref})
			}
			state.RefList.Key = func(i int) any { row := rows[i]; return row.group + ":" + row.ref.Kind + ":" + row.ref.Name }
			n.List(c, &state.RefList, len(rows), func(i int) {
				row := rows[i]
				if row.group != "" {
					n.Text(c, row.group).Height(24).FillWidth().Padding(4, 7).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted)
					return
				}
				ref := row.ref
				button := control(c.Key(ref.Kind+":"+ref.Name), 4).FillWidth().Height(26).PaddingX(7).Label("比较版本:" + ref.Kind + ":" + ref.Name).Tooltip(diffEndpointDescription(ref))
				if state.RefFocusFirst {
					button.Focus()
					state.RefFocusFirst = false
				}
				button.Children(func() {
					n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
						icon := branchIcon
						if ref.Kind == "worktree" {
							icon = workspaceIcon
						}
						inlineSymbol(c, icon, diffEndpointName(ref), n.Font{Family: t.Font, Size: 13}, 14, t.TextMuted)
						n.Text(c, diffEndpointName(ref)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(13).FixedLineHeight(20)
						hash := ref.Commit
						if len(hash) > 7 {
							hash = hash[:7]
						}
						n.Text(c, hash).Font("Menlo").FontSize(10).TextColor(t.TextMuted)
						selected := state.Comparison.Target
						if base {
							selected = state.Comparison.Base
						}
						opacity := float32(0)
						if selected.Kind == ref.Kind && selected.Name == ref.Name {
							opacity = 1
						}
						inlineSymbol(c, check, diffEndpointName(ref), n.Font{Family: t.Font, Size: 13}, 14, t.TextMuted).Opacity(opacity)
					})
				})
				if button.Clicked() {
					v.chooseDiffEndpoint(state, base, ref)
				}
			}).FillWidth().FocusGroup(n.Vertical).Height(min(272, float32(len(rows))*26))

		})
	})
}

func diffEndpointShortDescription(endpoint domain.DiffEndpoint) string {
	if len(endpoint.Commit) > 7 {
		endpoint.Commit = endpoint.Commit[:7]
	}
	return diffEndpointDescription(endpoint)
}

func (s *changesState) acceptComparedFile(versions domain.FileVersions) {
	draft := s.CommentDrafts[diffCommentKey{versions.Comparison, versions.Path}]
	if draft != nil && (draft.Open || draft.Closing) && draft.Versions.Snapshot.ID != "" && draft.Snapshot.SHA256 != versions.Snapshot.SHA256 {
		versions = draft.Versions
		s.PinnedSnapshot = true
	}
	s.installDiff(versions)
}
