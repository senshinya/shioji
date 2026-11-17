# MyGo native presentation extensions

Upstream: `github.com/egoist/mygo` v0.7.4. The upstream LICENSE is retained.

Client settings additionally use `ui.Context.ReduceMotion` to request less
motion for the current frame, including native scrolling and Markdown. It
preserves the desktop preference and cannot disable system Reduce Motion.

Notification registration rechecks cancellation while holding the registry
lock. This prevents a canceled delivery from restoring its callback after
the client retires it; covered by `notification_registration_test.go`.
This local module contains the upstream runtime source and embedded resources;
examples, website, JavaScript tooling, CI, upstream tests and testdata are omitted.
The upstream architecture guide is retained for maintenance.

Rich-text editors explicitly clip their own paint callback to the scroll
viewport. Element `Clip()` only clips descendants, so a soft-wrapped paragraph
otherwise paints over composer footer controls. Covered by Shioji's
`internal/ui/composer_overflow_test.go`.

Scroll containers show their overlay thumbs only after the offset actually
changes or while a thumb is dragged. Hover and initial history placement do not
reveal them; after 700 ms without movement they fade over 200 ms. Geometry and
the gutter remain unchanged. Covered by `ui/scrollbar_activity_test.go`.

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
  scrolling and explicit scroll requests release the anchor. `KeepRowWithinContent`
  additionally clamps disclosure anchors to the natural content boundaries:
  a shrinking section can move its heading to avoid trailing blank space.
  Unbounded reading/sent-message anchors retain their temporary reading space.
- `ui.ListState.ScrollToRowWithinContent`: animate source navigation within the
  natural content boundaries, so a subsequent wheel movement cannot remove
  artificial trailing space and jump. Covered by Shioji's
  `internal/ui/work_source_scroll_test.go`, including folded tool sources.
- `ui.ListState.ScrollToEnd`: accepts an optional animation for an interruptible
  glide from the current reading position to the measured end, without mounting
  intervening history. Appended rows retarget it without moving the current
  viewport; completion resumes ordinary end following. The no-argument form
  retains its immediate behavior. Covered by `ui/list_reading_motion_test.go`.
- `ui/markdown.Options.OnRevealState`: report native buffered-text and fade
  activity so the visible conversation can defer its sidebar check until output
  settles, independently of authoritative execution completion.
- `ui/markdown.Options.LinkContextMenu`: extend the native context menu of the
  exact completed link under the pointer, preserving its destination and normal
  left-click behavior. Shioji uses the hook for device-scoped port forwarding;
  pending links retain their parent menu. Covered by Shioji's native Markdown
  link-context regression.
- `ui.ElementTransition.From` / `Lift`: retain a source rectangle through
  asynchronous acceptance and move sent text out of the editor into history.
  Lift only the moving message above ordinary content, below popovers, keeping
  its native input ownership. Respect Reduce Motion and retain list virtualization.
- `ui.ListState.AnchorRow`: move a just-sent message into reading position with
  an interruptible native scroll animation and temporary reply space. Filled
  space resumes end following; a reader who scrolls away keeps their position.
  Omitting its optional animation adopts an already-positioned row directly.
  History replacement uses that form when the accepted input replaces the local
  preview, so prefix remeasurement cannot replay the send glide.
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
- Ordered Markdown lists measure their marker column with the same `tnum`
  font feature used to paint it. Proportional-digit measurements otherwise
  wrap the punctuation of two-digit markers during streaming, until a wider
  final number happens to enlarge the column. Covered by Shioji's streamed
  ordered-list regression in both appearances.
- Preserve the previous frame's untracked-paint flag before resetting the
  repaint bookkeeping. A time-varying shader effect must repaint its layer even
  when another tracked Draw callback (such as the activity dot) also animates;
  partial replay alone would leave that effect frozen. Covered by Shioji's
  active-task/footer shimmer regression, including Reduce Motion.

- `ui.Inert`: retain an outgoing pane's exact paint while removing pointer,
  keyboard and accessibility interaction. Reuse the renderer's existing inert
  flag; propagate disabled input state without Disabled's 50% paint dimming,
  including stale accessibility references. Covered by pane interaction and
  Shioji question-card light/dark pixel regressions.
- `ui.PointerPressedWithin`: observe descendant/popup presses without turning a
  pane into a clickable container or stealing text-input focus.
- `ui.ListState.FollowingEnd`: distinguish automatic initial/end positioning
  from explicit history reading when showing the return-to-latest control.
  Pending and active `ScrollToEnd` glides count as following, so an accepted
  send does not flash the control before reaching the new end. Reader scrolling
  still interrupts following, covered by `ui/list_reading_motion_test.go` and
  Shioji's `internal/ui/latest_send_test.go`.
- `ui.ListState.FollowAnimation` measures growth below a visible keyed row,
  including content above fixed trailing footers. `FollowInset` excludes
  independently animated composer/request space from that glide, keeping the
  followed progress above the dock throughout its height animation. Prepended
  history does not replay unchanged content. Covered by `list_follow_growth_test.go`
  and Shioji's live-follow, reader-interruption and history-reopening regressions.
- `ui.VirtualTextSelection` / `SelectionItem`: opt-in retention of a message's
  selected paragraphs and rune offsets across real virtual-row unmounts. Stable
  model keys and current source text invalidate removed/replaced content; remount
  restores the selection, while another editor retains its own focus and Copy.
  The list releases the selected focus row instead of keeping its UI mounted.
  Optional retention code is registered through lightweight hooks, and is absent
  from linker dependencies when unused. The vendored checkout omits upstream
  examples, so equivalent minimal native/webview probes were built against HEAD
  and the modified tree: native 9,390,034 → 9,390,210 bytes (+176 bytes of hook
  plumbing), webview 7,011,490 → 7,011,490 bytes. The full retention implementation
  is excluded without opting in (`-dumpdep` checked).

- `ui/richtext/input.go`: editable fields consume plain/Shift Space keydown
  before ancestor page scrolling; the separate text event inserts the space
  exactly once. Covered by Shioji's history editor regression test.
- `ui/richtext.EditorOptions`: plain composer mode with pre-key/paste hooks,
  bounded content height and fixed line advance. Native attributed attachments
  retain selection, IME, wrapping and undo metadata; optional descent aligns
  compact inline labels, and attachment click/tooltip use shaped object bounds.
  `ui.TextShape.LineHeight` exposes the same fixed leading to retained layouts.
  Covered by Shioji's file/Skill completion, atomic editing/undo, copy, reload,
  queue-position, attachment-update and delayed-result regressions.

- `ui/layout.go`: after element transitions, reposition only overlays attached to
  moving targets, preserving their animated size/offset and descendants. Do not
  relayout dialog subtrees or outgoing snapshots after animation has placed them.
  Covered by Shioji's whole-page dialog/add-device Tab and moving-anchor popup
  regressions in `internal/ui/configuration_tabs_test.go`.

Changes are confined to ui/theme.go, ui/paint.go, ui/editor/editor.go and
ui/editor/theme.go, plus ui/glyphs.go, internal/text/layout.go and
internal/text/coretext_darwin.go, ui/layout.go, ui/transition.go, ui/list.go,
ui/list_reveal.go, ui/runtime.go, ui/markdown/code.go, ui/markdown/reveal.go,
ui/markdown/smooth.go and the
regression tests, plus ui/pane.go, ui/virtual_textselection*.go and their
ui/context.go, ui/element.go, ui/input.go and ui/textselection.go hooks, plus
ui/richtext/{view,input,document,text,clipboard,paint}.go and ui/text_layout.go
for native composer attachments. The popup and list changes are covered by Shioji's native
component interaction regressions; streaming code is covered by the native
reply regressions in internal/ui/streaming_test.go. Remove the replacement when an
upstream release exposes equivalent controls. This remains a native renderer;
no HTML or WebView is introduced.

Read-only unified Diff additionally supports exact old/new text fragments,
including deleted blocks and partial lines, native copy, wrapped-row keyboard
selection, and restoring disjoint quotes after context expansion. Buffer keeps
the exact last loaded/set source for selection offset mapping; its editable
and save normalization remains unchanged. Read-only Diff hides insertion
carets, while editable editors keep theirs. Full-width annotations let Shioji
place compact inline comments. Regression tests live in
`ui/editor/diff_selection{,_regression}_test.go`.
