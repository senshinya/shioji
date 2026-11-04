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

Changes are confined to ui/theme.go, ui/paint.go, ui/editor/editor.go and
ui/editor/theme.go, plus the regression test. Remove the replacement when an
upstream release exposes equivalent controls. This remains a native renderer;
no HTML or WebView is introduced.
