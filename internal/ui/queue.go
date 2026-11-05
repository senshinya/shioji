package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

var queueAddIcon = svg("M3 5h11M3 10h6M3 15h6M14 10v8M10 14h8")
var steerMessageIcon = svg("M4 16v-6a3 3 0 0 1 3-3h9M12 3l4 4-4 4")
var queueEditIcon = svg("M13 3l4 4M4 13l-1 5 5-1L18 6l-4-4L4 13z")
var queueRemoveIcon = svg("M5 5l10 10M15 5L5 15")

func (v *View) queueDisclosure(c *n.Context, t *n.Theme) {
	m := v.Model
	sid := m.SessionID
	q := m.Queue()
	edit := m.QueueEdits[sid]
	if v.queueDrag != nil && (v.queueDrag.session != sid || v.queueDrag.epoch != m.ConnectionEpoch) {
		v.queueDrag = nil
	}
	held := false
	if sid == "" || len(q.Items) == 0 {
		v.queueDrag = nil
		return
	}
	bg := raised(t)
	if t.Dark {
		bg = t.Surface
	}
	entryMotion := disclosureMotion(c, 160*time.Millisecond)
	entryMotion.Size = false
	n.Column(c.Key("queued-turns:"+sid)).Label("待发送队列").FillWidth().Gap(0).Background(bg).Border(1, t.Text.Alpha(.10)).Radius(16, 16, 0, 0).Margin(0, 0, -1, 0).Clip().Transition(entryMotion).Children(func() {
		n.Row(c).FillWidth().Height(32).PaddingX(14).AlignItems(n.Center).Gap(8).Children(func() {
			label := fmt.Sprintf("待发送 %d", len(q.Items))
			trigger := n.ButtonBase(c.Key("queue-disclosure")).Height(32).Grow(1).MinWidth(0).Justify(n.Start).Label(label)
			color := textHoverColor(c, trigger, t.Text.Alpha(.86))
			trigger.Children(func() {
				n.Row(c).Gap(3).AlignItems(n.Center).Children(func() {
					angle := float32(-90)
					if m.QueueExpanded[sid] {
						angle = 0
					}
					n.Column(c).Size(16, 18).AlignItems(n.Center).Justify(n.Center).Children(func() {
						n.Icon(c, chevron).Size(14, 14).TextColor(subtle(t)).Transform(n.Rotation(trigger.AnimateTo("queue-chevron", angle, motionAnimation(c, 180*time.Millisecond))))
					})
					n.Text(c, "待发送").FontSize(12).FontWeight(500).FixedLineHeight(18).TextColor(color)
					n.Text(c, fmt.Sprint(len(q.Items))).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
				})
			})
			if trigger.Clicked() {
				m.QueueExpanded[sid] = !m.QueueExpanded[sid]
			}
			if q.Paused {
				n.Text(c, "已暂停").FontSize(11).TextColor(t.TextMuted).Tooltip(q.Reason)
				if ghost(c, "继续队列").Height(26).Disabled(!m.Connected || v.queueBusy[sid]).Clicked() {
					v.queueCommand("queue.continue", protocol.QueueInput{SessionID: sid, Revision: q.Revision}, nil)
				}
			}
		})
		_, windowHeight := c.Size()
		full := min(float32(len(q.Items))*32, min(float32(160), windowHeight*.22)) + 4
		target := float32(0)
		if m.QueueExpanded[sid] {
			target = full
		}
		body := n.Column(c.Key("queue-body")).FillWidth().Gap(0).Clip()
		height := body.AnimateTo("queue-height", target, motionAnimation(c, 160*time.Millisecond))
		opacity := min(float32(1), height/full)
		body.Height(height).Opacity(opacity).Children(func() {
			if height <= .1 {
				return
			}
			n.Column(c).Absolute().Left(0).Right(0).Top(0).Height(full).Gap(0).Children(func() {
				n.Scroll(c.Key("queue-scroll")).FillWidth().Height(full).Padding(0, 4, 4).Label("待发送消息列表").Children(func() {
					list := n.Column(c.Key("queue-rows")).FillWidth().Gap(0)
					items := v.previewQueueRows(c, list, q)
					list.Children(func() {
						for i, item := range items {
							i, item := i, item
							motion := n.ElementTransition{Position: !c.Preferences().ReduceMotion, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}}
							row := n.Row(c.Key(item.ID)).Transition(motion).FillWidth().Height(32).PaddingX(10).Gap(3).Radius(5).AlignItems(n.Center)
							held = v.queueRowGestures(c, row, q, item) || held
							hover := n.Transparent
							if row.Hovered() || row.FocusWithin() {
								hover = t.Text.Alpha(.04)
							}
							row.Background(row.AnimateColor("queue-row-hover", hover, motionAnimation(c, 120*time.Millisecond)))
							if row.Dragging() {
								row.Background(t.SurfaceHover).Shadow(0, 2, 6, 0, n.RGBA(0, 0, 0, .08))
							}
							row.Children(func() {
								n.Text(c, fmt.Sprint(i+1)).Width(16).TextAlign(n.Center).Font("Menlo").FontSize(11).FixedLineHeight(18).TextColor(subtle(t))
								text := strings.ReplaceAll(strings.TrimSpace(item.Input.Text), "\n", " ")
								n.Text(c, text).Grow(1).MinWidth(0).Lines(1, 1).Ellipsis("…").FontSize(14).FixedLineHeight(20).TextColor(t.Text.Alpha(.86)).Tooltip(item.Input.Text)
								if item.State != "" {
									n.Text(c, map[string]string{"dispatching": "发送中", "failed": "未发送", "unknown": "待核实"}[item.State]).FontSize(11).TextColor(subtle(t))
								}
								visible := float32(0)
								width, _ := c.Size()
								if row.Hovered() || row.FocusWithin() || width < 720 {
									visible = 1
								}
								n.Row(c).Gap(1).Opacity(row.AnimateTo("queue-actions", visible, motionAnimation(c, 120*time.Millisecond))).Children(func() {
									locked := !m.Connected || v.queueBusy[sid] || item.State == "dispatching" || item.State == "unknown"
									for _, action := range []struct {
										label    string
										icon     *n.SVG
										disabled bool
										run      func()
									}{
										{fmt.Sprintf("立即发送待发消息 %d", i+1), steerMessageIcon, locked || (m.Active() && m.ActiveTurn() == "") || item.State != "", func() {
											v.queueCommand("queue.send", protocol.QueueInput{SessionID: sid, ID: item.ID, Revision: q.Revision, TurnID: m.ActiveTurn()}, nil)
										}},
										{fmt.Sprintf("编辑待发消息 %d", i+1), queueEditIcon, locked || edit.ID != "" || m.HasPendingInput(), func() {
											v.editQueued(item.ID)
										}},
										{fmt.Sprintf("移除待发消息 %d", i+1), queueRemoveIcon, !m.Connected || v.queueBusy[sid] || item.State == "dispatching" || edit.ID == item.ID, func() {
											v.queueCommand("queue.remove", protocol.QueueInput{SessionID: sid, ID: item.ID, Revision: q.Revision}, nil)
										}},
									} {
										if control(c.Key(action.label), 5).Size(25, 26).Label(action.label).Disabled(action.disabled).Children(func() { n.Icon(c, action.icon).Size(12, 12) }).Clicked() {
											action.run()
										}
									}
								})
							})
						}
					})
				})
			})
		})
	})
	if !held && v.queueDrag != nil && !v.queueDrag.saving {
		v.queueDrag = nil
		c.AnimationFrame()
	}
}

func (v *View) queueEditBar(c *n.Context, t *n.Theme) {
	m := v.Model
	sid := m.SessionID
	if m.QueueEdits[sid].ID == "" {
		return
	}
	n.Row(c.Key("queue-edit-bar")).FillWidth().Padding(0, 0, 6, 10).Gap(5).AlignItems(n.Center).Children(func() {
		n.Icon(c, queueEditIcon).Size(12, 12).TextColor(t.TextMuted)
		n.Text(c, "编辑待发消息").FontSize(11).FixedLineHeight(22).TextColor(t.TextMuted)
		n.Spacer(c).Grow(1)
		if ghost(c, "取消编辑").PaddingX(0).Height(22).Disabled(v.queueBusy[sid]).Clicked() {
			m.CancelQueueEdit(sid)
			v.focusComposer = true
		}
	})
}
func (v *View) sendActions(c *n.Context, t *n.Theme, empty bool) {
	trigger := control(c, 5).Size(20, 28).Label("发送操作").Children(func() { n.Icon(c, chevron).Size(11, 11).TextColor(t.TextMuted) })
	if v.menuOpen == nil {
		v.menuOpen = map[string]bool{}
	}
	key := "send-actions:" + v.Model.DraftKey()
	open := v.menuOpen[key]
	if trigger.Clicked() {
		open = !open
	}
	popover(c.Key(key), trigger, &open, func(panel n.Element) {
		panel.AttachTo(trigger, n.AnchorTopEnd, n.AnchorBottomEnd).Margin(0, 0, 6, 0).Label("发送选项").Width(238).Padding(3).Radius(7).Background(raised(t)).Border(1, t.Border).Shadow(0, 4, 16, 0, n.RGBA(0, 0, 0, .12)).Children(func() {
			n.Column(c).FillWidth().Gap(0).FocusGroup(n.Vertical).Children(func() {
				for i, action := range []struct {
					name     string
					icon     *n.SVG
					disabled bool
					run      func()
				}{
					{"加入队列", queueAddIcon, empty || !v.Model.Connected || v.Model.WorkerMinor < 2, func() { v.sendMessage(false) }},
					{"立即介入", steerMessageIcon, empty || !v.Model.Connected || v.Model.ActiveTurn() == "" || v.Model.WorkerMinor < 2, func() { v.sendMessage(true) }},
					{"停止当前轮", stop, !v.Model.Connected || v.Model.ActiveTurn() == "", v.interrupt},
				} {
					if i == 2 {
						n.Column(c).FillWidth().Height(1).Margin(3, 4).Background(t.Border)
					}
					button := control(c.Key(action.name), 4).FillWidth().Height(26).PaddingX(7).Label(action.name).Disabled(action.disabled)
					if i == 0 {
						button.AutoFocus()
					}
					button.Children(func() {
						n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
							n.Icon(c, action.icon).Size(14, 14).TextColor(t.TextMuted)
							n.Text(c, action.name).FontSize(13).FixedLineHeight(20).TextColor(t.Text)
						})
					})
					if button.Clicked() {
						action.run()
						open = false
						trigger.Focus()
					}
				}
			})
		})
	})
	v.menuOpen[key] = open
}
func (v *View) queueCommand(method string, in protocol.QueueInput, done func(domain.QueueState)) {
	if v.Controller == nil {
		if method == "queue.reorder" {
			v.queueDrag = nil
		}
		return
	}
	if v.queueBusy == nil {
		v.queueBusy = map[string]bool{}
	}
	v.queueBusy[in.SessionID] = true
	if !v.do(app.Job{Method: method, Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		delete(v.queueBusy, in.SessionID)
		if method == "queue.reorder" && v.queueDrag != nil && v.queueDrag.session == in.SessionID {
			v.queueDrag = nil
		}
		if err != nil {
			m.Error = err.Error()
			return
		}
		var q domain.QueueState
		if err = json.Unmarshal(b, &q); err != nil {
			m.Error = err.Error()
			return
		}
		m.SetQueue(q)
		if done != nil {
			done(q)
		}
	}}) {
		delete(v.queueBusy, in.SessionID)
		if method == "queue.reorder" && v.queueDrag != nil && v.queueDrag.session == in.SessionID {
			v.queueDrag = nil
		}
	}
}
func (v *View) saveQueued() {
	m := v.Model
	sid := m.SessionID
	edit := m.QueueEdits[sid]
	text := m.Drafts[m.DraftKey()]
	if edit.ID == "" || v.queueBusy[sid] || !m.Connected || strings.TrimSpace(text) == "" {
		return
	}
	v.queueCommand("queue.edit", protocol.QueueInput{SessionID: sid, ID: edit.ID, Revision: edit.Revision, Text: text}, func(q domain.QueueState) {
		m.FinishQueueEdit(sid, edit.ID, text, q)
		if m.SessionID == sid {
			v.focusComposer = true
		}
	})
}
func (v *View) submitActive(in domain.Submission, method, turn string) {
	m := v.Model
	var sent *sentMessageMotion
	var payload any = in
	if method == "turn.steer" {
		sent = v.beginSendMotion(in)
		payload = protocol.SteerInput{Input: in, TurnID: turn}
	}
	var prepare func() error
	if v.Saver != nil {
		state, _ := m.EncodeState()
		v.Saver.Queue(state)
		v.lastQueued = state
		prepare = v.Saver.FlushLatest
	}
	if !v.do(app.Job{Method: method, Input: payload, Prepare: prepare, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			if sent != nil && v.sent == sent {
				v.sent = nil
			}
			m.SubmissionError(in, err)
			return
		}
		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil {
			if sent != nil && v.sent == sent {
				v.sent = nil
			}
			m.Error = err.Error()
			return
		}
		m.Accepted(in, op)
		if sent != nil && v.sent == sent {
			if op.State == "rejected" || op.State == "indeterminate" {
				v.sent = nil
			} else {
				sent.session = op.SessionID
			}
		}
	}}) {
		if sent != nil && v.sent == sent {
			v.sent = nil
		}
		m.Reject(in, "操作繁忙，请重试")
	}
}
