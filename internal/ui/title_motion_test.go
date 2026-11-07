package ui

import (
	"bytes"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestAutomaticTitleCrossfadesInPlaceWithoutLosingComposerFocus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		model := layoutModel()
		v := New(model, nil, "")
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Click("消息输入")
		ui.Type("保留草稿")
		ui.Advance(time.Second)
		heading, _ := ui.Find("会话名称")
		editor, _ := ui.Find("消息编辑器")
		pixels := func() []byte {
			im := ui.Image()
			out := []byte{}
			for y := int(heading.Y); y < int(heading.Y+heading.H); y++ {
				for x := int(heading.X); x < int(heading.X+heading.W); x++ {
					p := im.RGBAAt(x, y)
					out = append(out, p.R, p.G, p.B, p.A)
				}
			}
			return out
		}
		before := pixels()
		session := model.Snapshot.Sessions[0]
		session.Title = "红黑树旋转边界"
		session.TitleRevision++
		if err := model.ApplyEvent(domain.Event{Sequence: 1, Type: "session.title", SessionID: session.ID, Session: &session, Text: session.Title}); err != nil {
			t.Fatal(err)
		}
		ui.Frame()
		first := pixels()
		ui.Advance(80 * time.Millisecond)
		middle := pixels()
		ui.Advance(200 * time.Millisecond)
		last := pixels()
		if !bytes.Equal(before, first) || bytes.Equal(first, middle) || bytes.Equal(middle, last) {
			t.Fatal("title update skipped its fade", dark)
		}
		after, _ := ui.Find("会话名称")
		lastEditor, _ := ui.Find("消息编辑器")
		if heading != after || editor != lastEditor || !ui.Focused("消息输入") || model.Drafts[model.DraftKey()] != "保留草稿" {
			t.Fatal("title update moved layout or lost typing", heading, after, editor, lastEditor)
		}
	}
}
