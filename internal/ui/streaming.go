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
}

func (v *View) syncReplyStreams(detail domain.SessionDetail) {
	m := v.Model
	opening := m.FoldToolHistory[m.SessionID] && detail.Session.ID != ""
	reset := v.replyStreams == nil || v.replySession != m.SessionID || v.replyEpoch != m.ConnectionEpoch || opening
	if reset {
		v.replySession, v.replyEpoch = m.SessionID, m.ConnectionEpoch
		v.replyStreams = make(map[string]*replyStream)
	}
	for _, item := range detail.Items {
		if item.Kind != "assistant" {
			continue
		}
		stream := v.replyStreams[item.ID]
		if stream == nil {
			v.replyStreams[item.ID] = &replyStream{text: item.Text, live: !reset, fresh: !reset, changedAt: v.frameTime}
		} else if stream.text != item.Text {
			stream.live = strings.HasPrefix(item.Text, stream.text)
			stream.text, stream.changedAt = item.Text, v.frameTime
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
