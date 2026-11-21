package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

const (
	inspectorMinWidth             = 280
	inspectorMaxWidth             = 960
	inspectorMinConversationWidth = 360
)

type changesState struct {
	FailedComparison *domain.DiffComparison
	RefFocusFirst    bool

	CommentNotes []diffCommentNote
	CommentWidth float32

	Locating                bool
	LocateFragment          *editor.DiffTextFragment
	LocateSequence          uint64
	SuppressSelectionAction bool

	Comparison           domain.DiffComparison
	PendingComparison    *domain.DiffComparison
	ComparisonSequence   uint64
	comparisonEpoch      string
	FileSequence         uint64
	Refs                 []domain.DiffEndpoint
	refsLoading          bool
	refsEpoch            string
	BaseOpen, TargetOpen bool
	RefSearch            string
	RefList              n.ListState
	CommentDrafts        map[diffCommentKey]*diffCommentDraft
	PinnedSnapshot       bool

	Files         []domain.ChangedFile
	load          changesLoadPhase
	Error         string
	NonRepository bool
	Updated       time.Time
	Path          string
	file          fileDiffPhase
	Versions      domain.FileVersions
	Diff          *editor.DiffState
	List          n.ListState
	Tasks         n.ListState
	DockSeen      bool
	WorkGroups    []app.InspectorGroup
}

type changesLoadPhase uint8

const (
	changesUnloaded changesLoadPhase = iota
	changesLoading
	changesLoaded
	changesRefreshing
)

func (s *changesState) loading() bool { return s.load == changesLoading || s.load == changesRefreshing }
func (s *changesState) loaded() bool  { return s.load == changesLoaded || s.load == changesRefreshing }
func (s *changesState) beginLoad() {
	if s.loaded() {
		s.load = changesRefreshing
	} else {
		s.load = changesLoading
	}
}
func (s *changesState) endLoad(success bool) {
	if success || s.loaded() {
		s.load = changesLoaded
	} else {
		s.load = changesUnloaded
	}
	if success {
		s.NonRepository = false
	}
}

func (s *changesState) failChanges(err error) {
	s.Error = err.Error()
	var remote *protocol.RemoteError
	s.NonRepository = errors.As(err, &remote) && remote.Code == "notGitRepository"
	// Workers predating the structured error return this fixed diagnostic.
	s.NonRepository = s.NonRepository || s.Error == "not a Git repository"
	if s.NonRepository {
		s.Files, s.Refs = nil, nil
		s.BaseOpen, s.TargetOpen = false, false
		s.closeFile()
	}
}

type fileDiffPhase uint8

const (
	fileUnselected fileDiffPhase = iota
	fileAwaiting
	fileAvailable
	fileRefreshing
	fileFailed
	fileFailedWithCache
)

func (s *changesState) fileAvailable() bool {
	return s.file == fileAvailable || s.file == fileRefreshing || s.file == fileFailedWithCache
}
func (s *changesState) selectFile(path string) {
	s.Path = path
	s.file = fileAwaiting
	if s.Versions.Path == path {
		s.file = fileRefreshing
	}
}
func (s *changesState) failFile() {
	if s.Versions.Path == s.Path {
		s.file = fileFailedWithCache
	} else {
		s.file = fileFailed
	}
}
func (s *changesState) receiveFile(versions domain.FileVersions) {
	s.Versions = versions
	s.file = fileAwaiting
	if versions.Path == s.Path {
		s.file = fileAvailable
	}
}
func (s *changesState) closeFile() { s.Path, s.file = "", fileUnselected }

var refreshIcon = svg("M16 7a7 7 0 1 0 1 5M16 3v4h-4")
var changesIcon = svg("M4 3h8l4 4v10H4zM12 3v4h4M7 10h6M10 8v4M7 14.5h6")
var tasksIcon = svg("M3 4h1M8 4h9M3 10h1M8 10h9M3 16h1M8 16h9")

func (v *View) changesFor(id string) *changesState {
	if v.changes == nil {
		v.changes = map[string]*changesState{}
	}
	state := v.changes[id]
	if state == nil {
		state = &changesState{Comparison: domain.DefaultDiffComparison(), Diff: &editor.DiffState{}}
		v.changes[id] = state
	}
	return state
}
func (v *View) refreshChanges() {
	v.requestChanges(true)
	if v.Model.WorkerMinor < 33 {
		if state := v.changesFor(v.Model.SessionID); state.Path != "" {
			v.openChangedFile(state.Path)
		}
	}
}
func (v *View) requestChanges(force bool) {
	m := v.Model
	if v.Controller == nil || m.SessionID == "" {
		return
	}
	state := v.changesFor(m.SessionID)
	if state.PendingComparison != nil && (!m.Connected || state.comparisonEpoch != m.ConnectionEpoch) {
		state.ComparisonSequence++
		state.finishComparison()
	}
	if !m.Connected {
		return
	}
	if m.WorkerMinor >= 33 {
		draft := state.commentDraft()
		if state.loading() || state.PinnedSnapshot && !force || draft != nil && (draft.Open || draft.Closing) && !force || !force && time.Since(state.Updated) < 3*time.Second {
			return
		}
		next := state.Comparison
		if state.FailedComparison != nil && !state.NonRepository {
			if !force {
				return
			}
			next = *state.FailedComparison
		}
		if force {
			next = unpinDiffComparison(next)
		}
		v.requestComparison(state, next)
		return
	}
	if state.loading() || (!force && time.Since(state.Updated) < 3*time.Second) {
		return
	}
	state.beginLoad()
	_, q := v.gitQuery()
	if !v.do(app.Job{Method: "git.changes", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		state.endLoad(false)
		state.Updated = time.Now()
		if err != nil {
			state.failChanges(err)
			return
		}
		state.Error = ""
		if err = json.Unmarshal(b, &state.Files); err != nil {
			state.failChanges(err)
			return
		}
		state.endLoad(true)
	}}) {
		state.endLoad(false)
	}
}
func (v *View) openChangedFile(path string) {
	if v.Controller == nil || !v.Model.Connected {
		return
	}
	state := v.changesFor(v.Model.SessionID)
	state.selectFile(path)
	state.PinnedSnapshot = false
	if state.PendingComparison != nil && v.Model.WorkerMinor >= 33 {
		v.requestComparison(state, *state.PendingComparison)
		return
	}
	state.FileSequence++
	sequence, epoch := state.FileSequence, v.Model.ConnectionEpoch
	comparisonSequence := state.ComparisonSequence
	state.Error = ""
	_, q := v.gitQuery()
	if v.Model.WorkerMinor >= 33 {
		pair := state.Comparison
		q.Comparison = &pair
	}
	v.do(app.Job{Method: "git.file", Input: protocol.FileQuery{GitQuery: q, Path: path}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if state.Path != path || state.FileSequence != sequence || state.ComparisonSequence != comparisonSequence || m.ConnectionEpoch != epoch {
			return
		}
		if err != nil {
			state.Error = err.Error()
			state.failFile()
			return
		}
		var versions domain.FileVersions
		if err = json.Unmarshal(b, &versions); err != nil {
			state.Error = err.Error()
			state.failFile()
			return
		}
		state.acceptComparedFile(versions)
	}})
}
func (v *View) inspector(c *n.Context, t *n.Theme, drawer bool, width, limit float32) {
	id := v.Model.SessionID
	state := v.changesFor(id)
	detail := v.Model.Details[id]
	all := domain.SessionWorks(detail)
	works := visibleSessionWorks(detail, all)
	prior := state.DockSeen
	state.DockSeen = true
	if len(all) > 0 || len(v.Model.InspectorLayout.Groups) > 0 || state.WorkGroups != nil {
		v.workInspector(c, t, drawer, width, limit, state, works, prior)
		return
	}
	plan := v.Model.Details[id].Plan
	hasTasks := plan != nil && (len(plan.Steps) > 0 || plan.Narrative != "")
	v.requestChanges(false)
	c.After(3 * time.Second)
	_, height := c.Size()
	splitHeight := v.Model.InspectorSplitHeight
	if splitHeight == 0 {
		splitHeight = height / 2
	}
	splitLimit := max(float32(120), height-240)
	splitHeight = min(max(splitHeight, 120), splitLimit)
	newSplitHeight := min(max(height/2, 120), splitLimit)
	layout := v.Model.InspectorLayout
	first := layout.First
	if first == "" {
		first = "changes"
	}
	topKind := first
	if layout.Joined {
		topKind = layout.Selected
		if topKind == "" {
			topKind = first
		}
	}
	if !hasTasks {
		topKind = "changes"
	}
	split := hasTasks && !layout.Joined
	panel := n.Column(c.Key("changes-inspector:" + id)).Width(width).Shrink(0).FillHeight().Clip().Background(t.Background).Label("更改侧栏")
	if drawer {
		panel.Absolute().Right(0).Top(0).Bottom(0).Shadow(-4, 0, 14, 0, n.RGBA(0, 0, 0, .12))
		if !v.splitRoot().resizingInspector {
			panel.Transition(drawerMotion(c))
		}
	}
	var drop *app.InspectorLayout
	panel.Children(func() {
		tabs := []string{topKind}
		if hasTasks && layout.Joined {
			tabs = []string{first, otherInspectorView(first)}
		}
		v.inspectorPane(c, t, state, plan, "top", topKind, tabs, hasTasks, mainToolbarHeight, width, &drop)
		bottom := n.Column(c.Key("inspector-bottom")).FillWidth().Shrink(0).Clip()
		target := float32(0)
		if split {
			target = 1
		}
		animation := motionAnimation(c, 200*time.Millisecond)
		if c.Preferences().ReduceMotion {
			animation.Duration = 0
		}
		extent := bottom.AnimateTo("split-presence", target, animation)
		bottom.Height(splitHeight * extent)
		bottom.Children(func() {
			if !split {
				return
			}
			kind := otherInspectorView(first)
			v.inspectorPane(c, t, state, plan, "bottom", kind, []string{kind}, true, 36, width, &drop)
			n.Column(c).Absolute().Left(0).Right(0).Top(0).Height(1).Background(t.Border).PassThrough()
			grip := n.Column(c.Key("task-panel-resize")).Absolute().Left(0).Right(0).Top(0).Height(6).Cursor(n.CursorResizeNS).Focusable().Role(n.RoleSplitter).Label("调整侧栏分区高度")
			_, dy, held := grip.Dragged()
			change := float32(0)
			if held {
				change = -dy
			}
			if grip.Shortcut(0, n.KeyUp) {
				change = 10
			}
			if grip.Shortcut(0, n.KeyDown) {
				change = -10
			}
			if change != 0 {
				v.Model.InspectorSplitHeight = min(max(splitHeight+change, 120), splitLimit)
				c.AnimationFrame()
			}
		})
		n.Row(c.Key("inspector-actions")).Absolute().Right(12).Top(mainWindowTopInset).Height(toolbarActionSize).Gap(6).AlignItems(n.Center).Background(t.Background).Children(func() {
			if iconButton(c, refreshIcon, "刷新更改").Disabled(!v.Model.Connected).Clicked() {
				v.refreshChanges()
			}
			if iconButton(c, closeIcon, "关闭更改侧栏").Clicked() {
				v.Model.Inspectors[id] = false
			}
		})
		if hasTasks && layout.Joined {
			// The sidebar takes the body drop; the smaller icon strip takes merging.
			dragged, over := n.DragOver[inspectorTabDrag](panel)
			dropped, released := n.Drop[inspectorTabDrag](panel)
			_, y, inside := panel.PointerPosition()
			bottomZone := inside && y > height*.6
			valid := over && dragged.session == id
			if valid && bottomZone {
				n.Column(c.Key("inspector-split-preview")).Absolute().Left(6).Right(6).Bottom(6).Height(newSplitHeight-12).Radius(6).Background(t.Text.Alpha(.07)).Border(1, t.TextMuted.Alpha(.4)).PassThrough().Label("下方分屏预览").Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 100*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
			}
			if released && bottomZone && dropped.session == id {
				v.Model.InspectorSplitHeight = newSplitHeight
				next := app.InspectorLayout{First: otherInspectorView(dropped.kind), Selected: dropped.kind}
				drop = &next
			}
		}
		n.Column(c).Width(1).Absolute().Left(0).Top(0).Bottom(0).Background(t.Border).PassThrough()
		grip := n.Column(c.Key("inspector-resize")).Absolute().Left(0).Top(0).Bottom(0).Width(6).Cursor(n.CursorResizeEW).Focusable().Role(n.RoleSplitter).Label("调整右侧栏宽度")
		dx, _, held := grip.Dragged()
		v.splitRoot().resizingInspector = held
		change := float32(0)
		if held {
			change = -dx
		}
		if grip.Shortcut(0, n.KeyLeft) {
			change = 10
		}
		if grip.Shortcut(0, n.KeyRight) {
			change = -10
		}
		if change != 0 {
			v.Model.InspectorWidth = min(max(width+change, inspectorMinWidth), min(limit, inspectorMaxWidth))
			c.AnimationFrame()
		}
	})
	if drop != nil {
		v.Model.InspectorLayout = *drop
		c.AnimationFrame()
	}
}

// The same two view bodies move between the upper/lower group or a shared tab
// group. Their file selection and scroll state remain owned by changesState.
func (v *View) inspectorPane(c *n.Context, t *n.Theme, state *changesState, plan *domain.Plan, key, kind string, tabs []string, draggable bool, headerHeight, width float32, drop **app.InspectorLayout) {
	pane := n.Column(c.Key("inspector-pane:" + key)).FillWidth().FillHeight().Grow(1).Clip().Background(t.Background)
	if headerHeight > 0 {
		pane.Transition(n.ElementTransition{Duration: motionAnimation(c, 140*time.Millisecond).Duration, Colors: true, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
	}
	pane.Children(func() {
		inset := float32(12)
		var topInset, bottomInset float32
		right := float32(12)
		if key == "top" {
			topInset, bottomInset, right = mainWindowTopInset, mainToolbarBottomInset, 80
		}
		if draggable {
			inset += float32(len(tabs)*30) + 6
			defer v.inspectorTabs(c, t, tabs, kind, (headerHeight+topInset-bottomInset)/2, drop)
		}
		goBack := false
		header := func(file bool) {
			if headerHeight == 0 && !file {
				return
			}
			height, headerTop, headerBottom := headerHeight, topInset, bottomInset
			if file && v.Model.WorkerMinor >= 33 {
				height, headerTop, headerBottom = 36, 0, 0
			}
			if height == 0 {
				height = 36
			}
			leftInset, rightInset := inset, right
			if file && v.Model.WorkerMinor >= 33 {
				leftInset, rightInset = 12, 12
			}
			n.Row(c).FillWidth().Height(height).Shrink(0).Padding(headerTop, rightInset, headerBottom, leftInset).Gap(6).AlignItems(n.Center).Children(func() {
				if file {
					back := control(c, 5).Size(24, 26).Label("返回更改列表").Tooltip("返回更改列表").Children(func() { n.Icon(c, disclosureRight).Size(14, 14).Transform(n.Rotation(180)).TextColor(t.TextMuted) })
					goBack = back.Clicked()
					n.Text(c, state.Path).SingleLine().Grow(1).FontSize(12).TooltipWhenTruncated(state.Path)
				} else if kind == "tasks" {
					n.Text(c, "任务").Label("任务标题").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				} else if draggable {
					n.Text(c, "更改").Label("更改标题").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				} else {
					inlineIconText(c, changesIcon, "更改", n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}, 16, 8, t.TextMuted).Grow(1)
				}
			})
		}
		// Each view keeps its own fade snapshot; only the selected view uses live list state.
		view := n.Column(c.Key("inspector-view:" + kind)).FillWidth().FillHeight().Grow(1).Background(t.Background)
		if headerHeight > 0 {
			view.Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 140*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
		}
		view.Children(func() {
			if kind == "tasks" {
				pane.Label("任务面板")
				n.Column(c.Key("task-body")).FillWidth().FillHeight().Grow(1).Children(func() {
					header(false)
					v.inspectorTaskList(c, t, state, plan)
				})
				return
			}
			pane.Label("文件更改区域")
			fileOpen := state.Path != ""
			modern := v.Model.WorkerMinor >= 33
			fileTop := float32(0)
			if state.NonRepository {
				header(false)
				n.Column(c.Key("non-git-directory")).FillWidth().FillHeight().Grow(1).Padding(24).Gap(12).AlignItems(n.Center).Justify(n.Center).Children(func() {
					n.Icon(c, workspaceIcon).Size(24, 24).TextColor(t.TextMuted)
					n.Text(c, "当前目录不是 Git 仓库").FontSize(12).TextColor(t.TextMuted).TextAlign(n.Center)
				})
				return
			}
			if modern {
				header(false)
				v.diffComparisonBar(c, t, state, width)
				fileTop = headerHeight + 38
			}

			n.Column(c.Key("inspector-list")).FillWidth().Grow(1).MinHeight(0).IgnoreInput(fileOpen).Children(func() {
				if !modern {
					header(false)
				}
				if state.Error != "" {
					n.Text(c, state.Error).FontSize(12).TextColor(t.TextMuted).Padding(12)
				}
				if state.loading() && !state.loaded() {
					n.Text(c, "读取更改…").FontSize(12).TextColor(t.TextMuted).Padding(12)
				} else if state.loaded() && len(state.Files) == 0 && state.Error == "" {
					n.Text(c, "没有文件更改").FontSize(12).TextColor(t.TextMuted).Padding(12)
				}
				state.List.Key = func(i int) any { return state.Files[i].Path }
				n.List(c, &state.List, len(state.Files), func(i int) {
					file := state.Files[i]
					row := control(c, 0).FillWidth().Height(28).PaddingX(12).Label("查看文件:" + file.Path).Children(func() {
						n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
							n.Text(c, file.Path).SingleLine().Grow(1).MinWidth(0).FontSize(12).TooltipWhenTruncated(file.Path)
							added, deleted, _, _ := gitColors(t)
							a, d := "", ""
							if file.Added != nil && *file.Added != 0 {
								a = fmt.Sprintf("+%d", *file.Added)
							}
							if file.Removed != nil && *file.Removed != 0 {
								d = fmt.Sprintf("−%d", *file.Removed)
							}
							leftColor, rightColor := added, deleted
							switch file.Kind {
							case "add":
								a, d, rightColor = "新增", "—", added
								if file.Added != nil {
									d = fmt.Sprintf("+%d", *file.Added)
								}
							case "delete":
								a, d, leftColor = "删除", "—", deleted
								if file.Removed != nil {
									d = fmt.Sprintf("−%d", *file.Removed)
								}
							}
							if a == "" && d == "" {
								d, rightColor = "—", t.TextMuted
							}
							n.Text(c, a).Font("Menlo").FontSize(11).FixedLineHeight(18).Width(34).Shrink(0).TextAlign(n.End).TextColor(leftColor)
							n.Text(c, d).Font("Menlo").FontSize(11).FixedLineHeight(18).Width(34).Shrink(0).TextAlign(n.End).TextColor(rightColor)
						})
					})
					if row.Clicked() {
						v.openChangedFile(file.Path)
					}
				}).FillWidth().FillHeight().Grow(1)
			})
			n.Column(c.Key("inspector-file-layer")).Absolute().Left(0).Right(0).Top(fileTop).Bottom(0).Clip().PassThrough().Children(func() {
				if !fileOpen {
					return
				}
				pose := n.Motion{X: width, Opacity: 1}
				if c.Preferences().ReduceMotion {
					pose.X = 0
				}
				n.Column(c.Key("inspector-file")).FillWidth().FillHeight().Background(t.Background).Transition(n.ElementTransition{Duration: motionAnimation(c, 260*time.Millisecond).Duration, Ease: n.EaseOut, Colors: true, Enter: &pose, Exit: &pose}).Children(func() {
					// Delay the Back mutation until after the exit snapshot has real content.
					header(true)
					v.diffBody(c, t, state)
				})
			})
			if goBack {
				state.closeFile()
			}
		})
	})
}

func (v *View) inspectorTaskList(c *n.Context, t *n.Theme, state *changesState, plan *domain.Plan) {
	start := 0
	if plan.Explanation != "" || plan.Narrative != "" {
		start = 1
	}
	state.Tasks.Key = func(i int) any {
		if i == len(plan.Steps)+start {
			return "truncated"
		}
		if i < start {
			return "explanation"
		}
		return fmt.Sprintf("%d:%s", i-start, plan.Steps[i-start].Text)
	}
	count := len(plan.Steps) + start
	if plan.Truncated {
		count++
	}
	n.List(c, &state.Tasks, count, func(i int) {
		if i == len(plan.Steps)+start {
			n.Text(c, "任务内容过长，已截断显示").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Padding(8, 12)
			return
		}
		if i < start {
			n.Column(c).FillWidth().Padding(8, 12).Gap(8).Children(func() {
				if plan.Narrative != "" {
					n.Text(c, plan.Narrative).FontSize(12).FixedLineHeight(20).Selectable()
				}
				if plan.Explanation != "" {
					n.Text(c, plan.Explanation).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				}
			})
			return
		}
		i -= start
		step := plan.Steps[i]
		status, color := "待处理", t.TextMuted
		switch step.Status {
		case "completed":
			status = "已完成"
		case "running":
			status, color = "进行中", t.Text
		}
		font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}
		n.Row(c).FillWidth().Padding(8, 12).Gap(8).AlignItems(n.Baseline).Label(status + "：" + step.Text).Children(func() {
			switch step.Status {
			case "running":
				statusDot(c, step.Text, font, t.Success, true).Width(14).TextAlign(n.Center)
			case "completed":
				inlineSymbol(c, check, step.Text, font, 14, color).Width(14)
			default:
				statusDot(c, step.Text, font, t.TextMuted, false).Width(14).TextAlign(n.Center)
			}
			text := n.Text(c, step.Text).Grow(1).FontSize(12).FixedLineHeight(20).TextColor(color)
			if step.Status == "running" {
				shimmer(c, text, t.TextMuted, t.Text)
			}
		})
	}).FillWidth().FillHeight().Grow(1).Label("任务列表")
}
func (v *View) diffBody(c *n.Context, t *n.Theme, state *changesState) {
	if state.Error != "" && !state.fileAvailable() {
		n.Text(c, state.Error).FontSize(12).TextColor(t.TextMuted).Padding(14)
		return
	}
	if !state.fileAvailable() {
		n.Text(c, "读取文件…").FontSize(12).TextColor(t.TextMuted).Padding(14)
		return
	}
	if state.Versions.Unchanged || state.Versions.Old == state.Versions.New {
		n.Column(c.Key("diff-unchanged")).Fill().AlignItems(n.Center).Justify(n.Center).Children(func() {
			message := "此文件在当前比较中没有差异"
			if !state.Versions.Unchanged {
				message = "此文件没有文本差异"
			}
			n.Text(c, message).FontSize(12).TextColor(t.TextMuted).Label("当前文件无差异")
		})
		return
	}
	added, deleted, addedFill, deletedFill := gitColors(t)
	state.Diff.Font = n.Font{Family: v.Model.Preferences.CodeFont, Size: 11, Weight: 400, Antialiased: true}
	state.Diff.LineHeight = 23.0 / 11
	state.Diff.Inline = editor.DiffInlineNone
	state.Diff.Indicators = editor.DiffClassic
	state.Diff.Theme = &editor.Theme{Background: t.Background, Text: t.Text, LineNumber: subtle(t), Added: added, Deleted: deleted, AddedBackground: addedFill, DeletedBackground: deletedFill}
	state.Diff.Theme.Selection = t.Text.Alpha(.15)
	state.Diff.Theme.InactiveSelection = t.Text.Alpha(.12)
	state.Diff.TextSelection = v.Model.WorkerMinor >= 33 && state.Versions.Snapshot.ID != ""
	v.configureDiffComments(c, t, state)
	viewer := editor.DiffView(c.Key("diff"), state.Diff).FillWidth().FillHeight().Grow(1).Label("原生文件差异")
	if width := viewer.Bounds().W; width > 0 && width != state.CommentWidth {
		state.CommentWidth = width
		c.AnimationFrame()
	}
	v.diffSelectionAction(c, t, state, viewer)
}
