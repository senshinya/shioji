package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
)

// PrepareExecutionRuntime stages only a signed execution host. It never
// registers/unregisters a login item or replaces the running network service.
func (s RemoteWorker) PrepareExecutionRuntime(ctx context.Context, artifact WorkerArtifact) error {
	if s.Run == nil || artifact == nil {
		return errors.New("客户端缺少执行 Runtime 制品，请更新 Shioji")
	}
	directory, err := remoteWorkerRoot(s.Directory)
	if err != nil {
		return err
	}
	content, digest, err := artifact(ctx)
	if err != nil {
		return err
	}
	defer content.Close()
	if _, err := StagedWorkerPath("/", digest); err != nil {
		return err
	}
	script := `set -eu
umask 077
worker=` + directory + `
[ -d "$worker" ] && [ ! -L "$worker" ]
[ "$(/usr/bin/stat -f %u "$worker")" = "$(/usr/bin/id -u)" ]
[ "$(/usr/bin/stat -f %Lp "$worker")" = 700 ]
root="$HOME/Library/Application Support/Shioji/Runtime/Execution"
/bin/mkdir -p "$root"
[ ! -L "$root" ]
stage=$(/usr/bin/mktemp -d "$root/.install.XXXXXX")
trap '/bin/rm -rf "$stage"' EXIT
/bin/cat > "$stage/runtime.tar"
actual=$(/usr/bin/shasum -a 256 "$stage/runtime.tar")
[ "${actual%% *}" = ` + shellQuote(digest) + ` ]
target="$root/` + digest + `/Shioji Runtime.app"
if [ ! -e "$target" ]; then
  /bin/mkdir "$stage/Shioji Runtime.app"
  /usr/bin/tar -xf "$stage/runtime.tar" -C "$stage/Shioji Runtime.app"
  /usr/bin/codesign --verify --deep --strict "$stage/Shioji Runtime.app"
  [ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$stage/Shioji Runtime.app/Contents/Info.plist")" = dev.shioji.runtime ]
  "$stage/Shioji Runtime.app/Contents/MacOS/shioji-runtime" --execution-describe > "$stage/description.json"
  [ "$(/usr/bin/plutil -extract Version raw "$stage/description.json")" = 1 ]
  [ "$(/usr/bin/plutil -extract Role raw "$stage/description.json")" = execution ]
  /bin/mkdir -p "$root/` + digest + `"
  /bin/mv -n "$stage/Shioji Runtime.app" "$target"
fi
[ -d "$target" ] && [ ! -L "$target" ]
/usr/bin/codesign --verify --deep --strict "$target"
[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$target/Contents/Info.plist")" = dev.shioji.runtime ]
"$target/Contents/MacOS/shioji-runtime" --execution-describe > "$stage/description.json"
[ "$(/usr/bin/plutil -extract Version raw "$stage/description.json")" = 1 ]
[ "$(/usr/bin/plutil -extract Role raw "$stage/description.json")" = execution ]
record=$(/usr/bin/mktemp "$worker/.execution-host.XXXXXX")
printf '%s\n' '<?xml version="1.0"?><plist version="1.0"><dict/></plist>' > "$record"
/usr/bin/plutil -insert Bundle -string "$target" "$record"
/usr/bin/plutil -convert json "$record"
/bin/mv -f "$record" "$worker/execution-host.json"
`
	var diagnostic bytes.Buffer
	_, err = s.Run(ctx, "/bin/sh -c "+shellQuote(script), content, io.Discard, &boundedWriter{w: &diagnostic, remaining: 2048})
	if err != nil {
		return fmt.Errorf("准备设备执行 Runtime 失败：%s (%w)", diagnostic.String(), err)
	}
	return nil
}
