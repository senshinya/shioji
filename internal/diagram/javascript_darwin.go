package diagram

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	n "github.com/egoist/mygo/ui"
)

const diagramFont = "Arial, Hiragino Sans GB"
const diagramMono = "Menlo, Hiragino Sans GB"
const diagramFontPath = "/System/Library/Fonts/Supplemental/Arial.ttf"
const diagramCJKPath = "/System/Library/Fonts/Hiragino Sans GB.ttc"
const diagramMonoPath = "/System/Library/Fonts/Menlo.ttc"

var jsc struct {
	once          sync.Once
	err           error
	create        func(uintptr) uintptr
	release       func(uintptr)
	stringCreate  func(string) uintptr
	stringRelease func(uintptr)
	evaluate      func(uintptr, uintptr, uintptr, uintptr, int32, *uintptr) uintptr
	valueString   func(uintptr, uintptr, *uintptr) uintptr
	maxUTF8       func(uintptr) uintptr
	getUTF8       func(uintptr, uintptr, uintptr) uintptr
	makeFunction  func(uintptr, uintptr, uintptr) uintptr
	global        func(uintptr) uintptr
	setProperty   func(uintptr, uintptr, uintptr, uintptr, uint32, *uintptr)
	number        func(uintptr, uintptr, *uintptr) float64
	makeNumber    func(uintptr, float64) uintptr
	measure       uintptr
}

func loadJSC() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/JavaScriptCore.framework/JavaScriptCore", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		jsc.err = err
		return
	}
	purego.RegisterLibFunc(&jsc.create, lib, "JSGlobalContextCreate")
	purego.RegisterLibFunc(&jsc.release, lib, "JSGlobalContextRelease")
	purego.RegisterLibFunc(&jsc.stringCreate, lib, "JSStringCreateWithUTF8CString")
	purego.RegisterLibFunc(&jsc.stringRelease, lib, "JSStringRelease")
	purego.RegisterLibFunc(&jsc.evaluate, lib, "JSEvaluateScript")
	purego.RegisterLibFunc(&jsc.valueString, lib, "JSValueToStringCopy")
	purego.RegisterLibFunc(&jsc.maxUTF8, lib, "JSStringGetMaximumUTF8CStringSize")
	purego.RegisterLibFunc(&jsc.getUTF8, lib, "JSStringGetUTF8CString")
	purego.RegisterLibFunc(&jsc.makeFunction, lib, "JSObjectMakeFunctionWithCallback")
	purego.RegisterLibFunc(&jsc.global, lib, "JSContextGetGlobalObject")
	purego.RegisterLibFunc(&jsc.setProperty, lib, "JSObjectSetProperty")
	purego.RegisterLibFunc(&jsc.number, lib, "JSValueToNumber")
	purego.RegisterLibFunc(&jsc.makeNumber, lib, "JSValueMakeNumber")
	// One callback for the process, never one per diagram/context/window.
	jsc.measure = purego.NewCallback(measureText)
}

func jsText(ctx, value uintptr) (string, error) {
	s := jsc.valueString(ctx, value, nil)
	if s == 0 {
		return "", errors.New("无法读取图表结果")
	}
	defer jsc.stringRelease(s)
	size := jsc.maxUTF8(s)
	if size == 0 || size > maxSVG+4096 {
		return "", errors.New("图表超出渲染范围")
	}
	data := make([]byte, size)
	count := jsc.getUTF8(s, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	runtime.KeepAlive(data)
	if count == 0 {
		return "", errors.New("图表文字编码无效")
	}
	return string(data[:count-1]), nil
}

func measureText(ctx, _ uintptr, _ uintptr, count uintptr, values uintptr, _ uintptr) uintptr {
	if count != 3 {
		return jsc.makeNumber(ctx, 0)
	}
	args := unsafe.Slice((*uintptr)(unsafe.Pointer(values)), 3)
	text, err := jsText(ctx, args[0])
	size := jsc.number(ctx, args[1], nil)
	style := jsc.number(ctx, args[2], nil)
	if err != nil || len(text) > MaxSource || math.IsNaN(size) || math.IsInf(size, 0) || size <= 0 || size > 128 || math.IsNaN(style) || math.IsInf(style, 0) {
		return jsc.makeNumber(ctx, 0)
	}
	font := n.Font{Family: diagramFont, Size: float32(size), Weight: 400, Italic: int(style)&2 != 0}
	if int(style)&1 != 0 {
		font.Weight = 700
	}
	if int(style)&4 != 0 {
		font.Family = diagramMono
	}
	var width float32
	for _, g := range n.Shape(text, font) {
		width = max(width, g.X+g.Advance)
	}
	return jsc.makeNumber(ctx, float64(width))
}

func evaluatePele(req Request) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	jsc.once.Do(loadJSC)
	if jsc.err != nil {
		return "", errors.New("JavaScriptCore 不可用")
	}
	ctx := jsc.create(0)
	if ctx == 0 {
		return "", errors.New("无法创建图表渲染环境")
	}
	defer jsc.release(ctx)
	name := jsc.stringCreate("nativeMeasure")
	fn := jsc.makeFunction(ctx, name, jsc.measure)
	jsc.setProperty(ctx, jsc.global(ctx), name, fn, 0, nil)
	jsc.stringRelease(name)
	eval := func(source string) (string, error) {
		script := jsc.stringCreate(source)
		defer jsc.stringRelease(script)
		var exception uintptr
		value := jsc.evaluate(ctx, script, 0, 0, 1, &exception)
		if exception != 0 {
			message, err := jsText(ctx, exception)
			if err != nil {
				return "", errors.New("无法解析 Mermaid 图表")
			}
			if len(message) > 1024 {
				message = message[:1024]
			}
			return "", fmt.Errorf("%s", strings.TrimSpace(message))
		}
		return jsText(ctx, value)
	}
	if _, err := eval(peleSource); err != nil {
		return "", err
	}
	request, _ := json.Marshal(req)
	quoted, _ := json.Marshal(string(request))
	return eval("shiojiDiagram(" + string(quoted) + ")")
}
