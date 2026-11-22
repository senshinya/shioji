package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type reviewRefPicker struct {
	base  bool
	query string
	list  ui.ListState
	rows  []reviewRefRow
}

type reviewRefRow struct {
	heading string
	ref     domain.DiffEndpoint
}

func (p *reviewRefPicker) filter(refs []domain.DiffEndpoint) {
	needle := strings.ToLower(strings.TrimSpace(p.query))
	var groups [3][]domain.DiffEndpoint
	for _, ref := range refs {
		if !strings.Contains(strings.ToLower(comparisonEndpointName(ref)+" "+ref.Branch), needle) {
			continue
		}
		switch ref.Kind {
		case "head", "worktree", "empty":
			groups[0] = append(groups[0], ref)
		case "branch":
			groups[1] = append(groups[1], ref)
		case "remote":
			groups[2] = append(groups[2], ref)
		}
	}
	p.rows = p.rows[:0]
	for i, title := range []string{"当前仓库", "本地分支", "远程分支"} {
		if len(groups[i]) == 0 {
			continue
		}
		p.rows = append(p.rows, reviewRefRow{heading: title})
		for _, ref := range groups[i] {
			p.rows = append(p.rows, reviewRefRow{ref: ref})
		}
	}
}

func defaultReviewRefs() []domain.DiffEndpoint {
	return []domain.DiffEndpoint{{Kind: "head"}, {Kind: "worktree"}, {Kind: "empty"}}
}

func (a *mobileApp) toggleReviewRefPicker(r *reviewState, base bool) {
	if r.refPicker != nil && r.refPicker.base == base {
		r.refPicker = nil
	} else {
		if len(r.refs) == 0 {
			r.refs = defaultReviewRefs()
		}
		r.refPicker = &reviewRefPicker{base: base}
		r.refPicker.filter(r.refs)
		a.loadReviewRefs(r)
	}
	a.refresh()
}

func (a *mobileApp) loadReviewRefs(r *reviewState) {
	accept := func(catalog domain.DiffRefCatalog) error {
		refs := defaultReviewRefs()
		for _, ref := range catalog.Refs {
			switch ref.Kind {
			case "head":
				refs[0] = ref
			case "worktree":
				refs[1] = ref
			case "branch", "remote":
				refs = append(refs, ref)
			}
		}
		r.refs = refs
		if p := r.refPicker; p != nil {
			p.filter(refs)
		}
		return nil
	}
	if a.preview {
		_ = accept(domain.DiffRefCatalog{Refs: []domain.DiffEndpoint{
			{Kind: "branch", Name: "main"}, {Kind: "branch", Name: "feat/reconnect"},
			{Kind: "remote", Name: "origin/main"},
		}})
		return
	}
	a.pageJob(r.parent, &r.rangeJob, "git.refs",
		protocol.GitQuery{WorkspaceID: r.session.WorkspaceID, SessionID: r.session.ID}, decodePage(accept))
}

func (a *mobileApp) reviewRefOptions(c *ui.Context, r *reviewState, picker *reviewRefPicker) {
	// Separate keys keep the native input from reusing the other endpoint's search.
	c = c.Key(picker.base)
	p := palette(c)
	syncSearch := func() {
		picker.list = ui.ListState{}
		picker.filter(r.refs)
	}
	close := func() {
		r.refPicker = nil
		a.refresh()
	}
	choose := func(ref domain.DiffEndpoint) {
		if r.refPicker != picker {
			return
		}
		next := r.requestedRange()
		if picker.base {
			next.Base = ref
		} else {
			next.Target = ref
		}
		r.refPicker = nil
		a.compareReview(r, unpinReviewRange(next))
		a.refresh()
	}
	comparison := unpinReviewRange(r.confirmedRange())
	selected := comparison.Target
	if picker.base {
		selected = comparison.Base
	}
	mobilePageContent(c, 0).Grow(1).Basis(0).MinHeight(0).Children(func() {
		search := c.Key("comparison-ref-search")
		ui.Row(search).Gap(4).Margin(0, 0, searchResultsGap).AlignItems(ui.Center).Children(func() {
			field := mobileSearchField(search, ui.Row(search).Grow(1).MinWidth(0), &picker.query, "搜索比较分支", "搜索分支", func() {
				if picker.query != "" {
					iconButton(search.Key("clear"), "清除搜索", closeSymbol, 13, func() {
						picker.query = ""
						syncSearch()
					}).KeepFocus()
				}
			}).Autocorrect(false).Capitalize(ui.CapitalizeNone).OnChange(syncSearch)
			field.OnTextKey(func(key ui.TextKey) bool {
				switch key.Key {
				case ui.KeyEscape:
					close()
					return true
				case ui.KeyEnter:
					if len(picker.rows) > 1 && key.Text() == picker.query {
						choose(picker.rows[1].ref)
					}
					return true
				}
				return false
			})
			action(search.Key("close"), "取消", close).Label("关闭分支选择").Shrink(0)
		})
		if r.rangeJob.err != "" {
			ui.Row(c.Key("refs-error")).AlignItems(ui.Center).Gap(8).Children(func() {
				label(c, r.rangeJob.err, captionTextSize, p.warning).Grow(1).MinWidth(0).Role(ui.RoleStatus)
				action(c, "重试", func() { a.loadReviewRefs(r) }).Shrink(0)
			})
		} else if r.rangeJob.busy {
			label(c.Key("refs-loading"), "正在读取分支…", captionTextSize, p.secondary).Role(ui.RoleStatus)
		}
		if len(picker.rows) == 0 && !r.rangeJob.busy && r.rangeJob.err == "" {
			label(c.Key("refs-empty"), "没有匹配的分支", controlTextSize, p.secondary).Padding(10, 0).Role(ui.RoleStatus)
		}
		picker.list.Key = func(i int) any {
			row := picker.rows[i]
			if row.heading != "" {
				return [2]string{"section", row.heading}
			}
			return [2]string{row.ref.Kind, row.ref.Name}
		}
		ui.List(c.Key("comparison-refs"), &picker.list, len(picker.rows), func(i int) {
			option := picker.rows[i]
			row := c.Key(picker.list.Key(i))
			if option.heading != "" {
				mobileSection(row, option.heading)
				return
			}
			ref := option.ref
			name := comparisonEndpointName(ref)
			checked := ref.Kind == selected.Kind && name == comparisonEndpointName(selected)
			mobileButton(row, name, func() { choose(ref) }).FillWidth().MinHeight(max(contentRowHeight, mobileTapSize(c))).
				Padding(5, 0).Gap(8).AlignItems(ui.Center).AccessSelected(checked).Children(func() {
				icon := branchSymbol
				if ref.Kind == "worktree" {
					icon = folderSymbol
				}
				alignedSymbol(row, icon, name, mobileStatusFont(row, bodyTextSize), inlineIconSize, p.secondary)
				label(row, name, bodyTextSize, p.ink).Grow(1).MinWidth(0).MaxLines(2).Ellipsis("…").PassThrough()
				if ref.Commit != "" {
					hash := ref.Commit[:min(7, len(ref.Commit))]
					oneLine(row, hash, captionTextSize, p.secondary).Font("monospace").Shrink(0).PassThrough()
				}
				if checked {
					alignedSymbol(row, checkSymbol, name, mobileStatusFont(row, bodyTextSize), inlineIconSize, p.ink)
				} else {
					ui.Spacer(row).Width(inlineIconSize).Shrink(0)
				}
			})
		}).Grow(1).Basis(0).MinHeight(0)
	})
}
