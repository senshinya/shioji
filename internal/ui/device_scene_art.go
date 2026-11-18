package ui

import (
	"embed"
	"strings"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

// Approved front-view geometry. Text, lifecycle indicators and all interaction
// remain native elements, independently of vector scale and SVG parsing.
//
//go:embed assets/device-scene/*.svg
var deviceSceneAssets embed.FS

var deviceSceneArtwork = func() [2]map[string]*n.SVG {
	palettes := [][11]string{
		{"#e8e8eb", "#f4f4f6", "#939399", "#c7c7cc", "#fcfcfc", "#d4d4d9", "#56565e", "#68686f", "#e6e6ea", "#48484f", "#fcfcfc"},
		{"#35353a", "#252528", "#73737d", "#414148", "#2d2d31", "#505058", "#cacad1", "#a3a3ae", "#35353b", "#35353b", "#202022"},
	}
	keys := []string{"@case@", "@screen@", "@edge@", "@display@", "@window@", "@inner@", "@ink@", "@quiet@", "@faint@", "@bezel@", "@background@"}
	var result [2]map[string]*n.SVG
	for theme, palette := range palettes {
		result[theme] = map[string]*n.SVG{}
		pairs := make([]string, 0, len(keys)*2)
		for i, k := range keys {
			pairs = append(pairs, k, palette[i])
		}
		replace := strings.NewReplacer(pairs...)
		for _, name := range []string{"controller", "target-monitor", "target-laptop", "target-server", "target-phone", "target-tablet"} {
			source, err := deviceSceneAssets.ReadFile("assets/device-scene/" + name + ".svg")
			if err != nil {
				panic(err)
			}
			art := replace.Replace(string(source))
			result[theme][name] = n.MustParseSVG([]byte(strings.ReplaceAll(art, "@port-display@", "inline")))
			if name != "controller" {
				result[theme][name+"-without-port"] = n.MustParseSVG([]byte(strings.ReplaceAll(art, "@port-display@", "none")))
			}
		}
	}
	return result
}()

func deviceSceneTarget(t *n.Theme, icon app.DeviceIcon, withPort bool) *n.SVG {
	index := 0
	if t.Dark {
		index = 1
	}
	name := "target-" + string(icon)
	if !withPort {
		name += "-without-port"
	}
	return deviceSceneArtwork[index][name]
}
