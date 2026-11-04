# Native appearance review, 2026-11-04

The frozen design remains unchanged. This patch addresses the four open visual
comments among 17 total threads, plus the requested color/type audit and flat
native window controls. Visual verdicts remain pending on the local artboard.

## Scope and implementation

- System UI text uses grayscale coverage to match the frozen prototype's
  antialiased text. Native input, rich text and editor rendering remain native.
  No system or user font-smoothing preference is changed.
- Frozen neutral light/dark surfaces, muted text and icons are applied explicitly.
  User bubbles use the separate 5% text-over-canvas surface. Home input is 15/26;
  conversation input 14/22; body 14/24; tool labels/results and usage counters 11px.
  Duration and token counters use Menlo, independently of their sans-serif labels.
- Diff foregrounds and exact line/gutter fills are independent: light add/delete
  #45634f / #90564f over #f0f5f1 / #faf1ef; dark #99ada0 / #c39b94 over
  #252e28 / #332928. Native diff uses Menlo 11px with 23px line height.
- Send uses the frozen rounded stroke arrow, a 20px icon slot and 28px circular
  target. The disabled fill is the subtle color at the control's disabled opacity.
  Request headings have icons; attachment plus is visible but disabled because
  the current Codex input adapter supports text only.
- Choice menus share 26px rows, 13px names, neutral focus and bounded scrolling.
  Stable entity IDs distinguish repeated names. Navigation closes the old menu.
- Tool groups close once on later nonempty assistant prose. User messages, empty
  prose, completion and late output do not close them. Explicit disclosure is
  persisted; expanded children remain separate outer virtual-list rows.
- App packaging opts into AppKit compatibility appearance via
  UIDesignRequiresCompatibility. Actual macOS 27.2 testing with SDK 26.2 confirms
  flat system traffic lights. Close/minimize/zoom remain AppKit standard buttons.
  Recheck when advancing the SDK: [Apple documents the compatibility key](https://developer.apple.com/documentation/bundleresources/information-property-list/uidesignrequirescompatibility) as
  temporary, ignored for applications built with SDK 27 or later.

## Native dependency extension

`third_party/mygo` retains runtime source from v0.7.4 with four modified upstream
files, one regression test and provenance notes. No new dependency version is
introduced. See SHIOJI_PATCHES.md in that module for the two opt-in appearance
controls and their removal path. Comparison against upstream found no other
source changes. SDK/platform interop is untouched.

Release-style darwin/arm64 builds of upstream's two examples, before/after:

| Example | Upstream bytes | Patched bytes |
| --- | ---: | ---: |
| counter-native | 9,389,762 | 9,389,778 |
| hello (does not use native UI) | 9,578,370 | 9,578,370 |

The native example grows 16 bytes with the added theme flag/branch; no additional
package, asset or backend feature becomes reachable by the webview example.

## Evidence and checks

Actual MyGo windows were captured at 1440×900 DIP, plus 950×650 narrow dark review.
Each frozen comparison uses the same appearance and logical size. Native PNGs
carry monitor ICC data, so evidence is converted to sRGB before comparison;
raw captures are retained locally. Eight-bit ICC conversion can round channels
by one level; application tokens are taken from frozen CSS rather than adjusted
for the monitor. No screenshot content is retouched or reconstructed.

Tools and account usage replay an existing real account conversation; Git,
search and file review use a real local worker, pinned app-server and deterministic
local provider. No additional paid account turn is needed. Short tool disclosure
video is explicitly a sequence of actual native operation screenshots.

Checks: full project Go tests, UI/application race checks, UI/application vet,
explicit native diff fill regression, dev app build/signature verification and
repository design-freeze verification. The first full run encountered a worker
test temporary-directory cleanup failure (directory not empty); the individual
worker test and subsequent complete test runs passed. No worker code changed.

Only local/ LAN artifact hosting is used. Frozen inputs, original account
recordings, screenshots and generated state are not added to Git. User changes
to AGENTS.md remain outside this patch.

## Follow-up: sidebar and inline controls

The next six visual pins identified sidebar header spacing/alignment, workspace
chevron position, session indentation, the Permission shield shape, and branch
text/icon/chevron alignment. The original 24px frozen SVG paths now replace the
approximate shield/branch paths and character-based workspace arrows. Workspace
headers use 30px rows with a centered 20×24 disclosure slot; the section caption
is inset 8px and lowered 4px without moving the list. Session left padding is
reduced from 24 to 20px, and its icon/text gap from 8 to 6px. Inline controls use
explicit 20px text lines and centered icon boxes, including the worktree check.
The home branch strip retains the frozen 10px inset inside the composer.

The rejected screenshot and a fresh native before capture are retained locally.
Light/dark native frames are replaced in the existing verification groups;
workspace collapse/expand, branch disclosure and Permission disclosure are checked
in the running native app. Project tests, build and frozen-design check pass.

## Follow-up: composer and request panel

Four further pins refine the conversation composer and question card. The
conversation input text, branch path's visible edge and attachment plus now
share a left edge; home keeps its established context geometry. Placeholder
colors are explicit frozen subtle values (#707070 light, #969696 dark), while
entered text keeps the primary color. Completed sidebar ticks use the same
appearance-specific success green as turn completion.

Per the new comment asking for one corner radius, the pending panel, option
surface, custom answer and action buttons use 8px. The white option surface uses
8px horizontal insets; centered 12px radio controls in 28px rows provide matching
8px first/last vertical insets. Outer card padding uses the established 12/14px
insets. Question selection/submission and ordinary draft state are unchanged.
Native before/after evidence is retained in the existing conversation and pending
verification groups. Dark request-panel evidence is shown independently, without
substituting a light reference. Existing real conversation recordings are reused.
