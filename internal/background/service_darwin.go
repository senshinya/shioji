// Package background is the small native SMAppService bridge in Runtime.app.
package background

import (
	"errors"
	"fmt"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"runtime"
	"shioji/internal/domain"
	"unsafe"
)

const PlistName = "dev.shioji.runtime.agent.plist"
const NetworkPlistName = "dev.shioji.network.agent.plist"

func Manage(action, plist string) (domain.BackgroundService, error) {
	result := domain.BackgroundService{Format: 1, State: "unsupported"}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, path := range []string{"/System/Library/Frameworks/Foundation.framework/Foundation", "/System/Library/Frameworks/ServiceManagement.framework/ServiceManagement"} {
		if _, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			return result, err
		}
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	cls := objc.ID(objc.GetClass("SMAppService"))
	if cls == 0 {
		return result, errors.New("此系统不支持 SMAppService")
	}
	ns := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), plist)
	service := cls.Send(objc.RegisterName("agentServiceWithPlistName:"), ns)
	if action != "status" && action != "register" && action != "unregister" {
		return result, errors.New("未知后台服务操作")
	}
	status := func() int { return int(service.Send(objc.RegisterName("status"))) }
	current := status()
	if action == "register" && current != 1 && current != 2 || action == "unregister" && current != 0 {
		selector := "registerAndReturnError:"
		if action == "unregister" {
			selector = "unregisterAndReturnError:"
		}
		var nativeError objc.ID
		if service.Send(objc.RegisterName(selector), uintptr(unsafe.Pointer(&nativeError))) == 0 {
			var cString func(objc.ID, objc.SEL) string
			handle, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
			if err != nil {
				return result, err
			}
			purego.RegisterLibFunc(&cString, handle, "objc_msgSend")
			message := cString(nativeError.Send(objc.RegisterName("localizedDescription")), objc.RegisterName("UTF8String"))
			result.State = "failed"
			result.Message = message
			return result, fmt.Errorf("后台服务操作失败：%s", message)
		}
	}
	switch status() {
	case 0:
		result.State = "notRegistered"
	case 1:
		result.State = "enabled"
	case 2:
		result.State = "requiresApproval"
		result.Message = "请在系统设置的登录项与扩展中允许 Shioji 后台运行"
	case 3:
		result.State = "notFound"
	default:
		result.State = "unavailable"
	}
	return result, nil
}
