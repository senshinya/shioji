# Device storage management

The approved storage artboard is `.ryugi/artifacts/artboard/storage-management`.
The completion result replaces the cleaning label in the same trailing action
slot; it never creates a second success row. Keep the 40-DIP row and baseline
stable, transition the label and capacities, and only expand failure details.

Device settings shows owning-worker storage independently of this client's
display cache. Filesystem work runs outside UI rendering. Device requests are
bound to the saved device and connection epoch. An unavailable or old worker
does not report zero bytes. Repeated cleanup clicks are blocked while pending.
Failures remain visible with an inline retry and expandable reasons.

Worker cleanup only removes incomplete attachment upload bytes whose last write
was more than seven days ago, and recognized rotated worker logs. Small identity
manifests remain so retries cannot silently change an attachment's identity. Complete uploads,
session databases (including WAL), history, harness state and current logs are
retained. There is currently no worker preview cache or log rotation writer;
their measured reclaimable size is zero, rather than invented sample data.
Incomplete uploads are labeled accurately as expired incomplete uploads.

The client separates newly downloaded display copies into `cache/attachments`.
Existing `attachments` remain protected because they mix uploads and old cached
downloads without reliable provenance. Client cleanup touches only the dedicated
cache, serialized with reads, downloads and Save As. Cached files can be fetched
again from their owning worker. Active reads and original uploads remain intact.

No recursive deletion of user directories, symlink traversal, database vacuum,
worktree cleanup, service restart or credential changes are part of cleanup.
Reports count successful file removals; failed removals remain in the measured
usage. Per-file failures are bounded and use relative names without file contents.

Native verification covers light/dark, first-line icon alignment, stable action
row geometry, inline running/success/retry, stale responses, device isolation,
and cache cleanup preserving original draft attachments.
