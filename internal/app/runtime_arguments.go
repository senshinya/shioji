package app

import (
	"errors"
	"strings"
	"unicode"
)

// ParseRuntimeArguments reads a quoted argument list without invoking a shell.
func ParseRuntimeArguments(text string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range text {
		if escaped {
			word.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if unicode.IsSpace(r) {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(r)
		started = true
	}
	if quote != 0 || escaped {
		return nil, errors.New("启动参数的引号或转义尚未结束")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

// FormatRuntimeArguments retains argument boundaries and literal characters.
func FormatRuntimeArguments(args []string) string {
	words := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, "\"'\\") || strings.ContainsFunc(arg, unicode.IsSpace) {
			arg = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
		}
		words = append(words, arg)
	}
	return strings.Join(words, " ")
}
