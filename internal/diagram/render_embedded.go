//go:build ios || (darwin && diagram_embedded_test)

package diagram

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/dop251/goja"
)

var peleProgram = sync.OnceValues(func() (*goja.Program, error) {
	return goja.Compile("pele.js", peleSource, true)
})

// RenderEmbedded uses the same Pele build and SVG normalization as the Mac
// helper. On iOS, Goja lets the app stop evaluation without child processes.
func RenderEmbedded(ctx context.Context, req Request) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return evaluateSource(req, func(req Request) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		program, err := peleProgram()
		if err != nil {
			return "", err
		}
		vm := goja.New()
		vm.SetMaxCallStackSize(2048)
		stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
		defer stop()
		if err = vm.Set("nativeMeasure", diagramTextWidth); err != nil {
			return "", err
		}
		if _, err = vm.RunProgram(program); err != nil {
			return "", err
		}
		render, ok := goja.AssertFunction(vm.Get("shiojiDiagram"))
		if !ok {
			return "", errors.New("图表渲染器不可用")
		}
		data, err := json.Marshal(req)
		if err != nil {
			return "", err
		}
		result, err := render(goja.Undefined(), vm.ToValue(string(data)))
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			return "", err
		}
		return result.String(), nil
	})
}
