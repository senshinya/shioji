# Skills management

Approved in the Skills artboard and conversation on 2026-11-10. Artboard: `.ryugi/artifacts/artboard/skill-management/canvas.json` (ignored local design input).

## Confirmed behavior

- Native Go/MyGo UI. Device > Agent > Skills appears after Models, before MCP, only in device settings (not onboarding). Shows user/built-in/managed sources, never workspace switching.
- Workspace menu > Workspace settings adds General and Skills tabs. Skills binds the owning device, saved workspace main directory and selected supported harness. Group by scope and source directory; preserve main/additional folders and unsaved General edits. Inherited user/built-in sources are read-only, with navigation to device management and back retaining search, list/detail reading position.
- List search, refresh retaining existing rows, enabled/disabled entries, source-scoped warnings, empty/offline/read-only states. Detail reads actual offered Skill content on owning device, read-only Markdown plus full path and copy control. No install, edit or inferred directory scanning in shared code.
- Enable only when actual adapter/native configuration supports it; do not pretend user-owned changes are workspace-local. Confirm save, retain old value on failure; revision protects concurrent edits; unknown save must be refreshed, never automatically replayed.
- Existing `/` menu stays enabled-only with typed opaque SkillReference. Extra directories do not automatically widen skill access.
- Shared contract is harness-neutral. Adapter owns native scope/path interpretation, encoding, config policy and actual file reads. Worker owns target selection, scope validation and serialization. Unsupported harness capabilities stay unavailable.
- Whole horizontal panel transitions, moving tab marker, actual height for status changes; preserve input/query and reading anchors. Respect Reduce Motion and existing font/icon baselines. Workspace toolbar directory popup pulls down from the trigger and retracts up with opacity fixed at one (no fade). Sidebar click still collapses, context menu remains anchored.

## Verification

Actual isolated Codex process/native files; stale/forged references, inherited writes rejected, stale revisions and context, content read limits; native UI tester for navigation, pending/error/offline states, split contexts, positions, animation intermediate frames. Full Go suite, schema compatibility, frozen design check, native screenshots in both themes alongside approved boards. User requested screenshots only. No acceptance data or credentials added to Git.
