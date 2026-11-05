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
	text      string
	live      bool
	fresh     bool
	changedAt time.Time
	revealing bool
	seen      uint64
}

func (v *View) syncReplyStreams(detail domain.SessionDetail) {
	m := v.Model
	v.replyFrame++
	opening := m.FoldToolHistory[m.SessionID] && detail.Session.ID != ""
	reset := v.replyStreams == nil || v.replySession != m.SessionID || v.replyEpoch != m.ConnectionEpoch || opening
	if reset {
		v.replySession, v.replyEpoch = m.SessionID, m.ConnectionEpoch
		v.replyStreams = make(map[string]*replyStream)
	}
	for _, stream := range v.replyStreams {
		// An unmounted virtual row has no animation to wait for.
		if stream.seen+1 != v.replyFrame || v.reduceMotion {
			stream.revealing = false
		}
	}
	for _, item := range detail.Items {
		if item.Kind != "assistant" {
			continue
		}
		stream := v.replyStreams[item.ID]
		if stream == nil {
			v.replyStreams[item.ID] = &replyStream{text: item.Text, live: !reset, fresh: !reset, changedAt: v.frameTime, revealing: !reset && !v.reduceMotion && item.Text != ""}
		} else if stream.text != item.Text {
			stream.live = strings.HasPrefix(item.Text, stream.text)
			stream.text, stream.changedAt = item.Text, v.frameTime
			stream.revealing = stream.live && !v.reduceMotion && item.Text != ""
		}
	}
}

type replyMountKey struct{}

func (v *View) assistantReply(c *n.Context, item domain.Item) {
	root := n.Column(c.Key(item.ID)).FillWidth()
	initialized := n.Local(root, replyMountKey{}, func() bool { return false })
	stream := v.replyStreams[item.ID]
	streaming := false
	if stream != nil {
		// The final payload can append text too. Feed that frame through Stream
		// before View takes over and lets Markdown drain the buffered tail.
		streaming = stream.live && (!item.Final || stream.changedAt.Equal(v.frameTime))
		if !*initialized && !stream.fresh {
			streaming = false
		}
		stream.fresh = false
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
	if v.reduceMotion || stream == nil || !stream.live {
		v.md.Reveal = &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}
	}
	root.Children(func() {
		if streaming {
			markdown.Stream(c.Key("body"), item.Text, &v.md).FillWidth().FontSize(14)
		} else {
			markdown.View(c.Key("body"), item.Text, &v.md).FillWidth().FontSize(14)
		}
	})
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
