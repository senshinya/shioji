package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

const reviewComparisonMinor = 33

type reviewState struct {
	session      domain.Session
	parent       *mygo.Window
	list         *reviewState
	draftPage    *mygo.Window
	draft        *mobileDraftEditor
	comparison   *domain.DiffComparison
	pendingRange *domain.DiffComparison
	failedRange  *reviewRangeFailure
	versions     domain.FileVersions
	files        []domain.ChangedFile
	filesLoaded  bool
	path         string
	diff         *editor.DiffState
	err          string
	loading      bool
	request      uint64
	showRange    bool
	refs         []domain.DiffEndpoint
	rangeJob     mobileRequest
	refPicker    *reviewRefPicker
	note         *diffCommentDraft
	noteAdded    bool
	linked       *mobileLinkedPreview
	comment      *mobileDiffCommentReview
	previous     *reviewState
}

type reviewRangeFailure struct {
	pair domain.DiffComparison
	path string
	err  string
}

type splitReview struct {
	owner *mobileApp
	state *reviewState
	right bool
}

func (a *mobileApp) currentSplitReview() splitReview {
	if a.review != nil {
		return splitReview{owner: a, state: a.review, right: a.reviewBelongsToParallel(a.review)}
	}
	if pane := a.parallelDevice; pane != nil && pane.review != nil {
		return splitReview{owner: pane, state: pane.review, right: true}
	}
	return splitReview{}
}

func (a *mobileApp) reviewBelongsToParallel(r *reviewState) bool {
	return r != nil && a.parallelDevice == nil && a.parallel != "" && r.session.ID == a.parallel
}

type diffCommentDraft struct {
	selection editor.DiffTextSelection
	versions  domain.FileVersions
	text      string
	request   mobileRequest
}

func (a *mobileApp) openReview(parent *mygo.Window, session domain.Session, draftPage *mygo.Window, draft *mobileDraftEditor) *reviewState {
	if draft != nil && (!draft.current(a, draftPage) || draft.target.session != session.ID || draft.target.workspace != session.WorkspaceID) {
		draft = nil
	}
	if draft != nil && !draft.Doc.Composing() && draft.Publish(a.model, draft.target.key) {
		a.save()
	}
	r := &reviewState{session: session, parent: parent, draftPage: draftPage, draft: draft}
	if a.wide && parent == a.detail {
		a.replaceReview(r)
	} else {
		page := a.page("改动", func(c *ui.Context) { a.reviewContent(c, r, nil) })
		r.parent = page
		mygo.SetBarItems(page,
			mygo.BarItem{Title: "选择比较范围", Icon: toolbarImage(compareSymbol), OnClick: func() { a.openDiffComparison(r) }},
			a.reviewRefreshBarItem(r))
		a.pushNative(parent, page)
	}
	a.loadReview(r)
	a.refresh()
	return r
}

func (a *mobileApp) showReviewFile(r *reviewState, file string) *reviewState {
	if a.wide && r.parent == a.detail {
		a.loadReviewFile(r, file)
		return r
	}
	detail := &reviewState{session: r.session, list: r, comparison: r.comparison, files: r.files, filesLoaded: r.filesLoaded,
		draftPage: r.draftPage, draft: r.draft}
	page := a.page(path.Base(file), func(c *ui.Context) { a.reviewContent(c, detail, nil) })
	detail.parent = page
	page.OnClosed(func() { detail.request++ })
	mygo.SetBarItems(page, a.reviewRefreshBarItem(detail))
	a.loadReviewFile(detail, file)
	a.pushNative(r.parent, page)
	return detail
}

func (a *mobileApp) reviewRefreshBarItem(r *reviewState) mygo.BarItem {
	return mygo.BarItem{Title: "刷新改动", Symbol: "arrow.clockwise", OnClick: func() { a.refreshReview(r) }}
}

func (a *mobileApp) reviewContent(c *ui.Context, r *reviewState, close func()) {
	if r.comment != nil {
		a.savedDiffCommentView(c, r, close)
		return
	}
	if r.linked != nil {
		a.linkedFileView(c, r, close)
		return
	}
	p := palette(c)
	ui.Column(c).Fill().MinWidth(0).Background(p.canvas).Children(func() {
		if close != nil {
			ui.Row(c).MinHeight(55).Padding(2, 8, 4).AlignItems(ui.Center).Gap(8).Children(func() {
				if r.path != "" {
					iconButton(c, "返回文件列表", backSymbol, toolbarIconSize, func() {
						r.path, r.diff, r.err, r.loading = "", nil, "", false
						r.pendingRange, r.note = nil, nil
						r.request++
					})
					oneLine(c, path.Base(r.path), 16, p.ink).Grow(1).FontWeight(650)
				} else {
					oneLine(c, "改动", 17, p.ink).FontWeight(650).Grow(1).PaddingX(8)
					iconButton(c, "选择比较范围", compareSymbol, toolbarIconSize, func() { a.openDiffComparison(r) })
				}
				iconButton(c, "刷新改动", refreshSymbol, toolbarIconSize, func() { a.refreshReview(r) }).Disabled(r.loading)
				iconButton(c, "关闭改动", closeSymbol, toolbarIconSize, close)
			})
		}
		if r.showRange && r.path == "" {
			a.reviewRange(c, r)
			if r.refPicker != nil {
				a.reviewRefOptions(c, r, r.refPicker)
				return
			}
		}
		hasContent := r.path == "" && r.filesLoaded || r.path != "" && r.diff != nil
		if failure := r.failedRange; failure != nil && (r.path == "" || r.path == failure.path) {
			ui.Row(c).Padding(0, pageInset).Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, failure.err, captionTextSize, p.warning).Grow(1).MinWidth(0).Role(ui.RoleStatus)
				action(c, "重试比较", func() {
					a.compareReview(r, failure.pair)
					a.refresh()
				}).Shrink(0)
			})
		} else if r.loading && hasContent {
			oneLine(c, "正在刷新…", captionTextSize, p.secondary).Padding(8, pageInset).Role(ui.RoleStatus)
		}
		if r.err != "" {
			label(c, r.err, 13, p.warning).Padding(12)
			action(c, "重试", func() {
				if r.path != "" {
					a.loadReviewFile(r, r.path)
				} else {
					a.loadReview(r)
				}
			}).AlignSelf(ui.Start)
		}
		switch {
		case r.path != "" && r.diff != nil:
			a.reviewDiff(c, r)
		case r.loading && !hasContent:
			oneLine(c, "正在读取…", 13, p.secondary).Padding(12)
		case r.path == "" && r.filesLoaded && len(r.files) == 0:
			oneLine(c, "暂无改动", 13, p.secondary).Padding(16)
		case r.path == "" && r.filesLoaded:
			ui.Scroll(c).Grow(1).Basis(0).Padding(pageTopInset, pageInset, 40).Children(func() {
				ui.Row(c).Gap(10).MinHeight(32).AlignItems(ui.Center).Children(func() {
					oneLine(c, fmt.Sprintf("%d 个文件", len(r.files)), 13, p.secondary)
					add, del, added, deleted := 0, 0, false, false
					for _, file := range r.files {
						if file.Added != nil {
							add += *file.Added
							added = true
						}
						if file.Removed != nil {
							del += *file.Removed
							deleted = true
						}
					}
					diffCounts(c, add, del, added, deleted)
				})
				for _, file := range r.files {
					row := c.Key(file.Path)
					subtitle := path.Dir(file.Path)
					if subtitle == "." {
						subtitle = ""
					}
					mobileDetailRow(row, path.Base(file.Path), subtitle, file.Path, fileCodeSymbol, func() {
						if !r.loading {
							a.showReviewFile(r, file.Path)
						}
					}, func() {
						add, del := 0, 0
						if file.Added != nil {
							add = *file.Added
						}
						if file.Removed != nil {
							del = *file.Removed
						}
						diffCounts(row, add, del, file.Added != nil, file.Removed != nil)
					}).Disabled(r.loading)
				}
			})
		}
	})
}

func diffCounts(c *ui.Context, add, del int, added, deleted bool) {
	p := palette(c)
	if added {
		oneLine(c, fmt.Sprintf("+%d", add), 11.5, p.active).Font("monospace").PassThrough()
	}
	if deleted {
		oneLine(c, fmt.Sprintf("−%d", del), 11.5, p.diffRemoved).Font("monospace").PassThrough()
	}
}

func (a *mobileApp) reviewDiff(c *ui.Context, r *reviewState) {
	p := palette(c)
	if r.versions.Unchanged || r.versions.Old == r.versions.New {
		message := "此文件在当前比较中没有差异"
		if !r.versions.Unchanged {
			message = "此文件没有文本差异"
		}
		ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Padding(pageInset).Children(func() {
			label(c, message, controlTextSize, p.secondary).Role(ui.RoleStatus)
		})
		if r.note != nil {
			a.reviewCommentFooter(c, r)
		}
		return
	}
	folder := path.Dir(r.path)
	if folder == "." {
		folder = r.path
	}
	ui.Scroll(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
		ui.Column(c).Padding(pageTopInset, pageInset, 30).Gap(6).Children(func() {
			ui.Row(c).Gap(8).MinHeight(30).AlignItems(ui.Center).Children(func() {
				oneLine(c, folder, 11.5, p.secondary).Font("monospace").Shrink(1)
				for _, file := range r.files {
					if file.Path == r.path {
						add, del := 0, 0
						if file.Added != nil {
							add = *file.Added
						}
						if file.Removed != nil {
							del = *file.Removed
						}
						diffCounts(c, add, del, file.Added != nil, file.Removed != nil)
						break
					}
				}
			})
			a.styleMobileDiff(c, r.diff)
			ui.Column(c).MinHeight(0).Radius(cardRadius).Background(p.inset).Clip().Children(func() {
				oneLine(c, "@@ · "+path.Base(r.path), 11, p.secondary).Font("monospace").Padding(9, 11)
				editor.DiffView(c, r.diff).MinHeight(0).Label("文件差异：" + r.path)
			})
		})
	})
	if r.note != nil || r.versions.Snapshot.ID != "" && (r.draft != nil || a.preview) {
		a.reviewCommentFooter(c, r)
	}
}

func (a *mobileApp) styleMobileDiff(c *ui.Context, diff *editor.DiffState) {
	p := palette(c)
	theme := editor.DefaultTheme(c.Theme())
	theme.Background, theme.Text = p.inset, p.ink
	theme.LineNumber, theme.ActiveLineNumber = p.secondary, p.secondary
	theme.Selection, theme.InactiveSelection = p.ink.Alpha(.17), p.ink.Alpha(.12)
	theme.Added, theme.Deleted = p.active, p.diffRemoved
	theme.AddedBackground, theme.DeletedBackground = p.diffAddedFill, p.diffRemovedFill
	diff.Theme = theme
	const codeFontSize = 12
	const codeRowHeight = 25
	diff.Font = ui.Font{Family: a.codeFont(), Size: codeFontSize * c.Preferences().TextScale}
	diff.LineHeight = float32(codeRowHeight) / codeFontSize
}

func (a *mobileApp) loadReview(r *reviewState) {
	if a.model.WorkerMinor >= reviewComparisonMinor {
		a.compareReview(r, domain.DefaultDiffComparison())
		return
	}
	a.loadLegacyReview(r)
}

func (a *mobileApp) refreshReview(r *reviewState) {
	if r.loading {
		return
	}
	if a.preview || a.model.WorkerMinor >= reviewComparisonMinor {
		a.compareReview(r, unpinReviewRange(r.confirmedRange()))
	} else {
		a.loadLegacyReview(r)
	}
	a.refresh()
}

func (a *mobileApp) loadLegacyReview(r *reviewState) {
	query := protocol.GitQuery{WorkspaceID: r.session.WorkspaceID, SessionID: r.session.ID}
	a.reviewCommand(r, "git.changes", query, func(data json.RawMessage, err error) error {
		if err != nil {
			return err
		}
		var files []domain.ChangedFile
		if err := json.Unmarshal(data, &files); err != nil {
			return err
		}
		r.acceptReviewFiles(nil, files)
		if r.path != "" {
			a.loadReviewFile(r, r.path)
		}
		return nil
	})
}

func (r *reviewState) acceptReviewFiles(comparison *domain.DiffComparison, files []domain.ChangedFile) {
	r.comparison, r.files, r.filesLoaded = comparison, files, true
	if r.list != nil {
		r.list.comparison, r.list.files, r.list.filesLoaded = comparison, files, true
		r.list.parent.Invalidate()
	}
}

func (a *mobileApp) compareReview(r *reviewState, pair domain.DiffComparison) {
	r.pendingRange, r.failedRange = &pair, nil
	query := protocol.GitQuery{WorkspaceID: r.session.WorkspaceID, SessionID: r.session.ID, Comparison: &pair, ReviewPath: r.path}
	a.reviewCommand(r, "git.compare", query, func(data json.RawMessage, err error) error {
		r.pendingRange = nil
		var result domain.ComparisonResult
		if err == nil {
			err = json.Unmarshal(data, &result)
		}
		if err == nil && query.ReviewPath != "" && (result.Versions == nil || result.Versions.Path != query.ReviewPath) {
			err = errors.New("未收到当前文件的差异")
		}
		if err != nil {
			var remote *protocol.RemoteError
			if !r.filesLoaded && pair == domain.DefaultDiffComparison() &&
				errors.As(err, &remote) && remote.Code == "notGitRepository" {
				r.filesLoaded = true
			} else {
				r.failedRange = &reviewRangeFailure{pair: pair, path: query.ReviewPath, err: err.Error()}
			}
			return nil
		}
		r.acceptReviewFiles(&result.Comparison, result.Files)
		if result.Versions != nil {
			r.installReviewFile(*result.Versions)
		}
		return nil
	})
}

func (a *mobileApp) loadReviewFile(r *reviewState, file string) {
	if r.path != file {
		r.path, r.diff, r.note, r.noteAdded = file, nil, nil, false
	}
	q := protocol.FileQuery{GitQuery: protocol.GitQuery{WorkspaceID: r.session.WorkspaceID, SessionID: r.session.ID}, Path: file}
	if a.model.WorkerMinor >= reviewComparisonMinor {
		q.Comparison = r.comparison
		if q.Comparison == nil {
			defaultRange := domain.DefaultDiffComparison()
			q.Comparison = &defaultRange
		}
	}
	a.reviewCommand(r, "git.file", q, func(data json.RawMessage, err error) error {
		if err != nil {
			return err
		}
		var versions domain.FileVersions
		if err := json.Unmarshal(data, &versions); err != nil {
			return err
		}
		r.installReviewFile(versions)
		return nil
	})
}

func (r *reviewState) installReviewFile(versions domain.FileVersions) {
	r.versions = versions
	r.diff = &editor.DiffState{
		Old: editor.NewBuffer(versions.Old), New: editor.NewBuffer(versions.New),
		Layout: editor.DiffUnified, FitContent: true, Indicators: editor.DiffClassic,
	}
	if versions.Snapshot.ID == "" {
		return
	}
	d := r.diff
	d.TextSelection, d.LineSelection = true, true
	d.OnTextSelection = func(_ editor.DiffTextSelection) { r.parent.Invalidate() }
	d.OnLineSelection = func(lines editor.DiffLineRange) {
		buffer := d.New
		if lines.Side == editor.DiffOld {
			buffer = d.Old
		}
		view := buffer.Snapshot()
		d.SetTextSelection(editor.DiffTextPoint{Side: lines.Side, Offset: view.LineStart(lines.Start)},
			editor.DiffTextPoint{Side: lines.Side, Offset: view.LineEnd(lines.End)})
		r.parent.Invalidate()
	}
}

func (a *mobileApp) reviewCommand(r *reviewState, method string, input any, apply func(json.RawMessage, error) error) {
	epoch := a.model.ConnectionEpoch
	r.request++
	request := r.request
	r.loading, r.err = true, ""
	finish := func(data json.RawMessage, err error) {
		r.loading = false
		if err := apply(data, err); err != nil {
			r.err = err.Error()
		}
	}
	if a.preview {
		files := []domain.ChangedFile{{Path: "internal/ui/composer.go", Kind: "modified"}}
		if a.previewReviewBoard {
			files = []domain.ChangedFile{
				{Path: "internal/transport/connection.go", Kind: "modified", Added: countPointer(29), Removed: countPointer(8)},
				{Path: "internal/transport/reconnect_test.go", Kind: "modified", Added: countPointer(18), Removed: countPointer(2)},
				{Path: "internal/ui/device_state.go", Kind: "modified", Added: countPointer(6), Removed: countPointer(2)},
			}
		}
		var fixture any
		switch method {
		case "git.changes":
			fixture = files
		case "git.compare":
			query := input.(protocol.GitQuery)
			result := domain.ComparisonResult{Comparison: *query.Comparison, Files: files}
			if query.ReviewPath != "" {
				versions := previewReviewFile(query.ReviewPath)
				versions.Comparison = result.Comparison
				result.Versions = &versions
			}
			fixture = result
		case "git.file":
			fixture = previewReviewFile(input.(protocol.FileQuery).Path)
		default:
			finish(nil, errors.New("没有此操作的预览数据"))
			return
		}
		payload, err := json.Marshal(fixture)
		finish(payload, err)
		return
	}

	if !a.model.Connected || a.controller == nil {
		finish(nil, errors.New("连接执行设备后读取改动"))
		return
	}
	controller, selection := a.controller, a.deviceSelection
	if !a.controller.Do(app.Job{Method: method, Input: input, ConnectionEpoch: epoch, Apply: func(_ *app.Model, data json.RawMessage, err error) {
		if r.request != request || !a.pageCurrent(r.parent) {
			return
		}
		if epoch != a.model.ConnectionEpoch || controller != a.controller || selection != a.deviceSelection {
			err = errors.New("连接已改变，请重试")
		}
		finish(data, err)
	}}) {
		finish(nil, errors.New("请求繁忙，请稍后重试"))
	}
}

func previewReviewFile(file string) domain.FileVersions {
	old, newText := "func send() {\n    submit()\n}\n", "func send() {\n    if ready() { submit() }\n}\n"
	if file == "internal/transport/connection.go" {
		prefix := "func (c *Client) reconnect(ctx context.Context) error {\n    state := c.store.Snapshot()\n"
		suffix := "    return c.follow(ctx, state.Cursor)\n}\n"
		old = prefix + "    c.resetSession()\n" + suffix
		newText = prefix + "    if err := c.resume(ctx, state.SessionID); err != nil {\n        return err\n    }\n" + suffix
	}
	return domain.FileVersions{Path: file, Old: old, New: newText}
}

func unpinReviewRange(pair domain.DiffComparison) domain.DiffComparison {
	pair.Repository = ""
	for _, endpoint := range []*domain.DiffEndpoint{&pair.Base, &pair.Target} {
		endpoint.Commit, endpoint.Branch = "", ""
		if endpoint.Kind == "empty" && endpoint.Name == "HEAD" {
			endpoint.Kind = "head"
		}
	}
	return pair
}

func countPointer(value int) *int { return &value }

func comparisonEndpointName(ep domain.DiffEndpoint) string {
	if ep.Name != "" {
		return ep.Name
	}
	switch ep.Kind {
	case "head":
		return "HEAD"
	case "worktree":
		return "工作目录"
	case "empty":
		return "空版本"
	default:
		return ep.Kind
	}
}

func (a *mobileApp) openDiffComparison(r *reviewState) {
	r.showRange = !r.showRange
	r.refPicker = nil
	a.refresh()
}

func (r *reviewState) confirmedRange() domain.DiffComparison {
	if r.comparison != nil {
		return *r.comparison
	}
	return domain.DefaultDiffComparison()
}

func (r *reviewState) requestedRange() domain.DiffComparison {
	if r.pendingRange != nil {
		return *r.pendingRange
	}
	return r.confirmedRange()
}

func (a *mobileApp) reviewRange(c *ui.Context, r *reviewState) {
	comparison := r.confirmedRange()
	p := palette(c)
	ui.Column(c).Padding(4, 16).Children(func() {
		rowDivider(ui.Row(c).MinHeight(65).AlignItems(ui.Center).Gap(6), p.line).
			Label("比较范围").Children(func() {
			choice := func(labelText string, base bool, endpoint domain.DiffEndpoint) {
				col := c.Key(labelText)
				ui.Column(col).Grow(1).Basis(0).MinWidth(0).Children(func() {
					oneLine(col, labelText, 12, p.secondary)
					name := comparisonEndpointName(endpoint)
					iconAction(col, name, labelText, nil, 0, pickerSymbol, 12, 14, 500, func() {
						a.toggleReviewRefPicker(r, base)
					}).PaddingX(0).Value(name).AccessSelected(r.refPicker != nil && r.refPicker.base == base)
				})
			}
			choice("基准", true, comparison.Base)
			iconButton(c, "交换基准与目标", swapSymbol, 17, func() {
				r.refPicker = nil
				next := r.requestedRange()
				next.Base, next.Target = next.Target, next.Base
				a.compareReview(r, next)
				a.refresh()
			})
			choice("目标", false, comparison.Target)
		})
	})
}

func (a *mobileApp) reviewDraftCurrent(r *reviewState) bool {
	b := r.draft
	return b != nil && b.current(a, r.draftPage) && !b.pending() && !b.Doc.Composing() &&
		(r.parent != a.detail || a.review == r) && !a.sessionByID(r.session.ID).Archived && !a.workspace(r.session.WorkspaceID).Archived
}

func (a *mobileApp) reviewCommentFooter(c *ui.Context, r *reviewState) {
	p := palette(c)
	selection, selected := r.diff.SelectedText()
	ui.Column(c).Padding(6, 16, 4).Gap(4).Shrink(0).Children(func() {
		if r.note == nil {
			title := "点按行号选择代码"
			if r.noteAdded {
				title = "已加入当前会话草稿"
			}
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				oneLine(c, title, 12, p.secondary).Grow(1)
				action(c, "评论选区", func() { r.note = &diffCommentDraft{selection: selection, versions: r.versions} }).
					Disabled(!selected || selection.Dragging)
			})
			return
		}
		note := r.note
		versions := note.versions
		mobileComposerFrame(c.Key("comment-composer"), false).Padding(2, 6, 5, 12).Gap(2).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				title := "代码选区评论"
				if len(note.selection.Fragments) > 0 {
					part := note.selection.Fragments[0]
					side := "目标"
					if part.Side == editor.DiffOld {
						side = "基准"
					}
					title = fmt.Sprintf("%s · 第 %d 行", side, part.StartLine)
				}
				if versions.Snapshot.ID != r.versions.Snapshot.ID && versions.Snapshot.SHA256 != r.versions.Snapshot.SHA256 {
					title += " · 刷新前"
				}
				oneLine(c, title, 13, p.ink).FontWeight(620).Grow(1)
				iconButton(c, "取消代码评论", closeSymbol, 15, func() { r.note = nil }).KeepFocus().Disabled(note.request.busy)
			})
			ui.Row(c).AlignItems(ui.End).Gap(6).Children(func() {
				ui.TextAreaBase(c, &note.text).Label("代码选区评论").Multiline(true).
					Placeholder("写下对所选代码的评论…").PlaceholderColor(p.secondary).TextColor(p.ink).
					Height(62).Grow(1).MinWidth(0).Padding(6, 0)
				sendButton(c, "加入当前草稿", sendSymbol, false,
					note.request.busy || strings.TrimSpace(note.text) == "" || len(note.selection.Fragments) == 0, func() {
						if a.preview {
							r.note, r.noteAdded = nil, true
							return
						}
						if !a.reviewDraftCurrent(r) {
							note.request.err = "会话草稿或比较范围已改变"
							return
						}
						draft, target := r.draft, r.draft.target
						var comments int
						for _, ref := range a.model.Attachments[target.key] {
							if ref.Attachment.Kind == domain.DiffCommentResource {
								comments++
							}
						}
						if comments >= 8 {
							note.request.err = "最多添加 8 条代码评论"
							return
						}
						chip := draft.chipMaker(c)
						epoch := a.model.ConnectionEpoch
						in := protocol.DiffCommentInput{GitQuery: protocol.GitQuery{WorkspaceID: r.session.WorkspaceID,
							SessionID: r.session.ID, Comparison: &versions.Comparison}, Snapshot: versions.Snapshot, Comment: strings.TrimSpace(note.text)}
						for _, part := range note.selection.Fragments {
							side := "target"
							if part.Side == editor.DiffOld {
								side = "base"
							}
							in.Fragments = append(in.Fragments, domain.CodeSelection{Side: side, Start: part.Start, End: part.End})
						}
						a.pageJob(r.parent, &note.request, "diff.comment", in, decodePage(func(item domain.Attachment) error {
							if r.note != note || epoch != a.model.ConnectionEpoch || !a.reviewDraftCurrent(r) || item.Kind != domain.DiffCommentResource {
								return fmt.Errorf("无法将评论加入当前草稿")
							}
							ref := mobileclient.DraftReference{File: app.DraftAttachment{
								Attachment: item, DeviceID: target.device, WorkspaceID: target.workspace, Ready: true,
							}}
							if !draft.InsertAttachment(a.model, target.key, ref, chip) {
								return fmt.Errorf("评论已在草稿中，或草稿已改变")
							}
							a.save()
							r.note, r.noteAdded = nil, true
							return nil
						}))
					}).KeepFocus()
			})
			note.request.status(c)
		})
	})
}
