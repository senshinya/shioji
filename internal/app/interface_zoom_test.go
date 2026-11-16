package app

import (
	"encoding/json"
	"testing"
)

func TestSavedInterfaceZoomDefaultsSafelyWithoutLosingValidChoices(t *testing.T) {
	for _, tc := range []struct {
		json string
		want float32
	}{
		{`{}`, 1},
		{`{"interfaceZoom":0}`, 1},
		{`{"interfaceZoom":-1}`, 1},
		{`{"interfaceZoom":20}`, 1},
		{`{"interfaceZoom":0.75}`, .75},
		{`{"interfaceZoom":1.25}`, 1.25},
		{`{"interfaceZoom":1.5}`, 1.5},
	} {
		t.Run(tc.json, func(t *testing.T) {
			var p ClientPreferences
			if err := json.Unmarshal([]byte(tc.json), &p); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(p.Normalized())
			if err != nil {
				t.Fatal(err)
			}
			var saved struct{ InterfaceZoom float32 }
			if err := json.Unmarshal(encoded, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.InterfaceZoom != tc.want {
				t.Fatal("stored interface scale was not migrated safely", saved.InterfaceZoom)
			}
		})
	}
}
