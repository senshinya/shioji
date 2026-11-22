package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

const (
	importAll       = "全部"
	importAvailable = "可导入"
	importedOnly    = "已导入"
)

type mobileImportPicker struct {
	model               *app.Model
	workspace           domain.Workspace
	harnesses           []domain.HarnessInfo
	harness             string
	window              *mygo.Window
	list                ui.ListState
	catalog             mobileRequest
	items               []domain.ConversationCandidate
	cursor              string
	seen                map[string]bool
	filter, search      string
	selected            map[string]bool
	failures            map[string]string
	batch               []domain.ConversationCandidate
	batchEpoch, message string
	imported            int
	busy                bool
	started             bool
}

func (a *mobileApp) openConversationImport(parent *mygo.Window, workspace domain.Workspace) {
	if current := a.workspace(workspace.ID); current.ID != "" {
		workspace = current
	}
	u := &mobileImportPicker{
		model: a.model, workspace: workspace, filter: importAll,
		selected: map[string]bool{}, failures: map[string]string{}, seen: map[string]bool{},
	}
	for _, h := range a.model.Readiness.Harnesses {
		if h.Capabilities.ConversationImport {
			u.harnesses = append(u.harnesses, h)
			if u.harness == "" || h.ID == a.model.HarnessID() {
				u.harness = h.ID
			}
		}
	}
	if a.preview {
		u.harness = a.model.HarnessID()
		u.items = []domain.ConversationCandidate{
			{SourceRef: "old-ssh", Title: "检查旧版 SSH 配置", HarnessID: u.harness, Directory: "/Users/shinya/work/Shioji", UpdatedAt: time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local)},
			{SourceRef: "check", Title: "改进连接检查", HarnessID: u.harness, Directory: "/Users/shinya/work/Shioji", UpdatedAt: time.Date(2026, 11, 17, 9, 0, 0, 0, time.Local)},
		}
	}
	u.window = a.page("导入会话", func(c *ui.Context) { u.draw(a, c) })
	if !a.preview && u.valid(a) {
		u.load(a, "")
	}
	a.pushNative(parent, u.window)
}

func (u *mobileImportPicker) valid(a *mobileApp) bool {
	if !a.pageCurrent(u.window) || a.model != u.model || !a.model.Connected || u.harness == "" {
		return false
	}
	w := a.workspace(u.workspace.ID)
	return w.ID != "" && !w.Archived && w.Revision == u.workspace.Revision &&
		(a.preview || a.model.WorkerMinor >= 32)
}

func (u *mobileImportPicker) query(item domain.ConversationCandidate) protocol.ConversationQuery {
	return protocol.ConversationQuery{WorkspaceID: u.workspace.ID, Revision: u.workspace.Revision, HarnessID: u.harness,
		SourceRef: item.SourceRef, Directory: item.Directory}
}

func (u *mobileImportPicker) load(a *mobileApp, cursor string) {
	if !u.valid(a) || u.catalog.busy || u.busy {
		return
	}
	if cursor == "" {
		u.seen = map[string]bool{}
	}
	u.started = true
	q := u.query(domain.ConversationCandidate{})
	q.Cursor = cursor
	epoch := a.model.ConnectionEpoch
	a.pageJob(u.window, &u.catalog, "conversation.list", q, decodePage(func(page domain.ConversationCatalog) error {
		if !u.valid(a) || a.model.ConnectionEpoch != epoch || u.harness != q.HarnessID {
			return fmt.Errorf("设备或工作区已变化，请重新读取")
		}
		if len(page.Items) > 50 || len(page.Cursor) > 4096 {
			return fmt.Errorf("历史目录无效")
		}
		if page.Cursor != "" && u.seen[page.Cursor] {
			u.cursor = ""
			return fmt.Errorf("历史分页未继续推进")
		}
		for _, item := range page.Items {
			if item.SourceRef == "" || item.HarnessID != u.harness || !slices.Contains(u.workspace.Folders, item.Directory) {
				return fmt.Errorf("历史来源不属于此工作区")
			}
		}
		if cursor == "" {
			u.items = nil
		}
		for _, item := range page.Items {
			i := slices.IndexFunc(u.items, func(old domain.ConversationCandidate) bool { return old.SourceRef == item.SourceRef })
			if i < 0 {
				u.items = append(u.items, item)
			} else {
				u.items[i] = item
			}
			if item.ImportedSessionID != "" {
				delete(u.selected, item.SourceRef)
				delete(u.failures, item.SourceRef)
			}
		}
		u.cursor = page.Cursor
		if len(u.items) >= 1000 {
			u.items, u.cursor = u.items[:1000], ""
			u.message = "仅展示最近的 1000 个会话"
		}
		if u.cursor != "" {
			u.seen[u.cursor] = true
			if len(u.items) == 0 && len(u.seen) < 20 {
				u.load(a, u.cursor)
			}
		}
		return nil
	}))
}

func (u *mobileImportPicker) draw(a *mobileApp, c *ui.Context) {
	p := palette(c)
	live := u.valid(a)
	if live && !a.preview && !u.started {
		u.load(a, "")
	}
	if u.busy && (!live || a.model.ConnectionEpoch != u.batchEpoch) {
		u.stopBatch()
	}
	disabled := !live || u.busy
	term := strings.ToLower(strings.TrimSpace(u.search))
	ui.Column(c).Fill().Background(p.canvas).Children(func() {
		mobilePageContent(c.Key("import-controls"), 8).Gap(6).Children(func() {
			mobilePageContext(c, u.workspace.Name, a.deviceName())
			if len(u.harnesses) > 1 {
				name := u.harness
				for _, agent := range u.harnesses {
					if agent.ID == u.harness {
						name = agent.Name
					}
				}
				formChoice(c, "来源 Agent", name, func(menu *ui.Menu) {
					for _, agent := range u.harnesses {
						menuChoice(menu, agent.Name, agent.ID == u.harness, func() {
							if agent.ID != u.harness {
								u.harness = agent.ID
								u.items, u.cursor, u.message = nil, "", ""
								u.imported = 0
								clear(u.selected)
								clear(u.failures)
								u.load(a, "")
							}
						})
					}
				}).Disabled(disabled || u.catalog.busy)
			}
			mobileSearchField(c.Key("import-search"), ui.Row(c), &u.search, "搜索导入会话", "搜索会话", nil)
			mobileSegments(c, []mobileSegment{{title: importAll}, {title: importAvailable}, {title: importedOnly}}, u.filter,
				func(title string) { u.filter = title })
		})
		shown := make([]domain.ConversationCandidate, 0, len(u.items))
		for _, item := range u.items {
			if (u.filter == importAvailable && item.ImportedSessionID != "") ||
				(u.filter == importedOnly && item.ImportedSessionID == "") ||
				!strings.Contains(strings.ToLower(item.Title), term) {
				continue
			}
			shown = append(shown, item)
		}
		count := len(shown)
		footer := u.cursor != "" || u.catalog.err != "" || count == 0
		rows := count
		if footer {
			rows++
		}
		ui.List(c.Key(u.filter+":"+term), &u.list, rows, func(index int) {
			if index == count {
				mobileContent(c.Key("catalog-end"), contentMaxWidth).PaddingX(pageInset).Children(func() {
					switch {
					case u.catalog.err != "":
						u.catalog.status(c)
						action(c, "重新读取", func() { u.load(a, u.cursor) }).Disabled(disabled || u.catalog.busy).AlignSelf(ui.Start)
					case u.catalog.busy:
						mobileMessage(c, "正在读取会话…")
					case u.cursor != "":
						action(c, "更多会话", func() { u.load(a, u.cursor) }).Disabled(disabled).AlignSelf(ui.Start)
					case !live:
						mobileMessage(c, "连接所属设备并确认 Agent 支持导入后再试。")
					default:
						mobileMessage(c, "没有匹配的会话")
					}
				})
				return
			}
			item := shown[index]
			row := c.Key(item.SourceRef)
			mobileContent(row, contentMaxWidth).PaddingX(pageInset).Children(func() { u.row(a, row, item, disabled) })
		}).Grow(1).Basis(0).MinHeight(0)
		mobilePageFooter(c, func() {
			if u.message != "" {
				mobileMessage(c, u.message)
			}
			name := "导入所选会话"
			switch {
			case u.busy:
				name = "正在导入…"
			case len(u.selected) != 0 && len(u.selected) == len(u.failures):
				name = fmt.Sprintf("重试 %d 个", len(u.selected))
			case len(u.selected) != 0:
				name = fmt.Sprintf("导入 %d 个", len(u.selected))
			}
			primaryAction(c, name, func() { u.begin(a) }).Disabled(disabled || u.catalog.busy || len(u.selected) == 0 || a.preview)
		})
	})
}

var importSquare = mobileSymbol(`<rect x="4" y="4" width="16" height="16" rx="3"/>`)
var importChecked = mobileSymbol(`<rect x="4" y="4" width="16" height="16" rx="3"/><path d="m8 12 3 3 5-6"/>`)

func (u *mobileImportPicker) row(a *mobileApp, c *ui.Context, item domain.ConversationCandidate, disabled bool) {
	subtitle := item.Directory
	if !item.UpdatedAt.IsZero() {
		subtitle = item.UpdatedAt.Local().Format("1月2日") + " · " + subtitle
	}
	if failure := u.failures[item.SourceRef]; failure != "" {
		subtitle = failure
	}
	rowDivider(ui.Row(c).AlignItems(ui.Center), palette(c).line).Children(func() {
		if item.ImportedSessionID == "" {
			glyph := importSquare
			if u.selected[item.SourceRef] {
				glyph = importChecked
			}
			iconButton(c.Key("select"), "选择会话："+item.Title, glyph, 18, func() { u.toggle(item) }).
				AccessSelected(u.selected[item.SourceRef]).Disabled(disabled)
		} else {
			ui.Box(c).Size(mobileTapSize(c), mobileTapSize(c)).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
				ui.Icon(c, checkSymbol).Size(16, 16).TextColor(palette(c).secondary).PassThrough()
			})
		}
		mobileDetailRow(c.Key("open"), item.Title, subtitle, item.Title, nil,
			func() { u.preview(a, item) }, func() {
				if item.ImportedSessionID != "" {
					oneLine(c, "已导入", captionTextSize, palette(c).secondary)
				}
			}).Grow(1).MinWidth(0).Disabled(disabled)
	})
}

func (u *mobileImportPicker) toggle(item domain.ConversationCandidate) {
	if u.selected[item.SourceRef] {
		delete(u.selected, item.SourceRef)
		delete(u.failures, item.SourceRef)
	} else {
		u.selected[item.SourceRef] = true
	}
}

func (u *mobileImportPicker) preview(a *mobileApp, item domain.ConversationCandidate) {
	if !u.valid(a) || u.busy {
		return
	}
	var page *mygo.Window
	var result *domain.ConversationPreview
	var req mobileRequest
	ctx, cancel := context.WithCancel(context.Background())
	load := func() {
		q := u.query(item)
		epoch := a.model.ConnectionEpoch
		a.runPageJob(ctx, page, &req, "conversation.preview", q, nil, decodePage(func(p domain.ConversationPreview) error {
			if !u.valid(a) || a.model.ConnectionEpoch != epoch || u.harness != q.HarnessID ||
				p.Candidate.SourceRef != q.SourceRef || p.Candidate.HarnessID != q.HarnessID || p.Candidate.Directory != q.Directory ||
				len(p.Items) > 200 {
				return fmt.Errorf("导入来源已变化，请重试")
			}
			result = &p
			return nil
		}))
	}
	if a.preview {
		result = &domain.ConversationPreview{Candidate: item,
			Items: []domain.Item{{ID: "prompt", Kind: "user", Text: "核对之前的连接配置。"},
				{ID: "reply", Kind: "assistant", Text: "已找到这台设备上的连接记录。"}}}
	}
	page = a.page(item.Title, func(c *ui.Context) {
		mobilePageWithFooter(c, func() {
			if result == nil {
				req.status(c)
				if req.err != "" {
					action(c, "重新读取", load).AlignSelf(ui.Start).Disabled(!u.valid(a))
				}
				return
			}
			for index, message := range result.Items {
				a.importPreviewItem(c.Key(index), message)
			}
			if len(result.Items) == 0 {
				mobileMessage(c, "没有可预览的消息")
			}
		}, func() {
			if result != nil {
				note, detail := "可接续", result.Reason
				if result.ResumeRef == "" {
					note = "仅查看"
				}
				if result.Truncated && a.model.WorkerMinor >= importCompletionMinor {
					note, detail = "导入时完整读取", "这里只显示部分历史。导入时会核对能否继续对话。"
				}
				remoteStatus(c, infoSymbol, note, detail)
			}
			if item.ImportedSessionID != "" {
				primaryAction(c, "打开已导入会话", func() { a.openSessionFrom(page, item.ImportedSessionID) }).
					Disabled(a.sessionByID(item.ImportedSessionID).ID == "")
				return
			}
			selection := "选择此会话"
			if u.selected[item.SourceRef] {
				selection = "取消选择"
			}
			primaryAction(c, selection, func() {
				if u.valid(a) {
					u.toggle(item)
					u.window.Invalidate()
					page.Pop()
				}
			}).Disabled(result == nil || !u.valid(a))
		})
	})
	page.OnClosed(cancel)
	if !a.preview {
		load()
	}
	a.pushNative(u.window, page)
}

func (a *mobileApp) importPreviewItem(c *ui.Context, item domain.Item) {
	p := palette(c)
	ui.Column(c).FillWidth().Gap(4).PaddingY(7).Children(func() {
		switch item.Kind {
		case "user":
			mobileUserBubble(c).Children(func() {
				label(c, sentMessageText(mobileclient.SentMessageRuns(item)), bodyTextSize, p.ink).Selectable()
			})
		case "assistant":
			if item.Text != "" {
				markdown.View(c, item.Text, &markdown.Options{WrapCode: true,
					Theme: &markdown.Theme{FontSize: bodyTextSize * c.Preferences().TextScale, CodeFont: a.codeFont()}}).Selectable()
			}
		default:
			if item.Name != "" {
				label(c, item.Name, controlTextSize, p.ink).FontWeight(560).Selectable()
			}
			if item.Text != "" {
				if item.Presentation == "command" {
					toolResultText(c, item.Text, nil)
				} else {
					label(c, item.Text, controlTextSize, p.secondary).Selectable()
				}
			}
		}
		// References in a preview are source metadata. Their bytes and scope
		// belong to the imported session, once the worker admits it.
		for index, part := range item.Parts {
			row := c.Key(index)
			if part.Attachment != nil {
				mobileDetailRow(row, part.Attachment.Name, part.Label, "", fileCodeSymbol, nil, nil)
				continue
			}
			if part.Label != "" {
				label(row, part.Label, captionTextSize, p.secondary)
			}
			switch part.Kind {
			case "markdown":
				markdown.View(row, part.Text, &markdown.Options{WrapCode: true,
					Theme: &markdown.Theme{CodeFont: a.codeFont()}}).Selectable()
			case "code":
				a.codeResult(row, part.Text)
			default:
				label(row, part.Text, controlTextSize, p.ink).Selectable()
			}
		}
		for index, file := range item.Attachments {
			mobileDetailRow(c.Key("attachment").Key(index), file.Name, "", "", fileCodeSymbol, nil, nil)
		}
	})
}

func (u *mobileImportPicker) begin(a *mobileApp) {
	if !u.valid(a) || u.busy || u.catalog.busy || a.controller == nil {
		return
	}
	u.batch = nil
	for _, item := range u.items {
		if u.selected[item.SourceRef] && item.ImportedSessionID == "" {
			u.batch = append(u.batch, item)
		}
	}
	if len(u.batch) == 0 {
		return
	}
	u.batchEpoch, u.message = a.model.ConnectionEpoch, ""
	u.busy = true
	clear(u.failures)
	u.importNext(a)
}

func (u *mobileImportPicker) importNext(a *mobileApp) {
	defer u.window.Invalidate()
	if !u.valid(a) || a.model.ConnectionEpoch != u.batchEpoch {
		u.stopBatch()
		return
	}
	if len(u.batch) == 0 {
		u.busy = false
		if len(u.failures) != 0 {
			u.message = fmt.Sprintf("已导入 %d 个，%d 个未能完成", u.imported, len(u.failures))
		} else {
			u.message = fmt.Sprintf("已导入 %d 个会话", u.imported)
		}
		return
	}
	item := u.batch[0]
	u.batch = u.batch[1:]
	cfg := domain.RunConfig{}
	h, _ := a.model.Readiness.ForHarness(u.harness)
	for _, model := range h.Models {
		if cfg.Model == "" || model.Default {
			cfg.Model, cfg.Effort = model.ID, model.DefaultEffort
		}
	}
	if slices.Contains(h.Permissions, "default") {
		cfg.Permission = "default"
	} else if len(h.Permissions) > 0 {
		cfg.Permission = h.Permissions[0]
	}
	controller := a.controller
	epoch := u.batchEpoch
	in := protocol.ConversationImportInput{ConversationQuery: u.query(item), Config: cfg}
	if !controller.Do(app.Job{ConnectionEpoch: epoch, Method: "conversation.import", Input: in,
		Apply: func(m *app.Model, b json.RawMessage, err error) {
			if !a.pageCurrent(u.window) || m != u.model || a.controller != controller || !u.busy || u.batchEpoch != epoch {
				return
			}
			if !u.valid(a) || a.model.ConnectionEpoch != epoch {
				u.importNext(a)
				return
			}
			var session domain.Session
			if err == nil && (json.Unmarshal(b, &session) != nil || session.ID == "" || session.WorkspaceID != u.workspace.ID ||
				session.HarnessID != u.harness || (session.Origin == nil || session.Origin.SourceRef != item.SourceRef) && session.ThreadID != item.SourceRef) {
				err = fmt.Errorf("设备返回了无效的导入回执")
			}
			if err != nil {
				u.failures[item.SourceRef] = err.Error()
			} else {
				u.imported++
				delete(u.selected, item.SourceRef)
				for i := range u.items {
					if u.items[i].SourceRef == item.SourceRef {
						u.items[i].ImportedSessionID = session.ID
					}
				}
			}
			u.importNext(a)
		}}) {
		u.busy, u.message = false, "设备忙，请重试未完成的会话。"
	}
}

func (u *mobileImportPicker) stopBatch() {
	u.batch = nil
	u.busy, u.message = false, "连接或工作区已变化，请核对导入结果。"
}
