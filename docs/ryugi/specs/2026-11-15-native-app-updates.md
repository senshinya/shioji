# Native App updates

Approved in conversation on 2026-11-15. Reuse MyGo's updater engine, with no WebView. Following feedback on the initial window, render the session with Shioji's own shared native controls, typography, 16-DIP dialog insets and unboxed Markdown notes; do not use the stock MyGo updater window.

- Add Check for Updates to the App menu and About settings; expose automatic checks using the existing neutral settings controls. Persist updater preferences once and report saving failures.
- Download full, Ed25519-signed updates over HTTPS. Keep Apple Developer ID signing, notarization and stapling. Before staging, verify the candidate's bundle identity, release version, ARM64 executable, minimum macOS version and Developer ID team.
- Preserve the existing draft-flush/quit lifecycle. Updating the GUI never stops workers or the independent Tailnet service; existing worker idle admission remains authoritative.
- Keep VERSION authoritative. Existing release-published CI builds the signed App/DMG, adds a signed tar archive, and uploads its manifest last. Drafts and tags alone do not advertise updates. Development builds do not automatically check.
- Keep the update private key outside Git and logs. Provide a repeatable local configuration command for the GitHub release environment; require matching public/private keys before production builds.
- Verify archive/signature interoperability, invalid keys/packages, cancellation and candidate rejection before installation, settings behavior, and native light-mode evidence. Do not perform a full App build, install, relaunch, or publish a release in this task.

First install of update-capable Shioji is manual. Initial implementation uses full archives; delta updates are not published. Native installation requires a writable installation directory and reports when unavailable.
