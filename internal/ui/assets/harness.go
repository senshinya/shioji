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
