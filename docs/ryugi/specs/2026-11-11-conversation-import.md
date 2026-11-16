# 已有会话的发现与导入

Approved artboard: `.ryugi/artifacts/artboard/session-import/canvas.json` (welcome, workspace, dark, partial, empty). User approved the layout and requested implementation on 2026-11-11.

## Product behavior

- Reuse a native import module after workspace admission in the welcome flow and from the workspace menu. The welcome import step is optional: Skip finishes setup, retaining the home draft and creating no empty session. Scanning and failures never prevent Skip.
- Bind discovery to the owning device, selected harness, workspace revision and canonical folder membership. Do not search the controlling Mac for remote workspaces. Search/filter preserves selection; default selection is empty. Results page/scroll within a bounded height, with stable bottom actions.
- Show title, updated time, source harness and imported/read-only status. Checkbox selection and title preview are separate actions. Already imported sources open the existing session instead of creating duplicates. Preview is read-only and cannot resume or execute source work.
- Import selected conversations independently. Persist each successful import atomically, report partial failures, and retry only unsuccessful sources. Retrying or reconnecting cannot duplicate a source. Imported history belongs to the worker and opens through ordinary authoritative session loading.
- Preserve normalized user/assistant history and available tool content without reactivating historical requests, running tasks, notifications, usage or runtime ownership. Bound discovery and preview pagination/count/bytes. Formal imports read complete source history on the owning worker and expose it through ordinary paged history; exceeding the full-import capacity fails explicitly rather than creating a truncated read-only record. Legacy partial records can be explicitly completed in place with workspace/session revision checks, preserving identity, title, configuration and client drafts (2026-11-16 user correction).
- Continue only when the adapter can prepare an independent execution context from the exact imported prefix. Never attach to or commandeer a CLI/other client's process. Missing or changed source history, incomplete/running imports and unsupported runtime capabilities leave history readable and disable submission. New submissions still go through ordinary directory/Git/model/permission validation.

## Architecture

Neutral typed conversation catalog, preview, origin, import input/results in domain/protocol. Optional harness reader and continuation interfaces translate native history and prefix identity. Shared code must not parse those opaque identities. Codex uses supported app-server history APIs; no ad-hoc rollout filesystem parser. Catalog reads do not create/resume execution instances.

The first actual submission of a resumable import prepares a private source fork, releases it, persists its identifier, then resumes it through the managed session runtime. Adapter-owned prefix identity pins the imported content; changed history rejects continuation. Unknown preparation delivery is not automatically repeated.

The worker owns scope validation, durable source deduplication and import transactions. Imported source identity is scoped by harness and device-owned workspace, never by title. The UI guards late callbacks by module instance, device identity, connection epoch, workspace revision and query generation.

## Native UI and motion

Use existing form, tab, typography and dialog controls, cool monochrome tokens and 560 DIP content. Welcome steps, list/preview and horizontal filters slide as one unit for 260 ms EaseOut, with no opacity swap. Actions remain outside sliding content. Lists scroll internally; actions remain visible. Loading/status updates preserve one rotating glyph. Changes use existing content/height transitions, and reduced motion switches immediately.

## Verification

TDD for adapter source discovery and read-only history; durable import deduplication and scope rejection; real worker RPC and safe continuation; native welcome skip, stale callbacks, selection/filter/preview and partial retry. Run the Go suite and design freeze verification. Build and capture the actual native app in bright/dark modes; publish evidence only on the existing local artboard Verify page, never as HTML pretending to be native output.

Sources: [OpenAI app-server](https://learn.chatgpt.com/docs/app-server), Lody `5da9b1943141d5318f1f6c86f17f6708d5d601a1`, T3 Code `a03d9bce026ef11930b35e9855adc1cdb5ea6eb7`. Reference render provenance is recorded in the approved artboard.
