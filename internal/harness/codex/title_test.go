package codex

import (
	"shioji/internal/domain"
	"testing"
)

func TestTitleModelUsesAnAdvertisedLunaWithoutChangingChatConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []domain.Model
		want   string
	}{
		{"available", []domain.Model{{ID: "gpt-6.1-sol", Default: true}, {ID: "gpt-6-luna", Efforts: []string{"low", "medium"}}}, "gpt-6-luna"},
		{"preferred luna", []domain.Model{{ID: "gpt-5.6-luna"}, {ID: "gpt-6-luna", Default: true}}, "gpt-6-luna"},
		{"no luna", []domain.Model{{ID: "gpt-6.1-sol", Default: true}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, err := titleModel(tc.models)
			if tc.want == "" {
				if err == nil {
					t.Fatal("missing Luna silently selected another model", model)
				}
				return
			}
			if err != nil || model.ID != tc.want {
				t.Fatal("did not select the available Luna", model, err)
			}
		})
	}
}
