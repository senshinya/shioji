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

## Follow-up: one composer inset and a real Git question

Home and conversation now use the same left composition: branch control,
message text and attachment control. The visible reading inset is reduced from
approximately 24px to 16px. Home no longer has separate branch/text offsets.
The reading-column test now measures the native text caret, rather than the
outer text-area box. A new home/session alignment regression fails against the
preceding UI source and passes with the shared layout.

The earlier pending-card recording was attached to a non-Git directory; its
missing branch was an evidence-selection problem. The branch renderer already
keeps Git branch controls visible while requests are pending, disabling checkout.
A baseline diagnostic regression confirms this behavior. Pending-card evidence
now comes from a real Git workspace, authoritative local worker and actual pinned
Codex app-server request using the isolated deterministic local provider. The
branch remains visible and disabled; selecting an answer does not submit it,
and its draft survives visiting another session. No production branch is invented
for non-Git folders, and no new paid model request is made.

Tool/usage evidence continues to replay the existing account recording without
adding synthetic questions to its non-Git workspace. This keeps the distinct
recording and live Git-request evidence from implying contradictory branch state.

## Follow-up: toolbar edges and request indicators

Four new pins identify unequal toolbar end spacing, the visible bottom of
Menlo numbers relative to Chinese captions, a high pending-title arrow, and a
missing request icon in the transcript. The conversation toolbar now includes
the existing scroll gutter and the icon's internal inset in its right margin,
bringing its visible ends to approximately 24px. Home/device toolbars retain
their existing geometry.

Footer captions and numbers share 20px lines and baseline alignment; numbers
receive a 1px optical adjustment for the visible Chinese glyph bottom. Duration
numbers and their unit use separate text runs with the same treatment. The
pending-title arrow uses the frozen disclosure path, lowered 2px. Transcript
request events have a question or approval icon, plus a disclosure for pending
or submitting requests. Request lifecycle and answer submission are unchanged.

Light and dark windows are launched separately at 1440×900 DIP. Current native
before/after frames are retained alongside matching frozen references. Request
selection and navigation persistence are rechecked against the real Git worker
fixture; the usage and tool disclosure examples replay the existing account
recording. No paid turn, system appearance preference or credential changes are
needed. UI tests, the signed dev build, repository design-freeze verification,
and diff whitespace checks pass. Visual verdicts remain pending with the user.

## Follow-up: native focus and compact selectors

Seven further pins and inline corrections refine window controls, the footer
and home selectors. Native traffic-light artwork is retained; moving its frame
from (22,24) to (22,21) equalizes the visible red circle's top/left inset. The
preview previously forced only the content's dark flag. It now sets the app's
AppKit appearance too, with an independent native test window taking focus;
light and dark inactive captures both show three system gray circles. The
production app continues to follow the system appearance without a preference
change or replacement window buttons.

The whole footer now aligns by baseline, including duration and the usage
button; the completion icon remains centered. This corrects the offset between
the completion caption and the later Chinese units/captions, rather than adding
more independent number offsets.

The latest comments explicitly replace Codex text in the run summary with the
gray OpenAI Blossom, remove run/branch/home-heading disclosure arrows, and use
an underlined, regular-weight workspace name. These are user-directed updates
to the prior frozen appearance; the frozen files themselves are untouched. The
unchanged official SVG comes from OpenAI's brand download and remains a provider
indicator, not application branding. Its visible artwork is optically lowered
2px beside the model text; the full Codex identity remains in the accessible
control tooltip and Agent menu. Branch text is raised 0.5px to align with its
icon and the worktree control. All three menus retain their native interaction.

Fresh 1440×900 light/dark and 950×650 review evidence replaces the affected
frames. The existing real account recording is reused for tools and usage;
questions, Git and file review use the isolated real worker/app-server fixture.
The full Go suite passed, followed by UI tests and signed dev builds after the
last cosmetic adjustments; design-freeze and diff checks pass. Final visual
verdicts remain with the user.


## Follow-up: shared baseline typography and complete icon audit

The next four pins identify the provider mark sitting low, the Permission
shield sitting high, sinking digits, and the completion color. The user clarified
that the completion shape remains a check, with gray color. Later feedback
expanded the review to all implemented icon/text pairs, search versus native
window controls, and excessive spacing above/between the sidebar actions.

References were checked directly: Apple's [SF Symbols alignment session](https://developer.apple.com/videos/play/wwdc2021/10097/)
uses the baseline and cap-height center; its [monospaced-digit API](https://developer.apple.com/documentation/swiftui/view/monospaceddigit%28%29)
keeps the surrounding system font. The [OpenType baseline documentation](https://learn.microsoft.com/en-us/typography/opentype/spec/base)
distinguishes Roman and ideographic alignment. The implementation applies these
principles to this app's custom SVGs; it does not claim SVGs acquire SF Symbols
metadata automatically.

The previous per-element offsets are removed. MyGo exposes CoreText outline
bounds through Font.InkBounds, measured under the text-system lock with actual
fallback fonts. Native inline objects share the text baseline: Latin icons use
the visible H cap box, while Chinese uses the visible ideographic reference
body. Cached metrics retain the matching ascent/descent, so separate fixed icon
columns and flexible labels have compatible baseline metadata. Decorative
objects stay out of the accessibility tree; their named parent controls remain.

This is applied to navigation, workspace disclosures, completed-session marks,
current-device controls, branch triggers/choices, permission and run controls,
tool groups/items, request events/cards, completion and menu checks. Existing
4px status dots and icon sizes remain. Standalone search, attachment, settings,
send/stop, refresh, close and inspector buttons retain centered button targets.
File review uses these toolbar buttons; file count columns retain their geometry.

Footer duration and usage now use system text with tabular digits (tnum),
without a separate Menlo run or a numeric translation. The completion check is
gray. Per-turn values, unknown fields, ordering and exact-value popovers remain.

Search uses a top-aligned 24px target centered at y=29 DIP with the native
traffic lights. The sidebar header reserves 26px, placing New session at y=47;
28px navigation targets have 4px gaps. A coordinate click in the upper portion
of New session confirms that native titlebar chrome does not intercept it.
These spacing updates follow the user's latest instruction; frozen inputs are
not rewritten.

Native glyph, titlebar and compact-navigation regressions were observed failing
before their fixes. The full Go suite, UI race check, UI vet, signed development
build and repository design-freeze verification pass. Fresh light/dark and narrow
native captures cover the existing controls, menus, real pending requests and
file review. Tool/usage checks reuse the existing account conversation; no new
paid turn is made. User visual verdicts remain pending.
