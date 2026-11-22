package app

import (
	"slices"
	"testing"
)

func TestRuntimeArgumentTextRoundTrip(t *testing.T) {
	args := []string{"--profile", "with space", "-c", "escaped='yes'", "-c", "", "-c", "literal\\value\tand\nwhitespace"}
	text := FormatRuntimeArguments(args)
	got, err := ParseRuntimeArguments(text)
	if err != nil || !slices.Equal(got, args) {
		t.Fatalf("argument text changed its values or boundaries: %q, %v", got, err)
	}
	if _, err := ParseRuntimeArguments("--profile 'unfinished"); err == nil {
		t.Fatal("accepted an unfinished quoted argument")
	}
}
