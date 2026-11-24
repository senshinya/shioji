package ui

import (
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/domain"
)

// This is presentation state for the visible session, never persisted history.
// Native Markdown owns the reveal clock and buffered tail; this only separates
// live additions from opening/recovering history and remounting virtual rows.
type replyStream struct {
	text        string
	phase       replyPhase
	changedAt   time.Time
	revealAfter time.Time
	revealing   bool
	stopping    bool
	seen        uint64
}

// A replacement can arrive before the row's first mount. Retain that fact
// separately from history so a later prefix update preserves the old behavior.
type replyPhase uint8

const (
	replyHistorical replyPhase = iota
	replyFresh
	replyLive
	replyReplacedFresh
)

func replyPhaseFor(live, fresh bool) replyPhase {
	if live {
		if fresh {
			return replyFresh
		}
		return replyLive
	}
	if fresh {
		return replyReplacedFresh
	}
	return replyHistorical
}
func (s *replyStream) live() bool          { return s.phase == replyFresh || s.phase == replyLive }
func (s *replyStream) fresh() bool         { return s.phase == replyFresh || s.phase == replyReplacedFresh }
func (s *replyStream) mount()              { s.phase = replyPhaseFor(s.live(), false) }
func (s *replyStream) replace(prefix bool) { s.phase = replyPhaseFor(prefix, s.fresh()) }

const receiptEntranceDuration = 200 * time.Millisecond

func (v *View) syncReplyStreams(detail domain.SessionDetail) {
	m := v.Model
	v.replyFrame++
	opening := m.FoldToolHistory[m.SessionID] && detail.Session.ID != ""
	reset := v.replyStreams == nil || v.replySession != m.SessionID || v.replyEpoch != m.ConnectionEpoch || opening
	if reset {
		v.replySession, v.replyEpoch = m.SessionID, m.ConnectionEpoch
		v.replyStreams = make(map[string]*replyStream)
		v.activityPatches = nil
		v.replyLatestOrder = 0
	}
	for _, stream := range v.replyStreams {
		// An unmounted virtual row has no animation to wait for.
		if stream.seen+1 != v.replyFrame || v.reduceMotion {
			stream.revealing = false
		}
	}
	previousOrder := v.replyLatestOrder
	var receiptUntil time.Time
	var interrupted map[string]bool
	for _, turn := range detail.Turns {
		if turn.Status == domain.ExecutionInterrupted {
			if interrupted == nil {
				interrupted = make(map[string]bool)
			}
			interrupted[turn.ID] = true
		}
	}
	for _, item := range detail.Items {
		v.replyLatestOrder = max(v.replyLatestOrder, item.Order)
		if item.Kind != "assistant" && item.Kind != "request" && item.Kind != "request_result" {
			continue
		}
		stream := v.replyStreams[item.ID]
		stopping := v.stoppingTurn(item.TurnID)
		if stopping || interrupted[item.TurnID] || stream != nil && stream.stopping {
			if stream == nil {
				stream = &replyStream{}
				v.replyStreams[item.ID] = stream
			}
			// Apply buffered and late output without starting a new reveal on
			// an interrupted reply. A rejected stop can stream subsequent data.
			stream.text, stream.phase, stream.revealing = item.Text, replyHistorical, false
			stream.revealAfter = time.Time{}
			stream.stopping = stopping
			continue
		}
		if stream == nil {
			live := !reset
			if item.Kind == "request_result" && item.Order > 0 && item.Order <= previousOrder {
				live = false
			}
			stream = &replyStream{text: item.Text, phase: replyPhaseFor(live, live), changedAt: v.frameTime, revealing: live && !v.reduceMotion && item.Text != ""}
			if item.Kind == "assistant" && live && v.frameTime.Before(receiptUntil) {
				stream.revealAfter = receiptUntil
			}
			v.replyStreams[item.ID] = stream
		} else if stream.text != item.Text {
			stream.replace(strings.HasPrefix(item.Text, stream.text))
			stream.text, stream.changedAt = item.Text, v.frameTime
			stream.revealing = stream.live() && !v.reduceMotion && item.Text != ""
		}
		if item.Kind == "request_result" && stream.live() {
			receiptUntil = stream.changedAt.Add(receiptEntranceDuration)
		}
	}
}

type replyMountKey struct{}

// Source titles share Markdown's reveal clock, but remain literal metadata.
func (v *View) requestSourceLabel(c *n.Context, t *n.Theme, label string, stream *replyStream) {
	root := n.Column(c.Key("source-label")).MinHeight(20)
	options := n.Local(root, "markdown", func() markdown.Options { return markdown.Options{ShareSelection: true} })
	options.Theme = &markdown.Theme{Font: "system-ui", FontSize: transcriptCaptionSize, LineHeight: 20.0 / transcriptCaptionSize, Text: t.TextMuted}
	options.Reveal = &markdown.Reveal{Unit: markdown.ByCharacter, Fade: 180 * time.Millisecond, Smooth: true}
	fresh := stream != nil && stream.live() && stream.fresh()
	if v.reduceMotion || stream == nil || !stream.live() {
		options.Reveal = &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}
	}
	options.OnRevealState = nil
	if stream != nil {
		stream.mount()
		stream.seen = v.replyFrame
		options.OnRevealState = func(active bool) {
			if stream.revealing != active {
				stream.revealing = active
				c.AnimationFrame()
			}
		}
	}
	var literal strings.Builder
	for _, ch := range label {
		if strings.ContainsRune("\\`*_{}[]()#+-.!<>|~&", ch) {
			literal.WriteByte('\\')
		}
		literal.WriteRune(ch)
	}
	root.Children(func() {
		if fresh {
			markdown.Stream(c.Key("text"), literal.String(), options)
		} else {
			markdown.View(c.Key("text"), literal.String(), options)
		}
	})
}

func (v *View) assistantReply(c *n.Context, item domain.Item) {
	if len(item.Questions) > 0 {
		v.asyncQuestionMessage(c, c.Theme(), item)
		return
	}
	v.md.DecorateRange = v.findDecoration(item, c.Theme().Dark)
	root := n.Column(c.Key(item.ID)).FillWidth().Clip()
	stream := v.replyStreams[item.ID]
	fresh := stream != nil && stream.live() && stream.fresh()
	// Receive all deltas normally, but let the preceding receipt finish its
	// entrance before mounting Markdown and starting its own reveal clock.
	if stream != nil && stream.live() && !v.reduceMotion && c.Now().Before(stream.revealAfter) {
		stream.seen, stream.revealing = v.replyFrame, true
		c.After(stream.revealAfter.Sub(c.Now()))
		return
	}
	initialized := n.Local(root, replyMountKey{}, func() bool { return false })
	streaming := false
	if stream != nil {
		// The final payload can append text too. Feed that frame through Stream
		// before View takes over and lets Markdown drain the buffered tail.
		streaming = stream.live() && (!item.Final || stream.changedAt.Equal(v.frameTime) || stream.fresh() && !stream.revealAfter.IsZero())
		if !*initialized && !stream.fresh() {
			streaming = false
		}
		stream.mount()
	}
	*initialized = true
	v.md.OnRevealState = nil
	if stream != nil {
		stream.seen = v.replyFrame
		v.md.OnRevealState = func(active bool) {
			if stream.revealing != active {
				stream.revealing = active
				c.AnimationFrame()
			}
		}
	}
	v.md.Reveal = &markdown.Reveal{Unit: markdown.ByCharacter, Fade: 180 * time.Millisecond, Smooth: true}
	if v.reduceMotion || stream == nil || !stream.live() {
		v.md.Reveal = &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}
	}
	duration := 180 * time.Millisecond
	if stream == nil || !stream.live() {
		duration = 0
	}
	// The existing tool-to-prose handoff establishes the first reply's final
	// slot before fading in its footer; do not add another entrance clock.
	baseline := !fresh || v.runningProgress != nil && v.runningProgress.handoff != nil
	v.transcriptContentHeight(c, root, true, baseline, duration, func() float32 {
		return v.markdownHeightDelta(func() {
			if streaming {
				markdown.Stream(c.Key("body"), item.Text, &v.md).FillWidth().FontSize(float32(v.Model.Preferences.ReplySize))
			} else {
				markdown.View(c.Key("body"), item.Text, &v.md).FillWidth().FontSize(float32(v.Model.Preferences.ReplySize))
			}
		})
	})
}

// Markdown embeds may already animate their real height. Pass their current
// movement through the enclosing reply/tool clip on the same frame.
func (v *View) markdownHeightDelta(build func()) float32 {
	if v.mermaid != nil {
		v.mermaid.heightDelta = 0
	}
	if v.markdownImages != nil {
		v.markdownImages.heightDelta = 0
	}
	build()
	delta := float32(0)
	if v.markdownImages != nil {
		delta += v.markdownImages.heightDelta
	}
	if v.mermaid != nil {
		delta += v.mermaid.heightDelta
	}
	return delta
}

func (v *View) visibleOutputAnimating() bool {
	if v.devices || v.reduceMotion || v.replySession != v.Model.SessionID {
		return false
	}
	for _, stream := range v.replyStreams {
		if stream.revealing {
			return true
		}
	}
	return false
}
