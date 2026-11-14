# PermissionFlow integration

The settings window remains Go / MyGo. The local authorization action calls
this small C ABI bridge through the existing `purego` dependency, retaining
`CGO_ENABLED=0`. The Swift package directly depends on upstream PermissionFlow
at revision `65434c9ae04dba97cf52c1dffbe3e4e62b547c82`; its source is not copied or
reimplemented.

Only `PermissionFlowController` is used, for System Settings navigation and
its native floating App drag card. No status store or protected-file permission
probe is invoked. Authorization remains a user action in macOS. Accessibility
permission prompts are disabled; upstream uses its window-server tracking
fallback without requiring another permission.

The main App and an installed background Runtime have separate settings
actions. The Runtime target is the installed bundle at
`~/Library/Application Support/Shioji/Runtime/Shioji Runtime.app`, rather than
the copy embedded in the client or a remote device's path. Missing targets
report a visible settings error. A fresh controller for each action prevents
upstream's first-candidate selection from retaining the previous App card.

Open runs on the native main thread. Close tears down the panel and window
tracking on application quit. The dynamic library remains loaded for the
process lifetime because Swift owns its window and observer objects.

`scripts/bundle-permission-flow.py <App>/Contents` builds the pinned dynamic
library, copies its localized resource bundle and Swift compatibility libraries,
removes build-machine search paths, and includes the MIT license. The normal
packaging script signs these libraries before signing the enclosing App.
Packaged clients never download or compile the dependency at runtime.

Checks, without building the complete App:

```sh
swift test --package-path native/permission-flow --scratch-path build/native/permission-flow
python3 scripts/bundle-permission-flow.py /tmp/shioji-permissionflow-check/Contents
env -u GOROOT CGO_ENABLED=0 SHIOJI_PERMISSION_FLOW_TEST_LIBRARY=/tmp/shioji-permissionflow-check/Contents/Frameworks/libShiojiPermissionFlow.dylib go test ./internal/permissionflow -count=1
env -u GOROOT go test ./...
```

The bridge declares macOS 13 as its deployment target, matching current package
metadata. This does not establish live acceptance on every supported OS version.
