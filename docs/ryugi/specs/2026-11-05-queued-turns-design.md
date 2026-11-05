# Queued turns and steering

Status: implemented, pending human acceptance. See [the acceptance checklist](../../queued-turns-acceptance-2026-11-05.md). Automated checks do not mark visual or interaction acceptance as passed.

Approved scope: implement the Queue / Steer / Stop slice proposed on 2026-11-05. The authoritative requirements are AGENTS.md and the operation/queue sections of 2026-11-03-tahuna-implementation-design.md.

Running sessions accept ordinary messages into a durable worker-owned ordered queue. Enter sends while idle and queues while active; Cmd+Enter steers the actual active Codex turn. Empty Enter never interrupts. Pending question forms remain independent. Stop preserves drafts and queued inputs and pauses dispatch. Only a confirmed normally completed turn advances the queue; failure, interruption, worker recovery or changed execution context pauses it until explicit continue. Accepted inputs capture configuration, and uncertain deliveries are never automatically retried.

Queue disclosure attaches above the composer branch strip, starts collapsed, and retains per-session disclosure and editing state. Editing temporarily moves one queued input into the composer, preserving the displaced ordinary draft; save/cancel restores it. Queued inputs stay outside sent history until dispatch. Remove and edit use revisions. A compact action menu offers steering and stopping; empty running composer shows Stop, nonempty shows Queue.

Animate disclosure height and opacity together for 160 ms, with reversal from current progress. Keep the measured composer dock and transcript reservation synchronized, maintain reading/focus, and honor Reduce Motion. Verify the actual native UI with light/dark screenshots; no video.

Out of scope: new harnesses, SSH/tsnet, scheduler resource budgets, unrelated Settings UI. Existing keyboard defaults are retained; configurable bindings remain a separate task. Do not modify the frozen design or user-owned AGENTS.md. Do not merge or push this feature without a new instruction.

Implementation defaults: protocol minor 2 advertises queued turns/Steer; SQLite schema 2 adds the queue projection and rejects downgrade writes. Each session allows up to 32 queued items and 384 KiB of serialized queue state. Queue acceptance and harness dispatch have separate operation identities: durable enqueue confirmation clears the matching draft, while only confirmed harness delivery consumes an item. Known rejected delivery can be explicitly retried; uncertain delivery requires reconciliation. Immediate delivery of one queued item preserves the remaining queue's pause state.

User-approved extension: press and drag a queued row to reorder it, with native pointer-following preview and insertion marker; commit only on drop using stable item IDs and the press-time queue revision. Escape/outside drops and stale queue revisions cancel the gesture. A queue with a dispatching or uncertain item cannot be reordered. Sorting does not resume a held queue or alter input/config snapshots. Double-clicking a row uses the existing composer editing path, retaining the displaced ordinary draft and accessibility via the edit button.

Live sorting refinement: neighboring rows make room during the drag through position transitions. Release immediately retains the visible new order and saves asynchronously, with no user confirmation step. An automatic rollback restores authoritative order on failure. Respect Reduce Motion. Keep the original gesture revision through temporary dispatch locks; drop on the stable list container so animated gaps remain valid targets.
