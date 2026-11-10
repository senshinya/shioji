# Session subagents and background work

Approved in chat on 2026-11-10 after splitting the session-activity artboard:
upper Subagents, lower Background tasks; each is an independent view with its
own list, selection, details and reading position. Changes remains a tab in the
upper group and structured Tasks remains in the lower group. All four built-in
views may move between the two existing groups or one merged group. Defaults
split equally; do not introduce a general docking framework or a second sidebar.

## Data and ownership

- Extend the harness-neutral domain with bounded work metadata, work-item events
  and read-only work details. The roster returns up to 128 metadata summaries / 96 KiB, prioritizing active work; omitted older records remain persisted and explicit truncation is shown. Work belongs to one authoritative worker session,
  records the spawning root turn/source item and optional parent work, and never
  creates an application session/workspace just to render a child.
- Persist work independently of the main transcript. Child items cannot alter
  parent execution, usage or title, or appear as duplicate main-agent prose.
  History replacement removes work owned by removed root turns. Restart, checkout and runtime replacement wait while independent work is active. Main turn
  completion does not complete child agents or commands. Runtime loss/offline
  makes active observations unknown while preserving terminal results and logs.
- Translate concrete thread/process identities and states in the adapter. Shared
  code treats refs as opaque. Discover actual child links from structured events
  and actual background execution evidence, not tool-return completion or prose.
- Detail reads operate only on the owning session's known work. They do not start
  or resume a harness/thread or grant additional paths. Stop appears only for a
  concrete, validated per-work control capability; unsupported control remains
  unavailable, rather than substituting parent interrupt or killing a guessed PID.
- Preserve already reported legacy AgentActivity records as summary-only entries
  where no richer record exists. Never manufacture missing child history/output,
  model, timestamps or elapsed execution time.

## Native interface

- Full-height single right column; 28 px draggable icon tabs / 16 px aligned
  glyphs, common system font/gray tokens. Keep toolbar refresh/close fixed.
- Subagent and background rows have a fixed first-line state slot, title and
  elapsed/status column; second line only for actual latest action/command.
  Running uses the existing green breathing dot and text Shimmer; completed uses
  a gray check. Keep identity/order stable during updates, with bounded lists.
- Details slide horizontally inside their own group, including their Back/name
  row, while the other group and main conversation remain untouched. Each view
  retains selection/scroll during tab switches, regrouping and session navigation.
- Render child messages/tools with native components. Commands use existing
  incremental output rendering; append only new output, follow the last line
  only while already following, pause when reading older output, and provide the
  existing compact latest-output affordance.
- New records enter with opacity and real-height motion; changes to a row do not
  recreate the row. Split/merge animates actual group height; selection/details
  move horizontally with no navigation fades. Honor Reduce Motion.
- Opening the inspector remains explicit. New work does not steal an existing
  Changes/Tasks/detail selection. On a fresh work-bearing session show the
  approved upper/lower work views; preserve explicit client layout thereafter.
- Narrow mode uses the existing sole sidebar drawer. No new composer/toolbars or
  secondary explanation text. Main transcript retains lightweight source events.

## Verification

TDD for source/state/ownership/persistence and migration; focused native UI tests
for independent views, selection/scroll retention, drag/drop, unknown/offline,
history reset and completed-parent/live-child cases. Use an isolated real Codex
app-server with local deterministic model responses for runtime shape/lifecycle
checks, no paid turns or user credential changes. Capture matching light/dark
native screenshots at 1440x900 and supported minimum size, add them beside the
approved boards on the existing local artboard. No recording/cloud publication.
