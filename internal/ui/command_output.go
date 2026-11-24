package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func commandItem(item domain.Item) bool {
	return item.Kind == "tool" && (item.Presentation == "command" || item.Presentation == "")
}

func commandPreview(command string) string { return strings.Join(strings.Fields(command), " ") }

func commandCopyMenu(c *n.Context, command string) func(*n.Menu) {
	return func(menu *n.Menu) {
		if menu.Item("复制命令").Disabled(command == "").Chosen() {
			c.WriteClipboard(command)
		}
	}
}

func (v *View) commandOutputKey(id, work string) string {
	return fmt.Sprintf("command-output:%q:%q:%q:%q", v.Model.Snapshot.DeviceID, v.Model.SessionID, work, id)
}

// A new command closes every older output once. Later stdout and renders
// preserve explicit inspection; every newly seen command starts folded.
func (v *View) syncCommandOutputs(ids []string, previous *string, work string) {
	latest := ""
	if len(ids) > 0 {
		latest = ids[len(ids)-1]
	}
	changed := latest != *previous
	for _, id := range ids {
		key := v.commandOutputKey(id, work)
		if _, exists := v.Model.Collapsed[key]; changed || !exists {
			v.Model.Collapsed[key] = true
		}
	}
	*previous = latest
}

type commandOutputAppend struct {
	start, end int
	at         time.Time
}

type commandOutputState struct {
	textHandle        n.Handle
	scroll            n.ScrollState
	text              string
	open              bool
	following, placed bool
	target            float32
	appends           []commandOutputAppend
}

func (v *View) commandOutput(c *n.Context, t *n.Theme, item domain.Item, heading n.Element) {
	const height = 160
	display := strings.TrimRight(item.Text, "\r\n")
	key := v.commandOutputKey(item.ID, "")
	// The heading exists before stdout does. Preserve the running state there,
	// including a short command whose start/completion share one UI frame.
	state := n.Local(heading, "command-output", func() commandOutputState {
		arriving := v.toolArrivals != nil && v.toolArrivals.motions[item.ID] != nil
		return commandOutputState{text: display, following: arriving, open: item.Text != "" && !v.Model.Collapsed[key]}
	})
	live := !item.Final && (item.Status == "running" || item.Status == "starting")
	if live {
		state.following = true
	}
	follow := state.following // Finish laying out/scrolling the final batch too.
	open := item.Text != "" && !v.Model.Collapsed[key]
	wasOpen := state.open
	state.open = open
	if !open {
		state.appends = nil
		if v.Model.Collapsed[key] {
			state.following = false
		}
		display = state.text
	}
	frame := n.Column(c.Key("command-output-frame:" + item.ID)).FillWidth().Clip()
	// Own the spacing inside the animated frame too, so a closed command
	// leaves just its heading without a last-frame gap jump.
	v.transcriptContentHeight(c, frame, open, open, 160*time.Millisecond, func() float32 {
		n.Column(c).FillWidth().Padding(8, 0, 0, 0).Children(func() {
			output := n.Scroll(c.Key("output:" + item.ID)).FillWidth().MaxHeight(height)
			if display != state.text {
				if open && wasOpen && follow && strings.HasPrefix(display, state.text) && !c.Preferences().ReduceMotion {
					state.appends = append(state.appends, commandOutputAppend{len(state.text), len(display), c.Now()})
				} else {
					state.appends = nil
				}
				state.text = display
			}
			// Fade only bytes that actually arrived; earlier output keeps its ink and
			// baseline. Completed fragments merge back into the ordinary prefix.
			spans := []n.Span{}
			from := 0
			active := state.appends[:0]
			for _, addition := range state.appends {
				progress := min(1, float32(c.Now().Sub(addition.at))/float32(180*time.Millisecond))
				if progress >= 1 || c.Preferences().ReduceMotion {
					continue
				}
				active = append(active, addition)
				if from < addition.start {
					spans = append(spans, n.Span{Text: display[from:addition.start]})
				}
				spans = append(spans, n.Span{Text: display[addition.start:addition.end], Color: t.TextMuted.Alpha(n.EaseOut(max(0, progress)))})
				from = addition.end
				c.AnimationFrame()
			}
			state.appends = active
			if from < len(display) {
				spans = append(spans, n.Span{Text: display[from:]})
			}
			output.TrackScroll(&state.scroll).Children(func() {
				text := n.RichText(c, spans...).Bind(&state.textHandle).FillWidth().Padding(8, 10).Background(codeSurface(t)).Radius(6).Font(v.Model.Preferences.CodeFont).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
				v.decoratePlainFind(c, text, item, &state.scroll)
			})
			animation := motionAnimation(c, 160*time.Millisecond)
			invalidate := c.Services().Invalidate
			output.DrawOver(func(p *n.Painter, r n.Rect) {
				if !open || !follow {
					return
				}
				text := c.Resolve(state.textHandle)
				if !text.Valid() {
					return
				}
				// The viewport is still opening. Target its eventual capped height,
				// rather than scrolling old lines away to fit each smaller animation
				// frame and then bringing them back as the panel grows.
				contentHeight := text.LayoutBounds().H
				target := max(0, contentHeight-height)
				if !state.placed || math.Abs(float64(target-state.target)) > .5 || !state.scroll.Scrolling() && math.Abs(float64(target-state.scroll.Y)) > .5 {
					state.placed, state.target = true, target
					if target == 0 {
						state.scroll = n.ScrollState{Handle: state.scroll.Handle, X: state.scroll.X, Y: 0, MaxX: state.scroll.MaxX, MaxY: state.scroll.MaxY}
					} else {
						state.scroll.ScrollTo(state.scroll.X, target, animation)
					}
					invalidate()
					return
				}
				if !live && !state.scroll.Scrolling() && r.H >= min(height, contentHeight)-.5 {
					state.following = false
				}
			})
		})
		return 0
	})
}
