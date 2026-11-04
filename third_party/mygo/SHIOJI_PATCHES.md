# MyGo native presentation extensions

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
- `ui.ElementTransition.AnchorOrigin`: an attached popup scales around its
  trigger's actual center after viewport fitting, including flipped popups.
- `ui.ListState.KeepRow` / `ReleaseRow`: preserve a clicked disclosure's viewport
  position across structural changes, without disabling virtualization. User
  scrolling and explicit scroll requests release the anchor; collapsing may
  retain trailing reading space instead of jumping the clicked heading.
- `ui.ListState.Reveals` / `ListReveal`: allocate the changing height of a
  contiguous virtualized section. Content retains its natural layout, its
  exposed prefix clips at the animated boundary, and hidden suffix rows are
  skipped. One viewport is measured ahead to stabilize mixed-height content.

Changes are confined to ui/theme.go, ui/paint.go, ui/editor/editor.go and
ui/editor/theme.go, plus ui/glyphs.go, internal/text/layout.go and
internal/text/coretext_darwin.go, ui/layout.go, ui/transition.go, ui/list.go,
ui/list_reveal.go and the
regression tests. The popup and list changes are covered by Shioji's native
component interaction regressions. Remove the replacement when an
upstream release exposes equivalent controls. This remains a native renderer;
no HTML or WebView is introduced.
