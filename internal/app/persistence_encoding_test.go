package app

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestStateEncodingPreservesLayoutMutationsAndEarlierWrites(t *testing.T) {
	m := NewModel()
	m.Collapsed["tool"] = true
	first, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	frozen := bytes.Clone(first)
	check := func(wantDraft string, want map[string]bool) {
		t.Helper()
		data, err := m.EncodeState()
		if err != nil {
			t.Fatal(err)
		}
		var saved savedState
		if err = json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Drafts["home"] != wantDraft || len(saved.Collapsed) != len(want) {
			t.Fatal("state encoding lost current draft or layout", saved.Drafts, saved.Collapsed)
		}
		for key, value := range want {
			if got, ok := saved.Collapsed[key]; !ok || got != value {
				t.Fatal("layout update was not persisted", key, got, ok)
			}
		}
		if !bytes.Equal(first, frozen) {
			t.Fatal("later encoding changed bytes held by an earlier asynchronous write")
		}
	}
	check("", map[string]bool{"tool": true})
	m.Drafts["home"] = "draft with a literal \"collapsed\":null"
	check(m.Drafts["home"], map[string]bool{"tool": true})
	m.Collapsed["tool"] = false
	check(m.Drafts["home"], map[string]bool{"tool": false})
	m.Collapsed["other"] = true
	check(m.Drafts["home"], map[string]bool{"tool": false, "other": true})
	delete(m.Collapsed, "tool")
	check(m.Drafts["home"], map[string]bool{"other": true})
	clear(m.Collapsed)
	check(m.Drafts["home"], nil)
	m.Collapsed = nil
	check(m.Drafts["home"], nil)
	m.Collapsed = map[string]bool{"replacement": true}
	check(m.Drafts["home"], map[string]bool{"replacement": true})
}
