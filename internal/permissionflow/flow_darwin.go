// Package permissionflow loads the bundled Swift PermissionFlow bridge into the
// client and checks the client's effective Full Disk Access. Workers do not use it.
package permissionflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ebitengine/purego"
)

type Flow struct {
	app, runtime, library string
	mu                    sync.Mutex
	handle                uintptr
	open                  func(string) int32
	close                 func()
}

func New() *Flow {
	executable, _ := os.Executable()
	home, _ := os.UserHomeDir()
	return newFlow(executable, home)
}

func newFlow(executable, home string) *Flow {
	app := filepath.Dir(filepath.Dir(filepath.Dir(executable)))
	return &Flow{
		app:     app,
		runtime: filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Shioji Runtime.app"),
		library: filepath.Join(app, "Contents", "Frameworks", "libShiojiPermissionFlow.dylib"),
	}
}

func (f *Flow) RuntimeAvailable() bool {
	st, err := os.Stat(f.runtime)
	return err == nil && st.IsDir()
}

// Open must be called from the native UI thread. A missing Runtime never falls
// back to the main App: they have separate system authorization identities.
func (f *Flow) Open(background bool) error {
	app := f.app
	if background {
		app = f.runtime
	}
	if st, err := os.Stat(app); err != nil || !st.IsDir() {
		return fmt.Errorf("无法找到授权应用：%s", app)
	}
	if err := f.load(); err != nil {
		return err
	}
	switch f.open(app) {
	case 0:
		return nil
	case 1:
		return errors.New("授权目标不是有效的 App")
	case 2:
		return errors.New("授权引导需要从原生主线程打开")
	case 3:
		return errors.New("PermissionFlow 本地化资源缺失，请重新安装 Shioji")
	case 4:
		return errors.New("系统设置无法打开")
	default:
		return errors.New("PermissionFlow 返回未知结果")
	}
}

func (f *Flow) load() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handle != 0 {
		return nil
	}
	handle, err := purego.Dlopen(f.library, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return fmt.Errorf("无法加载 PermissionFlow 授权引导：%w", err)
	}
	// Resolve every symbol before publishing the handle. A packaging mismatch
	// reports an error instead of RegisterLibFunc panicking in a button handler.
	var symbols [3]uintptr
	for i, name := range []string{"shioji_permission_flow_version", "shioji_permission_flow_open", "shioji_permission_flow_close"} {
		symbols[i], err = purego.Dlsym(handle, name)
		if err != nil {
			purego.Dlclose(handle)
			return fmt.Errorf("PermissionFlow 桥接不完整：%w", err)
		}
	}
	var version func() int32
	purego.RegisterFunc(&version, symbols[0])
	if version() != 1 {
		purego.Dlclose(handle)
		return errors.New("PermissionFlow 桥接版本不兼容")
	}
	purego.RegisterFunc(&f.open, symbols[1])
	purego.RegisterFunc(&f.close, symbols[2])
	f.handle = handle // Retained for the process lifetime while Swift owns UI objects.
	return nil
}

func (f *Flow) Close() {
	f.mu.Lock()
	close := f.close
	f.mu.Unlock()
	if close != nil {
		close()
	}
}
