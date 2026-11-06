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
	if ghost(c, label).FontSize(12).Height(28).Margin(0, 0, 12, 0).Clicked() {
		if s.license >= 0 {
			s.license = -1
		} else {
			s.licenses = false
		}
	}
	if s.license >= 0 {
		notice := bundledNotices[s.license]
		n.Text(c, notice.Module).FontSize(16).FontWeight(500).Selectable()
		n.Text(c, notice.Version).FontSize(11).TextColor(t.TextMuted).Margin(4, 0, 16, 0)
		n.Text(c, notice.Text).FontSize(12).FixedLineHeight(20).FillWidth().Selectable()
		return
	}
	n.Text(c, "第三方许可").FontSize(18).FontWeight(500).Margin(0, 0, 16, 0)
	for i, notice := range bundledNotices {
		if control(c.Key(notice.Module)).FillWidth().MinHeight(42).Padding(8, 10).Label("许可：" + notice.Module).Children(func() {
			n.Column(c).Grow(1).MinWidth(0).Children(func() {
				n.Text(c, notice.Module).FontSize(12)
				n.Text(c, notice.Version).FontSize(11).TextColor(t.TextMuted)
			})
			n.Icon(c, disclosureRight).Size(13, 13).TextColor(t.TextMuted)
		}).Clicked() {
			s.license = i
		}
	}
}
