# Local macOS research archive

Only this README is versioned in this directory. The one-off probe sources remain locally available and are ignored by Git; they are not prerequisites for building the product or cloning its implementation baseline. Historical commands below describe that local archive. See [archive policy](../../docs/research/README.md).

These are throwaway feasibility fixtures, not the Tahuna worker or its production protocol. Results and limitations are in [the research report](../../docs/macos-worker-research-2026-11-03.md).

Final findings and user-deferred acceptance are consolidated in [the closeout](../../docs/research-closeout-2026-11-03.md).

- `codex_local_provider.py`: a real Codex app-server with a deterministic loopback provider and isolated CODEX_HOME; no account or external model invocation.
- `launchd_probe.go`: a native process, private Unix socket and same-process-group child for launchd lifecycle checks.
- `protocol_host_probe.py`: a temporary owner of app-server pipes, used to verify that a pending question survives separate SSH connections. Its harness-specific fixture commands are not the final worker API.
- `remote_macos_probe.py`: uploads those fixtures to a temporary remote directory, loads uniquely named user services, checks reconnect/crash behavior and removes the services and directory in `finally`.
- `sqlite_crash_probe.py`: transaction atomicity and an intentionally indeterminate external-effect window under SIGKILL.
- `sqlite_driver_probe.go`: the corresponding process-crash smoke check using the actual pure-Go SQLite candidate.
- `mygo_native_fixture.go` and `native_ui_probe.py`: a simulated native window and engine automation, not physical keyboard/IME or VoiceOver acceptance.
- `ssh_byte_bridge.py`: ordinary TCP ProxyCommand probe. This check failed in the observed environment; it is not a verified tsnet bridge.
- `workspace_store_probe.go` and `multi_client_probe.py`: one temporary Air owner, local/SSH clients, revision conflicts, operation deduplication, retained launch-context/queue fixtures, schema upgrade refusal, and a separate real Codex question/approval race. These are protocol clients, not installed GUIs; no queued prompt is executed.
- `tsnet_inbound_research_test.go`: copy into the pinned upstream `tsnet` package and run `go test -tags tahuna_research ./tsnet -run '^TestTahunaInboundOfficial$'`. Uses local upstream control/DERP helpers, bidirectional TCP, WhoIs-based fixture authorization, half-close, Unix backends, and explicit held-stream shutdown. Never joins a real tailnet.
- `local_network_probe.swift`: a throwaway Network.framework diagnostic for Local Network denial. It is not product code or a change to the Go frontend/worker choice.
- `codex_recovery_probe.py`: runtime catalog/requirements, four permission mappings, and signal-checked app-server crashes while waiting for a question/approval or streaming. No prompt replay or external provider.
- `codex_execution_probe.py`: actual Codex commands in isolated paths; read-only write refusal, workspace write acceptance and outside-root write refusal. It creates and removes its own test files only.
- `protocol_contract_probe.py`: exhaustive schema inventory routing and a small synthetic projection check; requires Python `jsonschema`. It is not a complete adapter implementation.
- `tsnet_ssh_research_test.go`: copy into official pinned tsnet, run opt-in `TestTahunaSSH`; temporary nonroot sshd, private generated test keys, strict host verification, no system sshd changes. `TestTahunaProxyHelper` is a subprocess fixture only.
- `sqlite_fault_probe.go`: actual driver faults, snapshot/backup, signal-checked migration crash and batched-load measurements. Quota exhaustion does not fill the user's volume.
- `mygo_stream_fixture.go` / `mygo_stream_probe.py`: simulated dual streams, two independent drafts and logical row counts of 5,000/50,000 per pane. Build as an ad-hoc app; `--wait-for-focus` pauses each run until the native window is activated. Read the complete result, including update-count and size bounds.
- `mygo_selection_research_test.go`: copy into pinned MyGo `ui`, run with the opt-in tag. Records the unsupported offscreen endpoint behavior instead of treating it as passed functionality.
- `mygo_secrets_probe.go` / `keychain_status_probe.swift`: temporary random credential and default-Keychain status only. The real Secrets attempt failed with interaction disallowed; no user credentials were read, no Keychain was unlocked and no plaintext fallback was used.
- `service_management_probe.swift` / `service_bundle_probe.py`: ad-hoc bundle registration and unregister through SMAppService, local/SSH context. Does not establish Developer ID or notarization acceptance.
- `macos_lifecycle_probe.py`: prepare/inspect/cleanup a uniquely named temporary login agent. It never sleeps or reboots the Mac itself. User deferred power tests and the prepared agent was removed. Preparation failures may require removal of the unique root/plist identified in its private config; never remove unrelated agents.
- `filesystem_git_probe.py` / `root_scope_probe.go`: disposable worktrees, conflicts, canonical repository identity and Go os.Root traversal/symlink checks.

Standalone Go probes have `//go:build ignore`; the upstream-package test uses the opt-in `tahuna_research` tag. A future product module does not build these fixtures by default. Compile standalone probes by explicit filename in an isolated build environment.

## Reproduction

Run local protocol checks with an available Codex 0.166.0 binary:

```sh
python3 scripts/research/codex_local_provider.py --out /tmp/tahuna-codex-result.json
python3 scripts/research/sqlite_crash_probe.py --out /tmp/tahuna-sqlite-result.json
env -u GOROOT GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -o /tmp/tahuna-launchd-probe scripts/research/launchd_probe.go
```

The explicit toolchain avoids the observed mismatch between the machine's default Go binary and its exported GOROOT; no global environment setting was changed.

For remote checks, first verify the target host fingerprint independently and create a dedicated known_hosts file. Then provide `--host`, `--known-hosts`, `--probe`, `--codex`, and `--out` to `remote_macos_probe.py`. It uses strict host checking and does not install into system or user application directories. A GUI user domain must be available for the question-reconnect fixture; no logout, reboot or power-loss test is performed.

Build the SQLite driver fixture in a disposable module with `modernc.org/sqlite v1.61.1`; the exact module and sums are recorded under `docs/research/macos-2026-11-03/`. Build the native fixture in a disposable module using the pinned MyGo v0.7.4 checkout, not the product root. Its automation package must remain absent from product release builds.

Do not infer production durability, authentication, bounded retention or complete request recovery from these fixtures. In particular, passing a private-socket reconnect check does not establish worker-crash recovery or make RPC execution exactly once.
