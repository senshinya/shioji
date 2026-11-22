// Package diagram renders Mermaid source into a bounded local SVG.
package diagram

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const MaxSource = 16 << 10
const maxSVG = 8 << 20
const Timeout = 4 * time.Second
const HelperArgument = "--render-diagram"

//go:embed assets/pele.js
var peleSource string

type Request struct {
	Source string
	Dark   bool
}

type response struct {
	SVG   string `json:",omitempty"`
	Error string `json:",omitempty"`
}

// IsHelper must be checked before the app opens user state or starts services.
func IsHelper(args []string) bool { return len(args) > 0 && args[0] == HelperArgument }

// Serve handles one bounded request. The calling process owns its deadline.
func Serve(in io.Reader, out io.Writer, usvg string) error {
	data, err := io.ReadAll(io.LimitReader(in, MaxSource*8+1))
	if err != nil {
		return err
	}
	if len(data) > MaxSource*8 {
		return errors.New("图表请求过大")
	}
	var req Request
	if err = json.Unmarshal(data, &req); err != nil {
		return errors.New("图表请求无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	svg, err := render(ctx, req, usvg)
	reply := response{SVG: string(svg)}
	if err != nil {
		reply.Error = err.Error()
	}
	return json.NewEncoder(out).Encode(reply)
}

func render(ctx context.Context, req Request, usvg string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	svg, err := renderSource(req)
	if err != nil {
		return nil, err
	}
	return convertSVG(ctx, svg, usvg)
}

func convertSVG(ctx context.Context, svg []byte, usvg string) ([]byte, error) {
	args := []string{"--skip-system-fonts"}
	for _, font := range []string{diagramFontPath, "/System/Library/Fonts/Supplemental/Arial Bold.ttf", "/System/Library/Fonts/Supplemental/Arial Italic.ttf", "/System/Library/Fonts/Supplemental/Arial Bold Italic.ttf", diagramCJKPath, diagramMonoPath} {
		args = append(args, "--use-font-file", font)
	}
	args = append(args, "-", "-c")
	cmd := exec.CommandContext(ctx, usvg, args...)
	cmd.Stdin = bytes.NewReader(svg)
	var output limitedBuffer
	output.limit = maxSVG
	cmd.Stdout = &output
	// Keep converter diagnostics separate from its SVG protocol.
	var diagnostics fontDiagnostics
	diagnostics.limit = 4096
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("图表字体转换失败：%w (%s)", err, strings.TrimSpace(diagnostics.String()))
	}
	if output.exceeded {
		return nil, errors.New("图表超出渲染范围")
	}
	if diagnostics.missingGlyph {
		return nil, errors.New("当前图表包含不可用的字体字形")
	}
	if err := checkVector(output.Bytes()); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type fontDiagnostics struct {
	limitedBuffer
	tail         string
	missingGlyph bool
}

func (b *fontDiagnostics) Write(data []byte) (int, error) {
	// Detect conversion warnings even after the retained diagnostic bound and
	// across writes; usvg can exit successfully while emitting .notdef paths.
	text := b.tail + string(data)
	b.missingGlyph = b.missingGlyph || strings.Contains(text, "No fonts with a ")
	if len(text) > 64 {
		text = text[len(text)-64:]
	}
	b.tail = text
	return b.limitedBuffer.Write(data)
}

func renderSource(req Request) ([]byte, error) {
	return evaluateSource(req, evaluatePele)
}

func evaluateSource(req Request, evaluate func(Request) (string, error)) ([]byte, error) {
	if strings.TrimSpace(req.Source) == "" || len(req.Source) > MaxSource || strings.ContainsRune(req.Source, 0) {
		return nil, errors.New("图表内容为空或超出渲染范围")
	}
	svg, err := evaluate(req)
	if err != nil {
		return nil, err
	}
	if len(svg) > 2<<20 {
		return nil, errors.New("图表超出渲染范围")
	}
	return normalizeSVG([]byte(svg), req.Dark)
}

type Client struct{ Executable, USVG string }

func DefaultClient() Client {
	exe, _ := os.Executable()
	usvg := filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "Resources", "usvg"))
	if _, err := os.Stat(usvg); err != nil {
		// Explicit development profile; release bundles always carry their runtime.
		if path := os.Getenv("SHIOJI_DIAGRAM_USVG"); filepath.IsAbs(path) {
			usvg = path
		} else if cwd, err := os.Getwd(); err == nil {
			usvg = filepath.Join(cwd, "build", "diagram-runtime", "usvg")
		}
	}
	return Client{Executable: exe, USVG: usvg}
}

func (c Client) Render(ctx context.Context, req Request) ([]byte, error) {
	if len(req.Source) > MaxSource {
		return nil, errors.New("图表超出渲染范围")
	}
	if _, err := os.Stat(c.USVG); err != nil {
		return nil, errors.New("图表运行环境未就绪")
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, c.Executable, HelperArgument, c.USVG)
	isolateProcess(cmd)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "MYGO_AUTOMATION=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Stdin = bytes.NewReader(data)
	var output limitedBuffer
	output.limit = maxSVG + 4096
	cmd.Stdout = &output
	var diagnostics limitedBuffer
	diagnostics.limit = 4096
	cmd.Stderr = &diagnostics
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("图表渲染进程未能完成")
	}
	if output.exceeded {
		return nil, errors.New("图表超出渲染范围")
	}
	var result response
	if err = json.Unmarshal(output.Bytes(), &result); err != nil {
		return nil, errors.New("图表渲染结果无效")
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	if err = checkVector([]byte(result.SVG)); err != nil {
		return nil, err
	}
	return []byte(result.SVG), nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *limitedBuffer) String() string { return b.buffer.String() }
func (b *limitedBuffer) Len() int       { return b.buffer.Len() }

func (b *limitedBuffer) Write(data []byte) (int, error) {
	n := len(data)
	if len(data) > b.limit-b.Len() {
		b.exceeded = true
		data = data[:max(0, b.limit-b.Len())]
	}
	_, _ = b.buffer.Write(data)
	// Drain the pipe after the bound, avoiding child-process deadlocks.
	return n, nil
}
