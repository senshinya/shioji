package domain

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// ClipDisplayText bounds the JSON encoding of a text field, including escaped
// characters and the notice shown when the display copy is shortened.
func ClipDisplayText(text string, limit int) string {
	size := func(s string) int {
		encoded, _ := json.Marshal(s)
		return len(encoded)
	}
	if len(text) <= limit && size(text) <= limit {
		return text
	}
	const suffix = "\n[Output limit reached]"
	low, high := 0, min(len(text), limit)
	for low < high {
		mid := low + (high-low+1)/2
		if size(text[:mid]+suffix) <= limit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	for low > 0 && low < len(text) && !utf8.RuneStart(text[low]) {
		low--
	}
	return strings.ToValidUTF8(text[:low], "�") + suffix
}
