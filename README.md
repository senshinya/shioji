# Shioji

Stable release **v0.9.0**. See the [changelog](CHANGELOG.md) and [signed DMG publishing and GitHub setup](docs/releases.md).

Native Apple Silicon macOS controller for device-owned coding-agent workspaces.
The developer build runs a self-contained local Go worker and an installed Codex CLI.
Device management supports application display names for both this Mac and SSH
devices, and device-owned Agent selection/executable configuration. Adding an SSH
device checks its selected Agents before saving the connection. It never changes
OS hostnames or installs a third-party Agent. Supported Agent sign-in can be
explicitly started from the account section; authorization and credentials stay
on the owning device, including for SSH devices.
The adapter checks the installed App Server protocol and exposes its supported capabilities rather than requiring one exact CLI version. The recorded protocol baseline remains 0.166.0; the current adapter also passes live tests with 0.167.0.
It uses MyGo native UI and SQLite; no WebView or herdr-core runtime is required.

```sh
bash packaging/build-dev.sh
open build/Shioji.app
```

Go 1.27.3 is pinned by `go.mod` and downloaded by Go's toolchain mechanism if needed.
An existing compatible Codex installation is required; Shioji does not install
or overwrite your Codex. Sign in from device configuration when needed. Create a workspace with the native
folder picker, then submit a nonempty prompt from Home. Cmd+N returns to Home.

For terminal development:

```sh
env -u GOROOT go run ./cmd/shioji-worker
# Separate terminal; starts/connects to the same user-owned worker:
env -u GOROOT go run ./cmd/shioji --no-start-worker
```

The app bundle contains separately built GUI/worker executables. The development
launcher starts its worker independently of the window. Closing the GUI does not
stop it. The bundled Shioji Runtime.app can register a user background service
through the device page’s 登录后启动服务 switch; macOS may require approval.
Turning it off waits for authoritative idle admission, then reconnects with an
independent worker. This ad-hoc signed developer bundle is not a notarized release
or a general installer. The toolchain's macOS 13 build target does not establish
a minimum supported OS; product support remains undecided.

Device state: `~/Library/Application Support/Shioji/worker/` (0700 directory).
Client drafts: `~/Library/Application Support/Shioji/client/state.json` (0600).
Use `--worker-state` and `--client-state` on the GUI or `--state-dir` on the worker
for an isolated development environment. To stop a development worker, send SIGTERM
to that specific worker process; it stops its own harness group orderly. Restart
automatically closes interrupted execution records once the old owner is retired.
Unconfirmed inputs retain their original receipts. For an interrupted turn with a
recoverable thread and an empty queue, the worker can send one new `continue`
message in the same session after restarting. Pending messages remain paused.

```sh
env -u GOROOT go test ./...
env -u GOROOT go test -race ./...
python3 scripts/verify-design-freeze.py --repository
python3 scripts/verify-codex-recordings.py
```

Recorded tests use sanitized real app-server conversation fixtures, not live model
calls. The schema verification script requires Python `jsonschema`. Regenerate the
loopback fixture with `scripts/record-codex.py --kind loopback --out ...`; use
`--kind live` only for a deliberate real-account recording (two small real turns).
Neither recorder reads authentication files or collects account/auth messages.

The implementation scope and outstanding gates are in
[stage 1 implementation notes](docs/implementation-stage-1.md). Frozen design,
research archives and acceptance probes are not production dependencies.
