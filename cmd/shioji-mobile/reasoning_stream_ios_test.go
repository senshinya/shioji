package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestMobileReasoningStreamsAndAutoCollapses(t *testing.T) {
	const chunk1 = "先核对移动端的思考摘要流式更新与高度动画衔接。"
	const chunk2 = "再验证尾部的多换行被扣住并在下一段到达时输出。"
	m := app.NewModel()
	sid := "s-reasoning"
	started := time.Now().Add(-2 * time.Second)
	session := domain.Session{ID: sid, WorkspaceID: "work", ThreadID: "t1", Status: domain.ExecutionRunning}
	m.Snapshot = domain.Snapshot{DeviceID: "device", Sessions: []domain.Session{session}}
	m.SessionID = sid
	m.Connected = true
	m.Details[sid] = domain.SessionDetail{
		Session:  session,
		Sequence: 1,
		History:  &domain.HistoryWindow{First: 1, Last: 2},
		Turns:    []domain.Turn{{ID: "turn-1", Status: domain.ExecutionRunning, StartedAt: &started}},
		Items: []domain.Item{
			{ID: "u1", TurnID: "turn-1", Order: 1, Kind: "user", Text: "检查一下", Status: "sent", Final: true},
			{ID: "r1", TurnID: "turn-1", Order: 2, Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Status: "running", ActivityOrder: 1},
		},
	}
	a := &mobileApp{
		model:   m,
		history: make(map[string]*ui.ListState),
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
			s := a.session()
			a.renderTranscript(c, nil, s, a.model.Details[s.ID], true, nil, nil, nil, ui.Element{}, firstSubmissionView{})
		})
	}, 390, 640)
	tester.Advance(time.Second)

	if !tester.HasText("正在思考") || tester.HasText("思考摘要") {
		t.Fatalf("empty live reasoning should show only 正在思考: %v", tester.Texts())
	}

	d := m.Details[sid]
	d.Sequence++
	d.Items[1].Text = chunk1 + "\n\n\n"
	m.Details[sid] = d
	tester.Frame()
	tester.Advance(32 * time.Millisecond)
	if tester.HasText(chunk1) {
		t.Fatalf("first chunk should stream progressively rather than popping in at once")
	}
	tester.Advance(time.Second)
	heading, hasHeading := tester.Find("思考摘要")
	body, hasBody := tester.Find(chunk1)
	thinking, hasThinking := tester.Find("正在思考")
	if !hasHeading || !hasBody || !hasThinking {
		t.Fatalf("expected expanded 思考摘要 + indented body + bottom 正在思考: %v", tester.Texts())
	}
	if !(heading.Y < body.Y && body.Y+body.H <= thinking.Y+1) {
		t.Fatalf("expected vertical order 思考摘要 < body < 正在思考: heading=%.1f body=[%.1f,%.1f] thinking=%.1f", heading.Y, body.Y, body.Y+body.H, thinking.Y)
	}
	for _, txt := range tester.Texts() {
		if strings.HasPrefix(txt, chunk1) && (strings.HasSuffix(txt, "\n") || strings.HasSuffix(txt, "\r")) {
			t.Fatalf("trailing newline exposed while streaming: %q", txt)
		}
	}

	d = m.Details[sid]
	d.Sequence++
	d.Items[1].Text = chunk1 + "\n\n\n" + chunk2 + "\n\n\n"
	m.Details[sid] = d
	tester.Frame()
	tester.Advance(time.Second)
	wantFull := chunk1 + "\n\n\n" + chunk2
	if !tester.HasText(wantFull) {
		t.Fatalf("second chunk with intermediate newlines missing: %v", tester.Texts())
	}

	d = m.Details[sid]
	d.Sequence++
	d.Items[1].Status = "completed"
	d.Items[1].Final = true
	m.Details[sid] = d
	tester.Frame()
	tester.Advance(time.Second)
	if !tester.HasText("思考摘要") || tester.HasText("正在思考") || tester.HasText(wantFull) || tester.HasText(chunk1) {
		t.Fatalf("completed reasoning did not auto-collapse: %v", tester.Texts())
	}
	if err := tester.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(time.Second)
	if !tester.HasText(wantFull) {
		t.Fatalf("tapping settled 思考摘要 did not reopen full body: %v", tester.Texts())
	}
}

func TestMobileToolGroupRespectsCollapsePreferenceAndStreamsWhenExpanded(t *testing.T) {
	const chunk1 = "移动端工具组默认折叠时不主动展开，手动展开后流式显示思考摘要。"
	const chunk2 = "后续段落继续逐字流式追加并隐藏尾部空行。"
	m := app.NewModel()
	m.Preferences.CollapseLatestTools = true
	sid := "s-tool-reasoning"
	started := time.Now().Add(-2 * time.Second)
	session := domain.Session{ID: sid, WorkspaceID: "work", ThreadID: "t1", Status: domain.ExecutionRunning}
	m.Snapshot = domain.Snapshot{DeviceID: "device", Sessions: []domain.Session{session}}
	m.SessionID = sid
	m.Connected = true
	m.Details[sid] = domain.SessionDetail{
		Session:  session,
		Sequence: 1,
		History:  &domain.HistoryWindow{First: 1, Last: 1},
		Turns:    []domain.Turn{{ID: "turn-1", Status: domain.ExecutionRunning, StartedAt: &started}},
		Items: []domain.Item{
			{ID: "u1", TurnID: "turn-1", Order: 1, Kind: "user", Text: "检查工具组默认折叠", Status: "sent", Final: true},
		},
	}
	a := &mobileApp{
		model:   m,
		history: make(map[string]*ui.ListState),
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
			s := a.session()
			a.renderTranscript(c, nil, s, a.model.Details[s.ID], true, nil, nil, nil, ui.Element{}, firstSubmissionView{})
		})
	}, 390, 640)
	tester.Advance(200 * time.Millisecond)

	d := m.Details[sid]
	d.Sequence++
	d.History = &domain.HistoryWindow{First: 1, Last: 3}
	d.Items = append(d.Items,
		domain.Item{ID: "t1", TurnID: "turn-1", Order: 2, Kind: "tool", Presentation: "files", Name: "读取 tool_transcript_ios.go", Status: "completed", Final: true, ActivityOrder: 1},
		domain.Item{ID: "r1", TurnID: "turn-1", Order: 3, Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: chunk1 + "\n\n\n", Status: "running", ActivityOrder: 2},
	)
	m.Details[sid] = d
	tester.Frame()
	tester.Advance(200 * time.Millisecond)

	if tester.HasText(chunk1) || !tester.HasText("1 次工具调用") {
		t.Fatalf("tool group should remain collapsed under CollapseLatestTools=true: %v", tester.Texts())
	}

	if err := tester.Click("1 次工具调用"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(32 * time.Millisecond)
	if tester.HasText(chunk1) {
		t.Fatalf("live reasoning inside newly expanded mobile tool group should stream progressively")
	}
	tester.Advance(time.Second)
	if !tester.HasText(chunk1) {
		t.Fatalf("expanded mobile tool group did not finish streaming first chunk: %v", tester.Texts())
	}

	d = m.Details[sid]
	d.Sequence++
	d.Items[2].Text = chunk1 + "\n\n\n" + chunk2 + "\n\n\n"
	m.Details[sid] = d
	tester.Frame()
	tester.Advance(time.Second)
	if !tester.HasText(chunk1 + "\n\n\n" + chunk2) {
		t.Fatalf("second chunk inside expanded mobile tool group missing: %v", tester.Texts())
	}
}
