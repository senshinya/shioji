package ui

import n "github.com/egoist/mygo/ui"

type menuChoice struct {
	id, name, detail  string
	checked, disabled bool
	choose            func()
}

// Shared native popup density and neutral keyboard focus match menus.css.
func (v *View) choiceMenu(c *n.Context, t *n.Theme, key string, trigger n.Element, choices []menuChoice) {
	if v.menuOpen == nil {
		v.menuOpen = map[string]bool{}
	}
	open := v.menuOpen[key]
	if trigger.Clicked() {
		open = !open
	}
	popover(c.Key("choices:"+key), trigger, &open, func(panel n.Element) {
		_, height := c.Size()
		panel.Width(264).Padding(3).Radius(9).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			n.Scroll(c).FillWidth().MaxHeight(min(360, height-32)).Label("菜单选项").Children(func() {
				n.Column(c).FillWidth().FocusGroup(n.Vertical).Children(func() {
					first := true
					for _, choice := range choices {
						if choice.disabled {
							n.Text(c, choice.name).FillWidth().MinHeight(26).Padding(4, 7).FontSize(12).TextColor(t.TextMuted)
							continue
						}
						row := control(c.Key(choice.id), 4).FillWidth().MinHeight(26).PaddingX(7).Radius(4).Label(choice.name).Disabled(choice.disabled)
						if row.FocusVisible() {
							row.Background(t.SurfaceHover)
						}
						if first {
							row.AutoFocus()
							first = false
						}
						row.Children(func() {
							n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
								name := n.Text(c, choice.name).FontSize(13).FixedLineHeight(18)
								if choice.detail != "" {
									name.Width(96)
									n.Text(c, choice.detail).FontSize(11).TextColor(t.TextMuted).Grow(1)
								} else {
									name.Grow(1)
								}
								inlineSymbol(c, check, choice.name, n.Font{Family: "system-ui", Size: 13, Weight: 400}, 12, t.Text).Opacity(func() float32 {
									if choice.checked {
										return 1
									}
									return 0
								}())
							})
						})
						if row.Clicked() {
							if choice.choose != nil {
								choice.choose()
							}
							open = false
							trigger.Focus()
						}
					}
				})
			})
		})
	})
	v.menuOpen[key] = open
}
