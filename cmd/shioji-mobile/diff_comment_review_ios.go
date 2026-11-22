package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileDiffCommentReview struct {
	reference domain.Attachment
	content   domain.CodeCommentContent
	fragments []editor.DiffTextFragment
	rendered  [][]editor.DiffTextFragment
	active    int
	reveal    bool
	viewport  float64
	cancel    context.CancelFunc
	draft     *mobileDraftEditor
	draftPage *mygo.Window
	edit      *mobileDiffCommentEdit
}

type mobileDiffCommentEdit struct {
	text    string
	request mobileRequest
}

func (d *mobileDiffCommentReview) stop() {
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
}

func (a *mobileApp) openSavedDiffComment(parent *mygo.Window, source domain.Session, item domain.Attachment, draft *mobileDraftEditor) {
	if !a.visibleLinkSource(parent, source) {
		return
	}
	if a.review != nil && a.review.comment != nil && a.review.comment.edit != nil {
		return
	}
	comment := &mobileDiffCommentReview{reference: item, draft: draft, draftPage: parent}
	r := &reviewState{session: source, parent: parent, comment: comment}
	if a.wide && parent == a.detail {
		a.replaceReview(r)
	} else {
		r.parent = a.page("代码评论", func(c *ui.Context) { a.reviewContent(c, r, nil) })
		r.parent.OnClosed(comment.stop)
		a.pushNative(parent, r.parent)
	}
	a.loadSavedDiffComment(r)
	a.refresh()
}

func (a *mobileApp) loadSavedDiffComment(r *reviewState) {
	comment := r.comment
	m, owner := a.model, a.controller
	device := m.Snapshot.DeviceID
	if !m.Connected || owner == nil || a.directory == "" || device == "" ||
		r.session.Context.DeviceID != "" && r.session.Context.DeviceID != device {
		r.loading, r.err = false, "连接会话所属设备后再读取代码评论"
		a.refresh()
		return
	}
	if r.loading {
		return
	}
	epoch, generation := m.ConnectionEpoch, m.Snapshot.Generation
	target := app.RemoteAttachmentTarget(device, r.session.WorkspaceID, owner.DialDeviceStream)
	staging := attachments.New(filepath.Join(a.directory, "attachments"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	comment.cancel = cancel
	r.loading, r.err = true, ""
	go func() {
		defer cancel()
		record, versions, parts, err := downloadDiffComment(ctx, target, staging, comment.reference)
		mygo.RunOnMain(func() {
			if comment.cancel == nil || !a.pageCurrent(r.parent) || r.parent == a.detail && a.review != r {
				return
			}
			comment.cancel = nil
			r.loading = false
			switch {
			case a.model != m || a.controller != owner || m.ConnectionEpoch != epoch || m.Snapshot.Generation != generation:
				r.err = "设备连接已改变，请重试"
			case err != nil:
				r.err = err.Error()
			default:
				versions.Snapshot = record.Snapshot
				r.versions = versions
				r.diff = &editor.DiffState{
					Old: editor.NewBuffer(versions.Old), New: editor.NewBuffer(versions.New),
					Layout: editor.DiffUnified, Indicators: editor.DiffClassic,
					Wrap: true, ExpandUnchanged: true, TextSelection: true,
				}
				comment.content, comment.fragments, comment.reveal = record.Content, parts, true
			}
			a.refresh()
		})
	}()
}

func downloadDiffComment(ctx context.Context, target app.AttachmentTarget, staging *attachments.Repository, ref domain.Attachment) (
	record domain.DiffCommentRecord, versions domain.FileVersions, parts []editor.DiffTextFragment, err error,
) {
	read := func(a domain.Attachment, value any) error {
		path, err := target.Download(ctx, staging, a)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, value)
	}
	if err = read(ref, &record); err != nil {
		return
	}
	unavailable := errors.New("无法核对评论的原始代码快照")
	if record.Snapshot.Kind != domain.DiffSnapshotResource ||
		record.Content.FileSnapshot != "" && record.Content.FileSnapshot != record.Snapshot.SHA256 {
		err = unavailable
		return
	}
	if err = read(record.Snapshot, &versions); err != nil {
		return
	}
	if versions.Path == "" || record.Content.Path != versions.Path || record.Content.Comparison != versions.Comparison ||
		len(record.Content.Fragments) == 0 {
		err = unavailable
		return
	}
	for _, part := range record.Content.Fragments {
		side, text := editor.DiffNew, versions.New
		switch part.Side {
		case "base":
			side, text = editor.DiffOld, versions.Old
		case "target":
		default:
			err = unavailable
			return
		}
		if part.Start < 0 || part.End <= part.Start || part.End > len(text) || text[part.Start:part.End] != part.Text {
			err = unavailable
			return
		}
		parts = append(parts, editor.DiffTextFragment{Side: side, Start: part.Start, End: part.End})
	}
	return
}

func (a *mobileApp) savedDiffCommentView(c *ui.Context, r *reviewState, close func()) {
	p := palette(c)
	saved := r.comment
	ui.Column(c).Fill().MinWidth(0).Background(p.canvas).Children(func() {
		if close != nil {
			ui.Row(c).MinHeight(55).Padding(2, 8, 4).AlignItems(ui.Center).Children(func() {
				oneLine(c, "代码评论", conversationTitle, p.ink).Grow(1).PaddingX(8).FontWeight(650)
				iconButton(c, "关闭代码评论", closeSymbol, toolbarIconSize, close).Disabled(saved.edit != nil && saved.edit.request.busy)
			})
		}
		if r.diff == nil {
			ui.Column(c).Padding(8, pageInset).Children(func() {
				if r.err != "" {
					mobileMessage(c, r.err)
					action(c, "重试", func() { a.loadSavedDiffComment(r) }).AlignSelf(ui.Start)
				} else {
					mobileMessage(c, "正在读取代码评论…")
				}
			})
			return
		}
		ui.Column(c).Padding(pageTopInset, pageInset, 6).Gap(4).Shrink(0).Children(func() {
			oneLine(c, r.versions.Path, controlTextSize, p.ink).FontWeight(650)
			comparison := r.versions.Comparison
			label(c, savedDiffEndpointName(comparison.Base)+" → "+savedDiffEndpointName(comparison.Target), captionTextSize, p.secondary).
				Wrap().MaxLines(3).Ellipsis("…")
			if saved.edit != nil {
				a.savedDiffCommentEditor(c, r)
			} else {
				ui.Scroll(c.Key("comment-text")).MaxHeight(112).FillWidth().Children(func() {
					label(c, saved.content.Comment, controlTextSize, p.ink).Selectable().Wrap()
				})
				if saved.draft != nil {
					action(c, "编辑评论", func() {
						saved.edit = &mobileDiffCommentEdit{text: saved.content.Comment}
					}).AlignSelf(ui.Start)
				}
			}
			first := saved.content.Fragments[saved.active]
			title := savedDiffRangeName(first)
			pick := func(index int) {
				saved.active, saved.reveal = index, true
			}
			if len(saved.fragments) == 1 {
				secondaryAction(c, title, func() { pick(0) }).Label("评论选区").Value(title).AlignSelf(ui.Start).
					ContextMenu(func(m *ui.Menu) {
						if m.Item("复制当前选区").Chosen() {
							mygo.Clipboard.WriteText(first.Text)
						}
					})
			} else {
				iconAction(c, title, "评论选区", nil, 0, pickerSymbol, pickerIconSize, captionTextSize, 550, nil).
					Value(title).AlignSelf(ui.Start).Menu(func(m *ui.Menu) {
					for i, part := range saved.content.Fragments {
						menuChoice(m, savedDiffRangeName(part), saved.active == i, func() { pick(i) })
					}
					m.Separator()
					if m.Item("复制当前选区").Chosen() {
						mygo.Clipboard.WriteText(first.Text)
					}
				})
			}
		})
		a.styleMobileDiff(c, r.diff)
		r.diff.Theme.Modified = p.secondary
		if saved.rendered == nil && !r.diff.Comparing() {
			saved.rendered = make([][]editor.DiffTextFragment, len(saved.fragments))
			for i, part := range saved.fragments {
				saved.rendered[i] = r.diff.UnifiedTextFragments(part)
			}
		}
		r.diff.TextHighlights = r.diff.TextHighlights[:0]
		for _, fragments := range saved.rendered {
			for _, part := range fragments {
				r.diff.TextHighlights = append(r.diff.TextHighlights, editor.DiffTextHighlight{
					Side: part.Side, Start: part.Start, End: part.End, Color: p.ink.Alpha(.19),
				})
			}
		}
		height := r.diff.ViewportHeight()
		if height > 0 {
			if saved.edit != nil && saved.viewport-height > 1 {
				saved.reveal = true
			}
			saved.viewport = height
		}
		if saved.reveal && saved.rendered != nil {
			if height > 0 {
				part := saved.fragments[saved.active]
				onScreen := part
				if projected := saved.rendered[saved.active]; len(projected) != 0 {
					onScreen = projected[0]
				}
				y, ok := r.diff.TextFragmentScrollTarget(onScreen)
				if !ok {
					line := saved.content.Fragments[saved.active].StartLine
					y = r.diff.LineTop(part.Side, line-1) - height*.35
				}
				r.diff.SetScrollY(y)
				saved.reveal = false
			} else {
				c.AnimationFrame()
			}
		}
		ui.Column(c).Grow(1).Basis(0).MinHeight(0).Margin(0, pageInset, 12).Radius(cardRadius).Clip().Children(func() {
			editor.DiffView(c, r.diff).Fill().Label("历史差异：" + r.versions.Path)
		})
	})
}

func (a *mobileApp) savedDiffCommentEditor(c *ui.Context, r *reviewState) {
	saved, edit := r.comment, r.comment.edit
	p := palette(c)
	mobileTextArea(c.Key("comment-edit"), &edit.text, "评论内容").
		Height(76).Radius(8).Background(p.inset).Disabled(edit.request.busy)
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		secondaryAction(c, "取消", func() { saved.edit = nil }).
			Label("取消编辑评论").Disabled(edit.request.busy)
		primaryAction(c, "保存评论", func() { a.saveDraftDiffComment(c, r) }).
			Disabled(edit.request.busy || strings.TrimSpace(edit.text) == "" || edit.text == saved.content.Comment)
	})
	edit.request.status(c)
}

func (a *mobileApp) saveDraftDiffComment(c *ui.Context, r *reviewState) {
	saved, edit := r.comment, r.comment.edit
	draft := saved.draft
	if !draft.current(a, saved.draftPage) || draft.pending() {
		edit.request.err = "原草稿已改变，请返回草稿后重试"
		return
	}
	original, chip := saved.reference, draft.chipMaker(c)
	epoch, generation := a.model.ConnectionEpoch, a.model.Snapshot.Generation
	in := protocol.DiffCommentInput{
		GitQuery: protocol.GitQuery{WorkspaceID: r.session.WorkspaceID, SessionID: r.session.ID},
		Snapshot: r.versions.Snapshot, Comment: edit.text, Fragments: saved.content.Fragments,
	}
	a.pageJob(r.parent, &edit.request, "diff.comment", in, decodePage(func(updated domain.Attachment) error {
		if saved.edit != edit || r.parent == a.detail && a.review != r || !draft.current(a, saved.draftPage) ||
			draft.pending() || a.model.ConnectionEpoch != epoch || a.model.Snapshot.Generation != generation {
			return errors.New("原草稿已改变，请返回草稿后重试")
		}
		if updated.Kind != domain.DiffCommentResource || updated.ID == "" {
			return errors.New("设备未返回有效的评论")
		}
		if !draft.ReplaceAttachment(a.model, draft.target.key, original, updated, chip) {
			return errors.New("原草稿中已找不到这条评论")
		}
		saved.reference, saved.content.Comment, saved.edit = updated, in.Comment, nil
		a.save()
		a.refresh()
		return nil
	}))
}

func savedDiffEndpointName(endpoint domain.DiffEndpoint) string {
	if endpoint.Kind == "worktree" {
		return "保存时的工作目录"
	}
	name := comparisonEndpointName(endpoint)
	if endpoint.Commit != "" {
		name += " " + endpoint.Commit[:min(7, len(endpoint.Commit))]
	}
	return name
}

func savedDiffRangeName(part domain.CodeSelection) string {
	side := "目标"
	if part.Side == "base" {
		side = "基准"
	}
	if part.StartLine == part.EndLine {
		return fmt.Sprintf("%s · 第 %d 行 · %d–%d", side, part.StartLine, part.StartColumn, part.EndColumn)
	}
	return fmt.Sprintf("%s · 第 %d–%d 行", side, part.StartLine, part.EndLine)
}
