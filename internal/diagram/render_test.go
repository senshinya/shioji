package diagram

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDiagramConverterPreservesBoldAndItalicFaces(t *testing.T) {
	path := os.Getenv("SHIOJI_DIAGRAM_USVG")
	if path == "" {
		t.Skip("set SHIOJI_DIAGRAM_USVG for real usvg integration")
	}
	var regular []byte
	for _, style := range []string{"", ` font-weight="700"`, ` font-style="italic"`, ` font-weight="700" font-style="italic"`} {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" width="250" height="80"><text x="10" y="35" font-family="Arial" font-size="20"` + style + `>WWWW Bold Text</text></svg>`
		out, err := convertSVG(context.Background(), []byte(svg), path)
		if err != nil {
			t.Fatal(err)
		}
		if style == "" {
			regular = out
		} else if bytes.Equal(regular, out) {
			t.Fatalf("emphasis lost its glyph face: %s", style)
		}
	}
}

func TestDiagramMissingGlyphsReturnSourceFallbackError(t *testing.T) {
	path := os.Getenv("SHIOJI_DIAGRAM_USVG")
	if path == "" {
		t.Skip("set SHIOJI_DIAGRAM_USVG for real usvg integration")
	}
	for _, source := range []string{"flowchart LR\n A[😀] --> B[🦊]", "flowchart LR\n A[한글] --> B[English]"} {
		if _, err := render(context.Background(), Request{Source: source}, path); err == nil {
			t.Fatal("missing glyphs were accepted as a successful diagram", source)
		}
	}
}

func TestPeleRendersChineseAndRejectsIncompleteSyntax(t *testing.T) {
	for _, test := range []struct {
		src string
		ok  bool
	}{
		{"flowchart TD\n A[开始 Start] --> B{通过?}\n B -->|是| C[完成]", true},
		{"flowchart LR\n A[\"中文第一行<br>English\"] --> B[\"<b>粗体</b>\"]", true},
		{"flowchart TD\n A[unterminated", false},
		{"flowchart TD\n A -->", false},
	} {
		svg, err := renderSource(Request{Source: test.src})
		if (err == nil) != test.ok {
			t.Fatalf("%q: %v", test.src, err)
		}
		if test.ok && (!strings.Contains(string(svg), "<text") || strings.Contains(string(svg), "var(")) {
			t.Fatalf("incomplete native SVG: %.300s", svg)
		}
	}
}
func TestDiagramSVGResolvesInheritedTokensAndKeepsTextPositions(t *testing.T) {
	src := `<svg xmlns="http://www.w3.org/2000/svg" width="160" height="80" viewBox="0 0 160 80" style="--_fg:var(--pele-fg,#111);--_s:var(--pele-surface,#eee);font:16px sans-serif" fill="var(--_fg)"><g style="--local:var(--_s)"><rect width="100" height="80" fill="var(--local)"/><text><tspan x="50" y="20">中文</tspan><tspan x="50" y="40">English</tspan></text></g></svg>`
	out, err := normalizeSVG([]byte(src), true)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{`fill="#eeeeee"`, `x="50" y="20"`, `x="50" y="40"`, "中文", "English"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost %s: %s", want, text)
		}
	}
	if strings.Contains(text, "var(") {
		t.Fatal("unresolved theme")
	}
}
func TestDiagramSVGRejectsResourcesAndUnknownCSS(t *testing.T) {
	for _, body := range []string{`<image href="file:///etc/passwd"/>`, `<script>1</script>`, `<foreignObject/>`, `<path fill="url(https://example.invalid/x)"/>`, `<path fill="var(--missing)"/>`} {
		_, err := normalizeSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">`+body+`</svg>`), false)
		if err == nil {
			t.Fatal("accepted unsupported content", body)
		}
	}
}
func TestDiagramFullVectorPipeline(t *testing.T) {
	path := os.Getenv("SHIOJI_DIAGRAM_USVG")
	if path == "" {
		t.Skip("set SHIOJI_DIAGRAM_USVG for real usvg integration")
	}
	out, err := render(context.Background(), Request{Source: "flowchart LR\n A[\"中文<br>English\"] --> B[完成]", Dark: true}, path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<path") || strings.Contains(string(out), "<text") || strings.Contains(string(out), "<marker") {
		t.Fatal("output is not a complete vector diagram")
	}
}

func TestDiagramStyledNodesRemainValidSVG(t *testing.T) {
	path := os.Getenv("SHIOJI_DIAGRAM_USVG")
	if path == "" {
		t.Skip("set SHIOJI_DIAGRAM_USVG for real usvg integration")
	}
	for _, source := range []string{
		"flowchart LR\n A[开始] --> B[完成]\n style A fill:#f00,stroke:#333,color:#fff",
		"classDiagram\n class Device {\n +String name\n +connect()\n }\n Device --> Workspace",
		"sequenceDiagram\n participant A as 客户端\n participant B as Worker\n A->>B: 任务\n B-->>A: 完成",
	} {
		if _, err := render(context.Background(), Request{Source: source}, path); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
}
func TestDiagramInputIsBoundedBeforeExecution(t *testing.T) {
	_, err := renderSource(Request{Source: strings.Repeat("x", MaxSource+1)})
	if err == nil {
		t.Fatal("accepted unbounded input")
	}
	var request Request
	if err = json.Unmarshal([]byte(`{"Source":"flowchart TD\\n A --> B","Dark":true}`), &request); err != nil {
		t.Fatal(err)
	}
}
