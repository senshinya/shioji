package ui

import (
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

type composerSendMatch struct{ input string }

// T3's composer uses this curve explicitly. Its desktop message positioning
// delegates to browser smooth scrolling, whose Chromium curve is separate.
var composerDescentEase = n.CubicBezier(.4, 0, .2, 1)
var sentScrollEase = n.CubicBezier(.4, 0, 0, 1)

const sentScrollDuration = 480 * time.Millisecond
const sentFadeDuration = 300 * time.Millisecond

// A send owns one ephemeral transition. Its input identity comes from the
// accepted operation, so history, remote-client sends and navigation cannot
// accidentally replay the entrance. Nothing here changes runtime acceptance.
type sentMessageMotion struct {
	input, origin, session, epoch, workspace string
	started                                  time.Time
	source                                   n.Rect
	anchored, focused, departed              bool
}

func (v *View) beginSendMotion(in domain.Submission) *sentMessageMotion {
	s := &sentMessageMotion{input: "input:" + in.OperationID, origin: in.SessionID, epoch: v.Model.ConnectionEpoch, workspace: in.WorkspaceID}
	if v.inputBounds.W > 0 {
		// Align the bubble text with the draft text, accounting for their
		// respective 16/12 px bubble and 9/0 px input insets.
		s.source = v.inputBounds
		s.source.X -= 7
		s.source.Y -= 12
		s.source.W += 14
		s.source.H += 24
	}
	v.sent = s
	return s
}

func (v *View) syncSendMotion() {
	s := v.sent
	if s == nil {
		return
	}
	m := v.Model
	if s.origin == "" && m.SessionID != "" && m.SessionID == s.session {
		s.departed = true
	}
	if (s.departed && m.SessionID == "") || m.ConnectionEpoch != s.epoch || v.devices || (s.origin == "" && m.WorkspaceID != s.workspace) || (m.SessionID != s.origin && m.SessionID != s.session) {
		v.sent = nil
		return
	}
	if !s.started.IsZero() && v.frameTime.Sub(s.started) >= sentScrollDuration+60*time.Millisecond {
		v.sent = nil
	}
}

func (v *View) anchorSentMessage(c *n.Context, state *n.ListState, rows []transcriptRow) {
	s := v.sent
	if s == nil || s.session != v.Model.SessionID || s.anchored {
		return
	}
	for i, row := range rows {
		if row.key != s.input {
			continue
		}
		s.started, s.anchored = c.Now(), true
		state.AnchorRow(i+1, 100, n.Animation{Duration: sentScrollDuration, Ease: sentScrollEase})
		c.AnimationFrame()
		break
	}
}

func (v *View) sentMessagePose(c *n.Context, content n.Element, item domain.Item) {
	s := v.sent
	if s == nil || item.ID != s.input || s.started.IsZero() || c.Preferences().ReduceMotion || s.source.W > 0 {
		return
	}
	p := min(float32(c.Now().Sub(s.started))/float32(sentFadeDuration), 1)
	k := n.EaseOut(max(0, p))
	content.Opacity(k).Transform(n.Translation(0, 12*(1-k)))
	if p < 1 {
		c.AnimationFrame()
	}
}

// Keep the sent words visible as they leave the editor. Only their
// bubble surface emerges; the message itself does not fade in at the top.
func (v *View) sentMessageBubble(c *n.Context, t *n.Theme, bubble n.Element, item domain.Item) {
	s := v.sent
	if s == nil || item.ID != s.input || s.source.W <= 0 || c.Preferences().ReduceMotion {
		return
	}
	bubble.Background(userBubble(t).Alpha(v.sendProgress(c)))
	bubble.Transition(n.ElementTransition{From: &s.source, Duration: sentScrollDuration, Ease: sentScrollEase, Position: true, Lift: true})
}

func (v *View) sendProgress(c *n.Context) float32 {
	s := v.sent
	if s == nil || s.started.IsZero() {
		return 0
	}
	p := min(max(float32(c.Now().Sub(s.started))/float32(sentScrollDuration), 0), 1)
	if p < 1 {
		c.AnimationFrame()
	}
	return sentScrollEase(p)
}

func (v *View) composerSendTransition(c *n.Context, composer n.Element, home bool) {
	s := v.sent
	if s == nil {
		return
	}
	t := n.ElementTransition{Duration: 360 * time.Millisecond, Ease: n.EaseOut, Size: true}
	if s.origin == "" {
		t.Duration, t.Ease = 400*time.Millisecond, composerDescentEase
	}
	if s.origin == "" && (home || s.session == v.Model.SessionID) {
		t.Match = composerSendMatch{s.input}
	}
	composer.Transition(t)
}
