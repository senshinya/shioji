package ui

import (
	_ "embed"
	"encoding/json"

	n "github.com/egoist/mygo/ui"
)

//go:embed third_party_notices.json
var thirdPartyNotices []byte

type thirdPartyNotice struct{ Module, Version, Text string }

var bundledNotices = func() []thirdPartyNotice {
	var result []thirdPartyNotice
	if err := json.Unmarshal(thirdPartyNotices, &result); err != nil {
		panic(err)
	}
	return result
}()

func (s *Settings) licenseView(c *n.Context, t *n.Theme) {
	label := "返回关于"
	if s.license >= 0 {
		label = "返回第三方许可"
	}
	if configurationAction(c, label).PaddingX(0).Margin(0, 0, 12, 0).Clicked() {
		if s.license >= 0 {
			s.license = -1
		} else {
			s.licenses = false
		}
	}
	if s.license >= 0 {
		notice := bundledNotices[s.license]
		n.Text(c, notice.Module).FontSize(configurationSectionSize).FontWeight(500).FixedLineHeight(configurationTitleLine).Selectable()
		n.Text(c, notice.Version).FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted).Margin(4, 0, configurationSectionGap, 0)
		n.Text(c, notice.Text).FontSize(12).FixedLineHeight(20).FillWidth().Selectable()
		return
	}
	settingsHeading(c, t, "第三方许可")
	for i, notice := range bundledNotices {
		if control(c.Key(notice.Module)).FillWidth().MarginX(-8).Height(56).Padding(8).Label("许可：" + notice.Module).Children(func() {
			n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
				n.Column(c).Grow(1).MinWidth(0).Children(func() {
					n.Text(c, notice.Module).FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine).SingleLine().Ellipsis("…").Tooltip(notice.Module)
					n.Text(c, notice.Version).FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted)
				})
				inlineSymbol(c, disclosureRight, notice.Module, n.Font{Family: t.Font, Size: configurationLabelSize, Weight: 400}, 12, t.TextMuted)
			})
		}).Clicked() {
			s.license = i
		}
	}
}
