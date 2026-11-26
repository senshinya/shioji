package app

import (
	"strings"
	"unicode"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type ProfileEffortDraft struct {
	Text     string
	initial  string
	previous *[]string
}

func (d *ProfileEffortDraft) Sync(agent domain.HarnessInfo, model domain.Model) {
	if d.Text != d.initial {
		return
	}
	d.previous = nil
	if levels, ok := agent.EffortOverrides[model.ID]; ok {
		copy := append([]string{}, levels...)
		d.previous = &copy
	}
	d.initial = strings.Join(model.Efforts, ", ")
	d.Text = d.initial
}

func (d *ProfileEffortDraft) Manual() bool  { return d.previous != nil }
func (d *ProfileEffortDraft) Changed() bool { return d.Text != d.initial }

func (d *ProfileEffortDraft) Input(profile, model string, automatic bool) protocol.ProfileEffortInput {
	in := protocol.ProfileEffortInput{ID: profile, Model: model, Previous: d.previous}
	if !automatic {
		levels := strings.FieldsFunc(d.Text, func(r rune) bool { return r == ',' || r == '，' || unicode.IsSpace(r) })
		if levels == nil {
			levels = []string{}
		}
		in.Efforts = &levels
	}
	return in
}
