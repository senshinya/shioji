package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"time"
)

const configurationTabDuration = 260 * time.Millisecond

type configurationTabItem struct {
	ID, Label string
	Icon      *n.SVG
}
type configurationTabStyle uint8

const (
	configurationUnderline configurationTabStyle = iota
	configurationSidebar
	configurationSegmented
)

func configurationTabIndex(items []configurationTabItem, selected string) int {
	for i, item := range items {
		if item.ID == selected {
			return i
		}
	}
	return 0
}

// One tab list owns one moving selection, including native arrow-key navigation.
func configurationTabs(c *n.Context, selected string, items []configurationTabItem, style configurationTabStyle, disabled bool) string {
	index := configurationTabIndex(items, selected)
	parts := n.TabsBase(c, &index, len(items))
	list := parts.List.FillWidth()
	switch style {
	case configurationSidebar:
		list.Column().Gap(4).FocusGroup(n.Vertical)
	case configurationSegmented:
		list.Height(configurationControlHeight).Padding(2).Gap(2).Radius(5).Background(c.Theme().Text.Alpha(.045))
	default:
		list.Height(configurationTabHeight).Gap(configurationSectionGap).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border)
	}
	animation := n.Animation{Duration: configurationTabDuration, Ease: n.EaseOut}
	position := float32(configurationTabIndex(items, selected))
	if style != configurationSidebar {
		position = list.AnimateTo("selection-index", position, animation)
	}
	if c.Preferences().ReduceMotion {
		position = list.Jump("selection-index", float32(configurationTabIndex(items, selected)))
	}
	x, width := float32(0), float32(0)
	if style == configurationUnderline {
		for _, item := range items {
			w, _ := n.ShapeText(item.Label, n.Font{Family: c.Theme().Font, Size: configurationLabelSize, Weight: 400}, 0).Size()
			if item.ID == selected {
				width = w
				break
			}
			x += w + configurationSectionGap
		}
		if c.Preferences().ReduceMotion {
			x = list.Jump("selection-x", x)
			width = list.Jump("selection-width", width)
		} else {
			x = list.AnimateTo("selection-x", x, animation)
			width = list.AnimateTo("selection-width", width, animation)
		}
	}
	ink, selection, background, border := c.Theme().Text, c.Theme().Selection, c.Theme().Background, c.Theme().Border
	// Paint the shared marker before every label; absolute child overlays would
	// cover the selected text while the marker travels between controls.
	list.Draw(func(p *n.Painter, r n.Rect) {
		switch style {
		case configurationSidebar:
			p.Fill(n.Rect{X: r.X, Y: r.Y + position*36, W: r.W, H: 32}, selection, 6)
		case configurationSegmented:
			count := float32(len(items))
			w := (r.W - 4 - 2*(count-1)) / count
			rect := n.Rect{X: r.X + 2 + position*(w+2), Y: r.Y + 2, W: w, H: r.H - 4}
			p.Fill(rect, border, 4)
			p.Fill(n.Rect{X: rect.X + 1, Y: rect.Y + 1, W: rect.W - 2, H: rect.H - 2}, background, 3)
		default:
			p.Fill(n.Rect{X: r.X + x, Y: r.Y + r.H - 1, W: width, H: 1}, ink, 0)
		}
	})
	list.Children(func() {
		for i, item := range items {
			tab := parts.Tab(i).Label(item.Label).Disabled(disabled)
			on := item.ID == selected
			color := c.Theme().TextMuted
			if on {
				color = c.Theme().Text
			}
			switch style {
			case configurationSidebar:
				tab.FillWidth().Height(32).PaddingX(8).Radius(6)
			case configurationSegmented:
				tab.Grow(1).Height(24).Radius(4).Justify(n.Center)
			default:
				tab.Height(configurationTabHeight).PaddingX(8).MarginX(-8)
			}
			controlFeedback(c, tab, func() float32 {
				if style == configurationSidebar {
					return 6
				}
				if style == configurationSegmented {
					return 4
				}
				return 0
			}())
			tab.TextColor(color).Children(func() {
				if item.Icon != nil {
					inlineIconText(c, item.Icon, item.Label, n.Font{Family: c.Theme().Font, Size: configurationLabelSize, Weight: 400}, 16, 8, color)
				} else {
					size := float32(configurationLabelSize)
					if style == configurationSegmented {
						size = configurationValueSize
					}
					n.Text(c, item.Label).FontSize(size).FixedLineHeight(configurationTextLine).SingleLine()
				}
			})
		}
	})
	if list.Changed() && len(items) > 0 {
		c.Services().Invalidate()
		return items[index].ID
	}
	return selected
}

type configurationPanelLayout uint8

const (
	configurationPanelFill configurationPanelLayout = iota
	configurationPanelFit
)

type configurationPanelMotion struct {
	selected  string
	index     int
	direction float32
	changed   time.Time
	exits     map[string]*n.Motion
	resizing  bool
	height    float32
}

// Only the selected page is live; native exit snapshots cannot receive input.
// Each leaving page retains its own exit direction during rapid reversals.
func configurationTabPanel(c *n.Context, key string, index int, layout configurationPanelLayout, build func(), initialMotion ...bool) n.Element {
	frame := n.Column(c).FillWidth().MinWidth(0).MinHeight(0).Clip()
	if layout == configurationPanelFill {
		frame.FillHeight().Grow(1)
	}
	state := n.Local(frame, "tab-motion", func() configurationPanelMotion {
		return configurationPanelMotion{selected: key, index: index, direction: 1, exits: map[string]*n.Motion{}}
	})
	distance := frame.Bounds().W
	if distance == 0 {
		distance, _ = c.Size()
	}
	if state.selected != key {
		state.direction = 1
		if index < state.index {
			state.direction = -1
		}
		if previous := state.exits[state.selected]; previous != nil {
			previous.X = -state.direction * distance
		}
		state.selected, state.index, state.changed = key, index, c.Now()
		state.resizing = true
	}
	exit := state.exits[key]
	if exit == nil {
		exit = &n.Motion{Opacity: 1}
		state.exits[key] = exit
	}
	if c.Preferences().ReduceMotion {
		distance = 0
	}
	buildPage := func() {
		current := n.Column(c.Key(key)).FillWidth().MinWidth(0).MinHeight(0)
		if layout == configurationPanelFill {
			current.FillHeight().Grow(1)
		}
		enterDistance := distance
		if len(initialMotion) > 0 && !initialMotion[0] && state.changed.IsZero() {
			enterDistance = 0
		}
		current.Transition(n.ElementTransition{Position: true, Duration: configurationTabDuration, Ease: n.EaseOut, Enter: &n.Motion{X: state.direction * enterDistance, Opacity: 1}, Exit: exit})

		current.DrawOver(func(_ *n.Painter, r n.Rect) {
			if key == state.selected {
				if math.Abs(float64(state.height-r.H)) > .5 {
					state.height = r.H
					c.Services().Invalidate()
				}
			}
		})
		current.Children(build)
	}
	if layout == configurationPanelFit {
		if state.resizing && c.Now().Sub(state.changed) >= configurationTabDuration && math.Abs(float64(frame.Bounds().H-state.height)) < .5 {
			state.resizing = false
		}
		// When the selected page changes, animate the height between pages.
		// Inside one page, disclosures already animate their real layout height;
		// leave it natural so their following controls move in the same frame.
		if state.resizing {
			frame.Height(frame.AnimateTo("height", state.height, motionAnimation(c, configurationTabDuration)))
		} else {
			frame.Jump("height", state.height)
		}
		frame.Children(func() {
			content := n.Column(c.Key("natural-content")).FillWidth().Shrink(0)
			if state.resizing {
				content.Absolute().Left(0).Right(0).Top(0)
			}
			content.Children(buildPage)
		})
	} else {
		frame.Children(buildPage)
	}
	return frame
}
