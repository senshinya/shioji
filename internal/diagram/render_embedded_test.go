//go:build darwin && diagram_embedded_test

package diagram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEmbeddedDiagramsDrawWithNativeSVG(t *testing.T) {
	for _, sample := range []struct{ name, text, label string }{
		{"flowchart", "flowchart LR\n A[\"中文第一行<br>English\"] --> B[\"<b>粗体</b>\"]", "中文第一行"},
		{"sequence", "sequenceDiagram\n participant A as 手机\n participant B as Worker\n A->>B: 查询回执\n B-->>A: <b>已接纳</b>", "已接纳"},
		{"class", "classDiagram\n class Device {\n +String name\n +connect()\n }\n Device --> Workspace", "Device"},
		{"state", "stateDiagram-v2\n [*] --> Still\n Still --> [*]", "Still"},
		{"entity", "erDiagram\n CUSTOMER ||--o{ ORDER : places", "CUSTOMER"},
		{"gantt", "gantt\n title Release\n dateFormat YYYY-MM-DD\n Ship :t1, 2026-11-20, 1d", "Ship"},
		{"pie", "pie\n title Animals\n \"Dog\" : 30\n \"Cat\" : 20", "Animals"},
		{"mindmap", "mindmap\n root((hello))\n  branch\n   leaf", "hello"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, dark := range []bool{false, true} {
				data, err := RenderEmbedded(context.Background(), Request{Source: sample.text, Dark: dark})
				if err != nil {
					t.Fatal(err)
				}
				if text := string(data); !strings.Contains(text, sample.label) || strings.Contains(text, "var(") {
					t.Fatal("lost graph labels or theme resolution")
				}
				graph, err := ui.ParseSVG(data)
				if err != nil {
					t.Fatal(err)
				}
				if missing := graph.Unsupported(); len(missing) != 0 {
					t.Fatalf("MyGo cannot draw this SVG: %v", missing)
				}
				if width, height := graph.Size(); width <= 0 || height <= 0 {
					t.Fatalf("empty diagram: %f by %f", width, height)
				}
			}
		})
	}
}

func TestEmbeddedDiagramsRejectInvalidSourceAndHonorCancellation(t *testing.T) {
	for _, source := range []string{"", "flowchart LR\n A -->", strings.Repeat("x", MaxSource+1)} {
		if _, err := RenderEmbedded(context.Background(), Request{Source: source}); err == nil {
			t.Fatal("invalid Mermaid source was accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RenderEmbedded(ctx, Request{Source: "flowchart LR\n A --> B"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v after cancellation", err)
	}
}
