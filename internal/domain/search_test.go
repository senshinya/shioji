package domain

import "testing"

func TestFindLiteralPreservesSourceBytes(t *testing.T) {
	for _, tt := range []struct {
		text, query            string
		occurrence, start, end int
	}{
		{"😀İx", "ix", 0, 4, 7},
		{"中文 root ROOT", "root", 1, 12, 16},
		{"a.* a.*", "a.*", 1, 4, 7},
		{"aaaa", "aa", 1, 2, 4},
		{"text", "", 0, -1, -1},
		{"text", "none", 0, -1, -1},
	} {
		a, b := FindLiteral(tt.text, tt.query, tt.occurrence)
		if a != tt.start || b != tt.end {
			t.Fatalf("%q/%q #%d: (%d,%d), want (%d,%d)", tt.text, tt.query, tt.occurrence, a, b, tt.start, tt.end)
		}
	}
}
