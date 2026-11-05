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
- `ui.ElementTransition.From` / `Lift`: retain a source rectangle through
  asynchronous acceptance and move sent text out of the editor into history.
  Lift only the moving message above ordinary content, below popovers, keeping
  its native input ownership. Respect Reduce Motion and retain list virtualization.
- `ui.ListState.AnchorRow`: move a just-sent message into reading position with
  an interruptible native scroll animation and temporary reply space. Filled
  space resumes end following; a reader who scrolls away keeps their position.
- `ui.ListState.Reveals` / `ListReveal`: allocate the changing height of a
  contiguous virtualized section. Content retains its natural layout, its
  exposed prefix clips at the animated boundary, and hidden suffix rows are
  skipped. One viewport is measured ahead to stabilize mixed-height content.
- Native Markdown offers an opt-in adaptive stream buffer measured in Unicode
  characters, with arrival-rate estimation, a short live reserve, per-character
  fade timestamps and a bounded completion drain. History and reduced-motion
  rendering bypass the buffer.
- Native Markdown code blocks apply the stream reveal clock to highlighted
  source spans as well as prose. Newly appended code fades without replaying
  existing lines, retokenizing on animation frames, or changing copied code.

Changes are confined to ui/theme.go, ui/paint.go, ui/editor/editor.go and
ui/editor/theme.go, plus ui/glyphs.go, internal/text/layout.go and
internal/text/coretext_darwin.go, ui/layout.go, ui/transition.go, ui/list.go,
ui/list_reveal.go, ui/markdown/code.go, ui/markdown/reveal.go,
ui/markdown/smooth.go and the
regression tests. The popup and list changes are covered by Shioji's native
component interaction regressions; streaming code is covered by the native
reply regressions in internal/ui/streaming_test.go. Remove the replacement when an
upstream release exposes equivalent controls. This remains a native renderer;
no HTML or WebView is introduced.
