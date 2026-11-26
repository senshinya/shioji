package assets

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

// DeepSeek official brand asset, with the original shape and theme-controlled ink.
// https://github.com/deepseek-ai/awesome-deepseek-integration/blob/main/docs/_logo%20svg/ICON.svg
//
//go:embed deepseek-monochrome.svg
var deepSeekSVG []byte

var DeepSeek = ui.MustParseSVG(deepSeekSVG)

// Claude's original mark with theme-controlled ink.
// Source: https://github.com/pingdotgg/t3code/blob/cef38e408d45b74a02f99da0ed3d400d97fd4182/apps/marketing/public/harnesses/claude-ai-icon.svg
//
//go:embed claude-monochrome.svg
var claudeSVG []byte

var Claude = ui.MustParseSVG(claudeSVG)

const DefaultProfileIcon = "openai"

var ProfileIconChoices = [...]struct{ ID, Name string }{
	{DefaultProfileIcon, "Codex"},
	{"deepseek", "DeepSeek"},
	{"antigravity", "Antigravity"},
	{"claude", "Anthropic"},
}

var profileSymbols = map[string]*ui.SVG{
	"profile-sliders":  profileSymbol("M3 6h14M3 14h14M6 4v4M14 12v4"),
	"profile-terminal": profileSymbol("M3 4h14v12H3zM6 8l2 2-2 2M11 12h3"),
	"profile-code":     profileSymbol("m7 6-4 4 4 4m6-8 4 4-4 4m-2-10-2 12"),
	"profile-cube":     profileSymbol("m10 3 6 3.5v7L10 17l-6-3.5v-7L10 3ZM4 6.5l6 3.5 6-3.5M10 10v7"),
	"profile-spark":    profileSymbol("M10 3c.5 4 3 6.5 7 7-4 .5-6.5 3-7 7-.5-4-3-6.5-7-7 4-.5 6.5-3 7-7Z"),
	"profile-bolt":     profileSymbol("m11 2-7 10h5l-1 6 8-10h-6l1-6Z"),
	"profile-orbit":    profileSymbol("M10 8a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM4 14c-2-2 1-6 4-8s7-2 8 0-1 6-4 8-7 2-8 0Z"),
}

func ProfileSymbol(id string) *ui.SVG { return profileSymbols[id] }

func profileSymbol(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round" stroke-linecap="round" d="` + path + `"/></svg>`))
}
