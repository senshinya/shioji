package domain

import (
	"strings"
	"unicode/utf8"
)

// FindLiteral returns the original UTF-8 byte range of occurrence, counting
// non-overlapping, case-insensitive literal matches. Missing matches return -1.
func FindLiteral(text, query string, occurrence int) (int, int) {
	needle := strings.ToLower(query)
	if needle == "" || occurrence < 0 {
		return -1, -1
	}
	folded, at := strings.ToLower(text), 0
	for n := 0; n <= occurrence; n++ {
		found := strings.Index(folded[at:], needle)
		if found < 0 {
			return -1, -1
		}
		at += found
		if n < occurrence {
			at += len(needle)
			continue
		}
		if folded == text {
			return at, at + len(needle)
		}
		first, last := utf8.RuneCountInString(folded[:at]), utf8.RuneCountInString(folded[:at+len(needle)])
		start, end, runeIndex := -1, len(text), 0
		for pos := range text {
			if runeIndex == first {
				start = pos
			}
			if runeIndex == last {
				end = pos
				break
			}
			runeIndex++
		}
		return start, end
	}
	return -1, -1
}
