package ui

import (
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestFeedbackShortFailureShowsReasonWithoutDisclosure(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		m.Error = "请先连接工作区所属设备，再重试。"
		ui := n.NewTester(New(m, nil, "").View, 1100, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		if !ui.HasText(m.Error) || ui.HasText("操作未完成") || ui.HasText("操作未能完成，原有内容已保留。") || ui.HasText("详细信息") {
			t.Fatal("short failure hides its reason behind generic copy", ui.Texts())
		}
	}
}

func TestFeedbackSpecificFailureKeepsContextAndGuidance(t *testing.T) {
	m := layoutModel()
	ui := n.NewTester(New(m, nil, "").View, 1100, 820)
	m.ReportFeedback(app.Feedback{Title: "本次提交未被接纳", Body: "原文与附件已保留。", Details: "所选模型已不可用，请选择其他模型。"})
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("本次提交未被接纳") || !ui.HasText("所选模型已不可用，请选择其他模型。\n原文与附件已保留。") || ui.HasText("详细信息") {
		t.Fatal("failure lost its reason, operation or useful guidance", ui.Texts())
	}
}

func TestFeedbackPromotedReasonRemainsCopyable(t *testing.T) {
	m := layoutModel()
	m.Error = "请先连接工作区所属设备，再重试。"
	ui := n.NewTester(New(m, nil, "").View, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click(m.Error); err != nil {
		t.Fatal(err)
	}
	ui.Key(n.Cmd, n.KeyA)
	ui.Key(n.Cmd, n.KeyC)
	if ui.Clipboard() != m.Error {
		t.Fatal("moving the reason out of details lost native copy", ui.Clipboard())
	}
}

func TestFeedbackLongFailureShowsPreviewAndRetainsDiagnostic(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	m.Error = "无法连接执行设备，请检查设备是否在线。\n" + strings.Repeat("connection refused\n", 25) + "诊断末尾"
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("无法连接执行设备，请检查设备是否在线。") {
		t.Fatal("long diagnostic hides even its reason", ui.Texts())
	}
	if err := ui.Click("查看消息详情"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText(m.Error) {
		t.Fatal("long diagnostic was truncated instead of retained")
	}
}

func TestFeedbackLongFailureDoesNotHideReasonBehindPreservationNotice(t *testing.T) {
	m := layoutModel()
	ui := n.NewTester(New(m, nil, "").View, 1100, 820)
	m.ReportFeedback(app.Feedback{Title: "本次提交未被接纳", Body: "原文与附件已保留。", Details: "无法切换分支，请先处理目录中的改动。\n" + strings.Repeat(" M internal/example.go\n", 12)})
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("无法切换分支，请先处理目录中的改动。…\n原文与附件已保留。") || !ui.HasText("详细信息") {
		t.Fatal("long failure shows only the preservation notice", ui.Texts())
	}
}

func TestFeedbackPromotedCopyIsRedactedAndReplaysWithoutDuplication(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	m.ReportFeedback(app.Feedback{Title: "操作未完成", Body: "操作未能完成，原有内容已保留。", Details: "连接失败：token=private-token"})
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("连接失败：token=[已隐藏]") || strings.Contains(strings.Join(ui.Texts(), "\n"), "private-token") {
		t.Fatal("promoted reason is missing or exposes credentials", ui.Texts())
	}
	ui.Click("关闭消息")
	ui.Advance(time.Second)
	ui.Click("最近消息")
	ui.Advance(time.Second)
	if !ui.HasText("连接失败：token=[已隐藏]") {
		t.Fatal("recent history reverted to generic copy")
	}
	if err := ui.Click("连接失败：token=[已隐藏]"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("连接失败：token=[已隐藏]") || len(v.feedbackState().messages) != 1 {
		t.Fatal("replay lost the reason or duplicated history")
	}
}

func TestBackgroundTimeoutDoesNotBecomeAnUnnamedNotification(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 680)
	m.Error = "context deadline exceeded"
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("弹出消息"); ok || len(v.feedbackState().messages) != 0 {
		t.Fatalf("a background timeout interrupted the conversation: %v", ui.Texts())
	}
}

func TestOperationFeedbackExplainsATimeoutInPlainLanguage(t *testing.T) {
	title, body, details := feedbackCopy(app.Feedback{Title: "重新加载未完成", Body: "context deadline exceeded"})
	if title != "重新加载未完成" || body != "等待设备响应超时。" || details != "" {
		t.Fatalf("timeout lost its action or exposed a Go error: %q %q %q", title, body, details)
	}
}
