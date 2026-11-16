package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/diagram"
)

// At most two isolated render jobs, including their converters, across panes.
var mermaidSlots = make(chan struct{}, 2)

type mermaidEntry struct {
	image         *n.SVG
	err           string
	running, done bool
}
type mermaidCache struct {
	mu          sync.Mutex
	client      diagram.Client
	ctx         context.Context
	cancel      context.CancelFunc
	scope       string
	entries     map[string]*mermaidEntry
	bytes       int
	closed      bool
	heightDelta float32
}

func (m *mermaidCache) reset(scope string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.scope == scope && m.ctx != nil && !m.closed {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.scope, m.entries, m.bytes, m.closed = scope, map[string]*mermaidEntry{}, 0, false
	if m.client.Executable == "" {
		m.client = diagram.DefaultClient()
	}
}
func (m *mermaidCache) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	m.entries = nil
	m.bytes = 0
	m.closed = true
}
func (m *mermaidCache) request(c *n.Context, source string, dark bool) (*n.SVG, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, "", false
	}
	entry := m.entries[source]
	if entry == nil {
		if len(m.entries) >= 32 {
			return nil, "图表数量超出本次预览范围", true
		}
		entry = &mermaidEntry{}
		m.entries[source] = entry
	}
	if entry.done {
		return entry.image, entry.err, true
	}
	if entry.running {
		return nil, "", false
	}
	select {
	case mermaidSlots <- struct{}{}:
	default:
		c.After(50 * time.Millisecond)
		return nil, "", false
	}
	entry.running = true
	ctx, client, services := m.ctx, m.client, c.Services()
	go func() {
		defer func() { <-mermaidSlots }()
		data, err := client.Render(ctx, diagram.Request{Source: source, Dark: dark})
		var image *n.SVG
		if err == nil {
			image, err = n.ParseSVG(data)
		}
		m.mu.Lock()
		if ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		if err == nil && m.bytes+len(data) > 16<<20 {
			err = fmt.Errorf("图表缓存超出本次预览范围")
			image = nil
		}
		if err == nil {
			m.bytes += len(data)
		}
		entry.image, entry.done, entry.running = image, true, false
		if err != nil {
			entry.err = err.Error()
		}
		m.mu.Unlock()
		services.Invalidate()
	}()
	return nil, "", false
}

func bindMermaid(c *n.Context, options *markdown.Options, cache **mermaidCache, scope string, compact bool) {
	if *cache == nil {
		*cache = &mermaidCache{}
	}
	renderer := *cache
	renderer.reset(fmt.Sprintf("%s|%t", scope, c.Theme().Dark))
	if options.Code == nil {
		options.Code = map[string]func(*n.Context, *markdown.CodeBlockView){}
	}
	options.Code["mermaid"] = func(c *n.Context, b *markdown.CodeBlockView) { mermaidCodeBlock(c, b, renderer, compact) }
}

func mermaidCodeBlock(c *n.Context, b *markdown.CodeBlockView, cache *mermaidCache, compact bool) {
	// The streaming parser may synthesize a complete-looking suffix. Wait for
	// its real closing fence rather than rendering each incomplete delta.
	if b.Open {
		markdown.DefaultCodeBlock(c, b)
		return
	}
	t := c.Theme()
	source := b.Code()
	image, message, done := cache.request(c, source, t.Dark)
	root := n.Column(c.Key("mermaid")).FillWidth().Background(raised(t)).Border(1, t.Border).Radius(t.Radius + 2).Clip().Role(n.RoleGroup).Label("Mermaid")
	if compact {
		// Conversation diagrams keep their natural width, with enough room for
		// the header controls, and never exceed the message's available width.
		width := float32(224)
		if image != nil {
			w, _ := image.Size()
			width = max(width, w+26) // Diagram padding and the card's border.
		}
		root.Width(width).MaxWidthPercent(100).AlignSelf(n.Start)
	}
	showSource := n.Local(root, "source", func() bool { return false })
	root.Children(func() {
		n.Row(c.Key("header")).FillWidth().AlignItems(n.Center).Padding(3, 6, 3, 10).Gap(4).Children(func() {
			n.Text(c, "Mermaid").FontSize(11).TextColor(t.TextMuted).Unselectable()
			n.Spacer(c)
			label, accessible := "源码", "查看 Mermaid 源码"
			if *showSource {
				label, accessible = "预览", "查看 Mermaid 图表"
			}
			toggle := n.ButtonBase(c).Height(24).Padding(0, 7).Radius(5).Label(accessible)
			if toggle.Hovered() {
				toggle.Background(t.Text.Alpha(0.06))
			}
			toggle.Children(func() { n.Text(c, label).FontSize(11).TextColor(t.TextMuted) })
			if toggle.Clicked() {
				*showSource = !*showSource
			}
			b.CopyButton(c)
		})
		body := n.Column(c.Key("body")).FillWidth().Clip()
		cache.heightDelta += animatedContentHeightAtMount(c, body, true, true, 180*time.Millisecond, func() float32 {
			if *showSource || message != "" {
				if message != "" {
					n.Text(c, "无法渲染图表，已显示源码").FontSize(11).TextColor(t.TextMuted).Padding(4, 10).Tooltip(message)
				}
				b.Body(c)
				return 0
			}
			if !done || image == nil {
				n.Row(c).FillWidth().Height(96).AlignItems(n.Center).Justify(n.Center).Children(func() { n.Text(c, "正在渲染…").FontSize(12).TextColor(t.TextMuted) })
				return 0
			}
			width, height := image.Size()
			// A large graph scrolls within its own bounded viewport; text remains
			// vector-sharp and the conversation's normal scrollport stays independent.
			n.Scroll(c.Key("diagram-scroll")).FillWidth().Height(min(height+24, 480)).Label("Mermaid 图表滚动区域").Children(func() {
				n.Column(c).FillWidth().AlignItems(n.Center).Padding(12).Children(func() {
					n.Image(c, image).MaxWidthPercent(100).AspectRatio(width / height).Label("Mermaid 流程图")
				})
			})
			return 0
		})
	})
}
