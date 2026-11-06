package domain

import "testing"

func TestFormContentRejectsInvalidTypedValuesAndExtraFields(t *testing.T) {
	max := float64(8)
	min := 1
	fields := []FormField{{ID: "count", Title: "重试次数", Type: "integer", Maximum: &max}, {ID: "email", Title: "邮箱", Type: "string", Format: "email"}, {ID: "checks", Title: "检查项", Type: "array", MinItems: &min, Options: []FormOption{{Value: "read"}, {Value: "write"}}}}
	for _, answers := range []map[string][]string{{"count": {"9"}}, {"count": {"NaN"}}, {"count": {"1.5"}}, {"email": {"Name <a@example.com>"}}, {"checks": {"read", "read"}}, {"checks": {"outside"}}, {"extra": {"value"}}} {
		if _, err := FormContent(fields, answers); err == nil {
			t.Fatal("invalid values accepted", answers)
		}
	}
	if _, err := FormContent(fields, map[string][]string{"count": {"8"}, "email": {"a@example.com"}, "checks": {"read", "write"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRequiredArrayDistinguishesMissingFromValidEmpty(t *testing.T) {
	zero := 0
	fields := []FormField{{ID: "checks", Title: "检查项", Type: "array", Required: true, MaxItems: &zero}}
	if _, err := FormContent(fields, nil); err == nil {
		t.Fatal("missing required field accepted")
	}
	content, err := FormContent(fields, map[string][]string{"checks": {}})
	if err != nil {
		t.Fatal("valid empty array rejected", err)
	}
	if values, ok := content["checks"].([]string); !ok || len(values) != 0 {
		t.Fatal("empty array omitted", content)
	}
}
