package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestChildInputStartsCollapsedAndRetainsDraftThroughDisclosure(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	d := v.Model.Details["s"]
	d.Session.ThreadID = "source"
	d.Works[0].OriginRef = "source"
	d.Works[0].CanSteer = true
	v.Model.Details["s"] = d
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	if _, ok := ui.Find("子 Agent 消息输入"); ok {
		t.Fatal("infrequent input occupies detail by default")
	}
	if err := ui.Click("发送消息"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(80 * time.Millisecond)
	during, _ := ui.Find("子 Agent 输入区")
	ui.Advance(time.Second)
	after, _ := ui.Find("子 Agent 输入区")
	if during.H <= 28 || during.H >= after.H {
		t.Fatal("disclosure has no intermediate height", during, after)
	}
	ui.Click("子 Agent 消息输入")
	ui.Type("补充根节点测试")
	ui.Click("收起子 Agent 输入")
	ui.Advance(time.Second)
	ui.Click("发送消息")
	ui.Advance(time.Second)
	if v.workPanel("agents").detail("agent-a").input.draft != "补充根节点测试" {
		t.Fatal("disclosure discarded the child draft", v.workPanel("agents").detail("agent-a").input.draft, ui.Focused("子 Agent 消息输入"))
	}
	if v.Model.Drafts["session:s"] != "keep draft" {
		t.Fatal("child input replaced parent draft")
	}
}

func TestChildInputKeepsDraftUntilExactReceiptAndHonorsNewlineShortcut(t *testing.T) {
	in := domain.WorkInput{OperationID: "op", SessionID: "s", WorkID: "a", Mode: "steer", Text: "keep"}
	s := workInputUI{draft: "keep", phase: workInputSending, operation: in}
	s.result(in, nil, errors.New("connection lost"), false)
	if s.phase != workInputUnknown || s.draft != "keep" {
		t.Fatal("uncertain send cleared draft", s)
	}
	body, _ := json.Marshal(domain.WorkInputOperation{Input: in, State: "confirmed"})
	other := in
	other.WorkID = "b"
	s.result(other, body, nil, true)
	if s.draft != "keep" {
		t.Fatal("another child receipt cleared draft")
	}
	s.result(in, body, nil, true)
	if s.draft != "" || s.locked() {
		t.Fatal("matching confirmation failed", s)
	}
	v, ui := sessionWorkFixture(t)
	d := v.Model.Details["s"]
	d.Works[0].CanSteer = true
	v.Model.Details["s"] = d
	v.Model.Preferences.Shortcuts.Send = "Cmd+Enter"
	v.Model.Preferences.Shortcuts.Newline = "Enter"
	v.Model.Preferences.Shortcuts.Steer = "Shift+Enter"
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	ui.Click("发送消息")
	ui.Advance(time.Second)
	ui.Click("子 Agent 消息输入")
	ui.Type("line")
	ui.Key(0, n.KeyEnter)
	ui.Type("two")
	if got := v.workPanel("agents").detail("agent-a").input.draft; got != "line\ntwo" {
		t.Fatal("child input ignored the configured newline binding", got)
	}
}

func TestChildInputConnectionLossUnlocksReceiptQueryWithoutResending(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	u := v.workPanel("agents").detail("agent-a")
	u.input = workInputUI{open: true, draft: "keep", phase: workInputSending, operation: domain.WorkInput{OperationID: "op", SessionID: "s", WorkID: "agent-a", Text: "keep"}}
	v.Model.ConnectionEpoch = "new-connection"
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	if u.input.phase != workInputUnknown || u.input.draft != "keep" {
		t.Fatal("connection change strands the input without a receipt query", u.input)
	}
}

func TestChildInputNativeSendLocksOnlyItsDraftAndCorrelatesRetry(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	v.Controller = app.NewController("")
	d := v.Model.Details["s"]
	d.Works[0].CanSteer = true
	v.Model.Details["s"] = d
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	ui.Click("发送消息")
	ui.Advance(time.Second)
	ui.Click("子 Agent 消息输入")
	ui.Type("only child")
	ui.Key(0, n.KeyEnter)
	ui.Key(0, n.KeyEnter)
	var job app.Job
	count := 0
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.work.send" {
			job = j
			count++
		}
	}
	if count != 1 {
		t.Fatal("input dispatched repeatedly", count)
	}
	in := job.Input.(domain.WorkInput)
	if in.SessionID != "s" || in.WorkID != "agent-a" || in.Mode != "steer" || in.Text != "only child" {
		t.Fatal("wrong child target", in)
	}
	job.Apply(v.Model, nil, &protocol.RemoteError{Code: "notSent", Message: "not accepted"})
	ui.Frame()
	u := v.workPanel("agents").detail("agent-a")
	if u.input.draft != "only child" || u.input.locked() {
		t.Fatal("rejection discarded draft", u.input)
	}
	ui.Click("发送给子 Agent")
	var retry app.Job
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.work.send" {
			retry = j
		}
	}
	in2 := retry.Input.(domain.WorkInput)
	if in2.OperationID == in.OperationID {
		t.Fatal("explicit rejected retry reused settled ID")
	}
	body, _ := json.Marshal(domain.WorkInputOperation{Input: in2, State: "confirmed"})
	retry.Apply(v.Model, body, nil)
	ui.Frame()
	if u.input.draft != "" || u.input.locked() || v.Model.Drafts["session:s"] != "keep draft" {
		t.Fatal("receipt changed unrelated draft", u.input)
	}
}

func TestChildInputNativeEvidence(t *testing.T) {
	dir := os.Getenv("SHIOJI_WORK_INPUT_CAPTURE")
	if dir == "" {
		t.Skip("set SHIOJI_WORK_INPUT_CAPTURE")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	for _, dark := range []bool{false, true} {
		v, ui := sessionWorkFixture(t)
		v.Controller = app.NewController("")
		ui.SetDark(dark)
		d := v.Model.Details["s"]
		d.Works[0].CanSteer = true
		v.Model.Details["s"] = d
		ui.Click("查看子 Agent：检查边界条件")
		ui.Advance(time.Second)
		name := "light"
		if dark {
			name = "dark"
		}
		saveNativeSkillImage(t, ui, name+"-collapsed.png")
		if !dark {
			ui.Click("发送消息")
			for f := 0; f < 30; f++ {
				ui.Frame()
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%03d.png", f))
				ui.Advance(time.Second / 60)
			}
		} else {
			ui.Click("发送消息")
			ui.Advance(time.Second)
		}
		ui.Click("子 Agent 消息输入")
		ui.Type("请补充根节点旋转的边界测试。")
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, name+"-expanded.png")
		ui.Click("收起子 Agent 输入")
		ui.Advance(time.Second)
		if got := v.workPanel("agents").detail("agent-a").input.draft; got != "请补充根节点旋转的边界测试。" {
			t.Fatal(got)
		}
	}
}
