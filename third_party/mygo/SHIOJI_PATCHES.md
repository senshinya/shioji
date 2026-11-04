# MyGo native appearance extensions

Upstream: `github.com/egoist/mygo` v0.7.4. The upstream LICENSE is retained.
This local module contains the upstream runtime source and embedded resources;
examples, website, JavaScript tooling, CI, upstream tests and testdata are omitted.
The upstream architecture guide is retained for maintenance.

Shioji uses this local replacement because the public native text/diff API does
not expose the following appearance settings required by the frozen design:

- `ui.Theme.FontAntialiased`: opt into grayscale glyph coverage (CoreText font
  smoothing otherwise changes apparent weight). Applied to plain/rich text and
  inherited by native editors. No system or user font preference is changed.
- `editor.Theme.AddedBackground` and `DeletedBackground`: optional exact line
  and gutter fills. Zero keeps upstream tint calculation; semantic foregrounds
  remain independent. Covered by `shioji_theme_test.go`.
- `ui.Font.InkBounds`: baseline-relative, visible outline bounds for single-line
  text, including CoreText's font fallback. This lets native inline objects
  align with Latin capitals or a Chinese body instead of a line's leading.
  Outline measurement is supported by the macOS text engine; other engines
  return an empty rectangle. Shaping and measurement share the text-system lock.
  Covered by Shioji's native font-metrics regression.

Changes are confined to ui/theme.go, ui/paint.go, ui/editor/editor.go and
ui/editor/theme.go, plus ui/glyphs.go, internal/text/layout.go and
internal/text/coretext_darwin.go and the regression tests. Remove the replacement when an
upstream release exposes equivalent controls. This remains a native renderer;
no HTML or WebView is introduced.
