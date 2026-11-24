package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestAuditMobileSelectedAgentTabIsVisible(t *testing.T) {
	tabs := []mobileSegment{
		{title: "常规"}, {title: "模型"}, {title: "Skills"}, {title: "MCP"}, {title: "Hooks"}, {title: "高级"},
	}
	for _, tc := range []struct {
		name  string
		width int
	}{{"open narrow", 320}, {"resize to narrow", 540}} {
		t.Run(tc.name, func(t *testing.T) {
			selection := "高级"
			tester := ui.NewTester(func(c *ui.Context) {
				c.SetZoom(mobileBaselineZoom)
				ui.Column(c).Fill().PaddingX(16).Children(func() {
					mobileSegments(c, tabs, selection, func(value string) { selection = value })
				})
			}, tc.width, 220)
			tester.SetSize(320, 220)
			tester.Advance(time.Second)
			if err := tester.Click("高级"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuditMobileFormActionsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fields    []domain.FormField
		wantError string
	}{
		{name: "zero fields"},
		{name: "required empty array", fields: []domain.FormField{{ID: "checks", Title: "检查项", Type: "array", Required: true,
			Options: []domain.FormOption{{Value: "transport", Label: "连接检查"}}}}},
		{name: "array below minimum", fields: []domain.FormField{{ID: "checks", Title: "检查项", Type: "array", Required: true,
			Options: []domain.FormOption{{Value: "transport", Label: "连接检查"}}, MinItems: new(1)}},
			wantError: "检查项的值不符合要求"},
		{name: "required boolean without default", fields: []domain.FormField{{ID: "cache", Title: "启用缓存", Type: "boolean", Required: true}},
			wantError: "请填写启用缓存"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &mobileApp{model: app.NewModel()}
			a.model.Connected = true
			requests := []domain.PendingRequest{{ID: "form", SessionID: "session", Title: "配置", Revision: 1, Kind: "form",
				State: domain.RequestPending, Fields: tc.fields}}
			pager := mobileRequestPager{}
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() {
					a.renderRequestPanel(c, nil, "session", "", false, requests, 0, -1, &pager)
				})
			}, 390, 600)
			tester.Advance(time.Second)
			for _, action := range []string{"确认表单", "拒绝", "取消"} {
				if !tester.HasText(action) {
					t.Errorf("missing form action %q; controls %v", action, tester.Texts())
				}
			}
			if tc.wantError != "" && !tester.HasText(tc.wantError) {
				t.Errorf("missing validation feedback %q", tc.wantError)
			}
			if tester.HasText("请填写检查项") {
				t.Error("an empty selection is legal for a required array without minItems")
			}
		})
	}
}

func TestAuditMobilePageWithoutFooterUsesFullViewport(t *testing.T) {
	var body ui.Rect
	tester := ui.NewTester(func(c *ui.Context) {
		mobilePageWithFooter(c, func() {
			mobileMessage(c, "只读内容")
		}, nil).DrawOver(func(_ *ui.Painter, r ui.Rect) { body = r })
	}, 320, 220)
	tester.Advance(time.Second)
	if body.H != 220 {
		t.Errorf("empty footer reserves page space: body=%+v", body)
	}
}

func TestAuditMobileRequestCaptionCenteredInHitArea(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetZoom(mobileBaselineZoom)
		ui.Column(c).Fill().Children(func() {
			transcriptDisclosure(transcriptRequestAction(c, "运行命令", "查看请求来源", permissionSymbol, rightSymbol, func() {}))
		})
	}, 320, 220)
	target, interactive := tester.Find("查看请求来源")
	caption, visible := tester.Find("运行命令")
	offset := (caption.Y + caption.H/2) - (target.Y + target.H/2)
	if !interactive || !visible || target.H < 44 || offset < -1 || offset > 1 {
		t.Errorf("caption should be centered in full hit area, target=%+v caption=%+v", target, caption)
	}
}
