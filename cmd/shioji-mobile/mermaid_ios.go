package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/diagram"
)

var mobileDiagramSlots = make(chan struct{}, 2)

type mobileDiagramScope struct {
	device                         uint64
	epoch, session, parallel, file string
	dark                           bool
}

type mobileDiagramCache struct {
	scope   mobileDiagramScope
	ctx     context.Context
	cancel  context.CancelFunc
	entries map[string]*mobileDiagramResult
	bytes   int
}

type mobileDiagramResult struct {
	image *ui.SVG
	err   string
}

// MyGo closes this local when its window or file inspector goes away.
func (m *mobileDiagramCache) Close() error {
	if m.cancel != nil {
		m.cancel()
		m.ctx, m.cancel = nil, nil
	}
	m.entries, m.bytes = nil, 0
	return nil
}

func (m *mobileDiagramCache) reset(scope mobileDiagramScope) {
	if m.ctx != nil && m.scope == scope {
		return
	}
	m.Close()
	m.scope = scope
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.entries = make(map[string]*mobileDiagramResult)
}

func (m *mobileDiagramCache) request(c *ui.Context, source string) mobileDiagramResult {
	if entry := m.entries[source]; entry != nil {
		return *entry
	}
	if len(source) > diagram.MaxSource || strings.TrimSpace(source) == "" {
		return mobileDiagramResult{err: "图表内容为空或超出渲染范围"}
	}
	if len(m.entries) >= 32 {
		return mobileDiagramResult{err: "图表数量超出本次预览范围"}
	}
	select {
	case mobileDiagramSlots <- struct{}{}:
	default:
		c.After(50 * time.Millisecond)
		return mobileDiagramResult{}
	}
	entry := &mobileDiagramResult{}
	m.entries[source] = entry
	ctx, services := m.ctx, c.Services()
	request := diagram.Request{Source: source, Dark: m.scope.dark}
	go func() {
		defer func() { <-mobileDiagramSlots }()
		data, err := diagram.RenderEmbedded(ctx, request)
		var image *ui.SVG
		if err == nil {
			image, err = ui.ParseSVG(data)
			if err == nil && len(image.Unsupported()) != 0 {
				err = fmt.Errorf("当前图表包含不支持的 SVG 样式：%s", strings.Join(image.Unsupported(), "、"))
			}
		}
		mygo.RunOnMain(func() {
			if ctx.Err() != nil {
				return
			}
			if err == nil && m.bytes+len(data) > 16<<20 {
				err = fmt.Errorf("图表缓存超出本次预览范围")
			}
			if err != nil {
				entry.err = err.Error()
			} else {
				entry.image = image
				m.bytes += len(data)
			}
			services.Invalidate()
		})
	}()
	return mobileDiagramResult{}
}

func (a *mobileApp) bindMobileDiagrams(c *ui.Context, owner ui.Element, file string, options *markdown.Options) {
	cache := ui.Local(owner, "mermaid-cache", func() mobileDiagramCache { return mobileDiagramCache{} })
	cache.reset(mobileDiagramScope{device: a.deviceSelection, epoch: a.model.ConnectionEpoch,
		session: a.model.SessionID, parallel: a.parallel, file: file, dark: c.Theme().Dark})
	if options.Code == nil {
		options.Code = make(map[string]func(*ui.Context, *markdown.CodeBlockView))
	}
	options.Code["mermaid"] = func(c *ui.Context, block *markdown.CodeBlockView) {
		mobileMermaidBlock(c, block, cache, file == "")
	}
}

func mobileMermaidBlock(c *ui.Context, block *markdown.CodeBlockView, cache *mobileDiagramCache, compact bool) {
	if block.Open {
		markdown.DefaultCodeBlock(c, block)
		return
	}
	result := cache.request(c, block.Code())
	p := palette(c)
	card := ui.Column(c.Key("mermaid")).FillWidth().MinWidth(0).Radius(cardRadius).
		Border(1, p.line).Background(p.inset).Clip().Role(ui.RoleGroup).Label("Mermaid")
	if compact {
		width := float32(224)
		if result.image != nil {
			naturalWidth, _ := result.image.Size()
			width = max(width, naturalWidth+26)
		}
		card.Width(width).MaxWidthPercent(100).AlignSelf(ui.Start)
	}
	source := ui.Local(card, "source", func() bool { return false })
	card.Children(func() {
		ui.Row(c.Key("controls")).AlignItems(ui.Center).Padding(0, 9, 0, 11).Gap(3).Children(func() {
			oneLine(c, "Mermaid", captionTextSize, p.secondary)
			ui.Spacer(c)
			if result.err == "" {
				title, accessible := "源码", "查看 Mermaid 源码"
				if *source {
					title, accessible = "预览", "查看 Mermaid 图表"
				}
				mobileButton(c, accessible, func() { *source = !*source }).AlignItems(ui.Center).Justify(ui.Center).
					PaddingX(7).Children(func() { oneLine(c, title, captionTextSize, p.secondary) })
			}
			block.CopyButton(c).MinHeight(mobileTapSize(c)).MinWidth(mobileTapSize(c)).AlignItems(ui.Center)
		})
		ui.Column(c.Key("body")).FillWidth().Clip().Transition(mobilePanelTransition).Children(func() {
			if result.err != "" {
				label(c, "无法渲染图表，已显示源码", captionTextSize, p.secondary).Padding(1, 11, 4).Value(result.err)
			}
			if *source || result.err != "" {
				block.Body(c)
				return
			}
			if result.image == nil {
				ui.Box(c).FillWidth().Height(74).Center().Children(func() {
					label(c, "正在渲染…", captionTextSize, p.secondary)
				})
				return
			}
			width, height := result.image.Size()
			showImage := func() {
				ui.Column(c).FillWidth().Padding(4, 12, 12).AlignItems(ui.Center).Children(func() {
					ui.Image(c, result.image).Width(width).MaxWidthPercent(100).AspectRatio(width / height).Label("Mermaid 图表")
				})
			}
			if height+16 > 440 {
				ui.Scroll(c.Key("diagram-scroll")).FillWidth().Height(440).Label("Mermaid 图表滚动区域").Children(showImage)
			} else {
				showImage()
			}
		})
	})
}
