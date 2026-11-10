# Workspace MCP inspection

Approved in the workspace-mcp artboard, including its corrected right edge.

- Native workspace settings: General / Skills / MCP. Reuse existing horizontal content and marker transitions. List search, source groups, directory selection, detail, path copy and refresh. Align trailing status/control slots with the header and rules. Keep the list full-height and scroll internally.
- Only inspect a saved owning-device workspace folder, or a session's accepted execution directory. Never merge all additional folders into one configuration. Session selection stays within the workspace and harness.
- Project configurations remain read-only. Inherited user configuration navigates to the existing device Agent MCP page and returns with workspace draft, selection, detail, search and reading position preserved; reload on return. No setup/onboarding MCP entry.
- Read actual source layers via the harness adapter. Show disabled/untrusted sources and parse/read failures without claiming adoption. Do not export secret values. Configured and disabled labels describe configuration; connection/tool states require the selected live session's own observation.
- Read-only queries neither reload MCP connections nor create/resume a session. Unsupported scoped discovery and absent runtimes yield unknown state, never device/global status as a substitute.
- Worker validates workspace, folder, session and harness ownership; UI guards callbacks by query context, device epoch, workspace revision and installation configuration. Minor protocol gate prevents an older worker from silently treating a scoped request as global.
- Verify actual Codex project configuration/trust and native schema support, worker ownership and no-start behavior, stale UI replies/navigation retention, light/dark/narrow native captures, motion, full Go checks and frozen design baseline. Screenshots only, no recording or cloud publication.

## Detail layout A (approved 2026-11-10)

Use the selected `detail-grid` board on the `detail-redesign` artboard page.
Service name at 16 px with its state immediately alongside; inherited management
shares that header's trailing edge. One separator below the header, no per-field
form outlines. Metadata uses a fixed 88 px label column, 16 px gap, flexible value
column and 28 px copy-action slot. Rows have 28 px line height, 4 px vertical
padding and 4 px separation. Paths may wrap; labels and copy icons stay at the
first line. Text values are 13 px, code values 12 px. Preserve actual source,
partial configuration, hidden-secret and session-observation semantics, including
individually copyable current source paths. Keep existing full-panel horizontal
navigation, copy confirmation and Reduce Motion support. Verify both appearances,
850×600 and 1240×820, inherited HTTP, partial source, session tools and long paths.

Follow-up: place the Back arrow immediately before the service name in the same
header row, replacing the separate MCP return row. Keep a 28 px hit target,
font-metric icon alignment and accessible Back label, including missing sources.
