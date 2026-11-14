package ui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
)

// appUpdate renders the updater's real session with the same native controls,
// typography and neutral surfaces as settings and device configuration.
type appUpdate struct {
	model            *app.Model
	session          updater.Session
	prompt, answered int
	automatic        bool
	preview          markdown.Options
	fitted, release  bool
}

func newAppUpdate(model *app.Model, session updater.Session) *appUpdate {
	return &appUpdate{model: model, session: session, prompt: -1, answered: -1}
}

func OpenAppUpdate(model *app.Model, session updater.Session) {
	mygo.RunOnMain(func() {
		w := newAppUpdate(model, session)
		_, changed := session.Current()
		win := session.NewWindow(n.View(w.View))
		// NewWindow owns cancellation, sizing and close behavior. Closing the
		// window cancels the current network operation through that session.
		go func() {
			for {
				select {
				case <-session.Done():
					return
				case <-changed:
					_, changed = session.Current()
					win.Invalidate()
				}
			}
		}()
		if session.User() || mygo.App.IsActive() {
			win.Show()
			win.Focus()
		} else {
			win.ShowInactive()
		}
	})
}

func (w *appUpdate) respond(v updater.Snapshot, b updater.Button) {
	if b.Disabled || w.answered == v.Prompt {
		return
	}
	w.answered = v.Prompt
	w.session.Respond(v.Prompt, b.Action, w.automatic)
}

func (w *appUpdate) View(c *n.Context) {
	v, _ := w.session.Current()
	if w.prompt != v.Prompt {
		w.prompt, w.automatic = v.Prompt, v.Checked
	}
	p := w.model.Preferences.Normalized()
	c.ReduceMotion(p.ReduceMotion)
	t := applyAppearance(c, p)
	for _, b := range v.Buttons {
		if b.Cancel && c.Shortcut(0, n.KeyEscape) || b.Default && c.Shortcut(0, n.KeyEnter) {
			w.respond(v, b)
		}
	}
	if !w.fitted || w.release != v.Release {
		if v.Release {
			w.session.Fit(620, 440)
		} else {
			w.session.Fit(520, 220)
		}
		w.fitted, w.release = true, v.Release
	}
	n.Column(c).Fill().Background(t.Background).Padding(configurationDialogInset).Gap(16).Children(func() {
		n.Scroll(c.Key("update-content")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() {
				n.Text(c, v.Title).FillWidth().FontSize(configurationTitleSize).FontWeight(400).FixedLineHeight(configurationTitleLine)
				if v.Message != "" {
					n.Text(c, v.Message).FillWidth().FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted)
				}
				if v.Bar {
					n.Progress(c, v.Progress).FillWidth().Height(4).Margin(8, 0)
				}
				if v.Detail != "" {
					n.Text(c, v.Detail).FillWidth().FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted).Selectable()
				}
				if v.NotesMarkdown != "" {
					n.Column(c).FillWidth().Height(.5).Background(t.Border).Margin(8, 0)
					w.preview.Theme = replyTheme(p)
					markdown.View(c, v.NotesMarkdown, &w.preview).FillWidth()
				}
			})
		})
		if v.Checkbox {
			configurationCheckbox(c, &w.automatic, "自动下载更新", n.Font{Family: t.Font, Size: configurationValueSize})
		}
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			buttons := func(aside bool) {
				for _, b := range v.Buttons {
					if b.Aside != aside {
						continue
					}
					e := configurationAction(c.Key(string(b.Action)), b.Label).Label("App 更新：" + b.Label).Disabled(b.Disabled || w.answered == v.Prompt).MinWidth(64).Justify(n.Center)
					if b.Default {
						e.Background(t.Accent).TextColor(t.AccentText).PaddingX(12)
					}
					if e.Clicked() {
						w.respond(v, b)
					}
				}
			}
			buttons(true)
			n.Spacer(c).Grow(1)
			buttons(false)
		})
	})
}
