package ui

import (
	"path"
	"strings"

	n "github.com/egoist/mygo/ui"
)

// Keep the prompt compact beside its underscore in the 20-DIP viewBox.
var commandTerminalIcon = svg("M4 5l5 5-5 5M11 15h6")
var gitCommandIcon = svg("M6 7v6M6 12h2a6 5 0 0 0 6-5M8 5a2 2 0 1 1-4 0a2 2 0 1 1 4 0M16 5a2 2 0 1 1-4 0a2 2 0 1 1 4 0M8 15a2 2 0 1 1-4 0a2 2 0 1 1 4 0")

func commandActivityIcon(command string) *n.SVG {
	if gitCommand(command, 0) {
		return gitCommandIcon
	}
	return commandTerminalIcon
}

// This is a display hint, never an execution or permission decision. Inspect
// executable positions, not argument substrings. Complex scripts we cannot
// identify statically keep the ordinary command glyph.
func gitCommand(command string, depth int) bool {
	if depth >= 4 {
		return false
	}
	for _, words := range commandIconWords(command) {
	head:
		for i := 0; i < len(words); {
			word := words[i]
			if name, _, ok := strings.Cut(word, "="); ok && shellAssignmentName(name) {
				i++
				continue
			}
			switch path.Base(word) {
			case "git":
				return true
			case "env":
				i++
				for i < len(words) && strings.HasPrefix(words[i], "-") {
					option := words[i]
					i++
					if option == "--" {
						break
					}
					switch {
					case option == "-u" || option == "--unset" || option == "-C" || option == "--chdir":
						i++
					case option == "-i" || option == "-" || option == "--ignore-environment" || strings.HasPrefix(option, "--unset=") || strings.HasPrefix(option, "--chdir="):
					default:
						break head
					}
				}
				continue
			case "command", "exec":
				prefix := path.Base(word)
				i++
				for i < len(words) && strings.HasPrefix(words[i], "-") {
					option := words[i]
					i++
					switch {
					case option == "--":
						break
					case prefix == "command" && option == "-p":
						continue
					case prefix == "exec" && option == "-a":
						i++
						continue
					case prefix == "exec" && (option == "-c" || option == "-l"):
						continue
					default:
						break head
					}
					break
				}
				continue
			case "sh", "bash", "zsh", "dash", "ksh", "fish":
				for j := i + 1; j < len(words) && strings.HasPrefix(words[j], "-"); j++ {
					if !strings.HasPrefix(words[j], "--") && strings.Contains(words[j], "c") && j+1 < len(words) {
						if gitCommand(words[j+1], depth+1) {
							return true
						}
						break
					}
				}
			}
			break
		}
	}
	return false
}

func shellAssignmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Split simple shell lists while retaining quoted words as one argument. Only
// a shell's explicit -c argument is inspected recursively; quoted prose is not.
func commandIconWords(command string) [][]string {
	var lists [][]string
	var words []string
	var word strings.Builder
	var quote byte
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	separator := func() {
		flush()
		if len(words) > 0 {
			lists = append(lists, words)
			words = nil
		}
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if quote == '"' && c == '\\' && i+1 < len(command) && strings.ContainsRune("\\\"$`\n", rune(command[i+1])) {
				i++
				if command[i] != '\n' {
					word.WriteByte(command[i])
				}
			} else {
				word.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote, started = c, true
		case '\\':
			if i+1 == len(command) {
				return nil
			}
			i++
			if command[i] != '\n' {
				word.WriteByte(command[i])
				started = true
			}
		case ' ', '\t', '\r':
			flush()
		case '\n', ';', '&', '|', '(', ')':
			separator()
		case '#':
			if started {
				word.WriteByte(c)
			} else {
				for i+1 < len(command) && command[i+1] != '\n' {
					i++
				}
			}
		case '<':
			if i+1 < len(command) && command[i+1] == '<' {
				return nil // Do not mistake here-document contents for commands.
			}
			word.WriteByte(c)
			started = true
		default:
			word.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil
	}
	separator()
	return lists
}
