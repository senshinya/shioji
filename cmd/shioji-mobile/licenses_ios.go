package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type mobileThirdPartyNotice struct {
	Module  string
	Version string
	Text    string
}

func loadMobileThirdPartyNotices() ([]mobileThirdPartyNotice, error) {
	directory, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(directory, "THIRD_PARTY_NOTICES.json"))
	if err != nil {
		return nil, err
	}
	var notices []mobileThirdPartyNotice
	err = json.Unmarshal(data, &notices)
	return notices, err
}

func (a *mobileApp) openThirdPartyLicenses(parent *mygo.Window) {
	notices, err := loadMobileThirdPartyNotices()
	var w *mygo.Window
	w = a.page("第三方许可", func(c *ui.Context) {
		mobilePageBody(c, func() {
			if err != nil || len(notices) == 0 {
				mobileMessage(c, "无法读取第三方许可")
				return
			}
			for _, notice := range notices {
				mobileRow(c.Key(notice.Module), notice.Module, notice.Version, "", nil, func() {
					a.openThirdPartyNotice(w, notice)
				})
			}
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) openThirdPartyNotice(parent *mygo.Window, notice mobileThirdPartyNotice) {
	w := a.page(notice.Module, func(c *ui.Context) {
		p := palette(c)
		mobilePageBody(c, func() {
			ui.Row(c.Key("metadata")).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				label(c.Key("version"), notice.Version, controlTextSize, p.secondary).Grow(1).MinWidth(0)
				mobileCopyButton(c.Key("copy"), "复制许可", notice.Text)
			})
			label(c.Key("notice"), notice.Text, controlTextSize, p.ink).FillWidth().Selectable().
				FixedLineHeight(controlTextSize * transcriptLeading * c.Preferences().TextScale)
		})
	})
	a.pushNative(parent, w)
}
