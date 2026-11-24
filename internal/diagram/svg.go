package diagram

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type svgNode struct {
	start    xml.StartElement
	text     string
	children []*svgNode
}

func readSVG(data []byte) (*svgNode, error) {
	if len(data) == 0 || len(data) > maxSVG {
		return nil, errors.New("图表结果为空或过大")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var root *svgNode
	stack := []*svgNode{}
	count := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("图表 SVG 无效")
		}
		switch t := token.(type) {
		case xml.StartElement:
			count++
			if count > 100000 || len(stack) > 64 {
				return nil, errors.New("图表超出渲染范围")
			}
			if t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg" {
				return nil, errors.New("图表包含不支持的内容")
			}
			switch t.Name.Local {
			case "svg", "g", "defs", "path", "rect", "circle", "ellipse", "line", "polygon", "polyline", "text", "tspan", "title", "desc", "linearGradient", "radialGradient", "stop", "clipPath", "use":
			default:
				return nil, fmt.Errorf("图表包含不支持的元素：%s", t.Name.Local)
			}
			node := &svgNode{start: t}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			} else if root != nil {
				return nil, errors.New("图表 SVG 无效")
			} else {
				root = node
			}
			stack = append(stack, node)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].children = append(stack[len(stack)-1].children, &svgNode{text: string(t)})
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("图表 SVG 无效")
			}
			stack = stack[:len(stack)-1]
		case xml.Directive:
			return nil, errors.New("图表不支持 XML 指令")
		}
	}
	if root == nil || root.start.Name.Local != "svg" || len(stack) != 0 {
		return nil, errors.New("图表 SVG 无效")
	}
	return root, nil
}

func normalizeSVG(data []byte, dark bool) ([]byte, error) {
	root, err := readSVG(data)
	if err != nil {
		return nil, err
	}
	palette := map[string]string{"--pele-bg": "#ffffff", "--pele-fg": "#242424", "--pele-muted": "#767676", "--pele-line": "#242424", "--pele-surface": "#f4f4f4", "--pele-surface-alt": "#e9e9e9", "--pele-border": "#a0a0a0", "--pele-accent": "#242424", "--pele-font": diagramFont, "--pele-font-mono": diagramMono, "--pele-radius": "4", "--pele-bold-weight": "700", "--pele-heading-weight": "700"}
	if dark {
		for key, value := range map[string]string{"--pele-bg": "#202020", "--pele-fg": "#eeeeee", "--pele-muted": "#9c9c9c", "--pele-line": "#eeeeee", "--pele-surface": "#2a2a2a", "--pele-surface-alt": "#353535", "--pele-border": "#7d7d7d", "--pele-accent": "#eeeeee"} {
			palette[key] = value
		}
	}
	if err = normalizeNode(root, palette, 0); err != nil {
		return nil, err
	}
	// Export a concrete local font, with the same family list as nativeMeasure.
	foundFont := false
	for i := range root.start.Attr {
		if root.start.Attr[i].Name.Local == "font-family" {
			root.start.Attr[i].Value = diagramFont
			foundFont = true
		}
	}
	if !foundFont {
		root.start.Attr = append(root.start.Attr, xml.Attr{Name: xml.Name{Local: "font-family"}, Value: diagramFont})
	}
	if err = dimensions(root); err != nil {
		return nil, err
	}
	return encodeSVG(root)
}

func normalizeNode(node *svgNode, inherited map[string]string, depth int) error {
	if node.start.Name.Local == "" {
		return nil
	}
	vars := make(map[string]string, len(inherited)+8)
	for key, value := range inherited {
		vars[key] = value
	}
	styles := map[string]string{}
	for _, attr := range node.start.Attr {
		if attr.Name.Local == "style" {
			for _, part := range strings.Split(attr.Value, ";") {
				key, value, ok := strings.Cut(part, ":")
				if !ok {
					continue
				}
				key, value = strings.TrimSpace(key), strings.TrimSpace(value)
				if strings.HasPrefix(key, "--") {
					if _, locked := inherited[key]; !locked || !strings.HasPrefix(key, "--pele-") {
						vars[key] = value
					}
				} else {
					styles[key] = value
				}
			}
		}
	}
	attrs := []xml.Attr{}
	for _, attr := range node.start.Attr {
		key := attr.Name.Local
		if key == "style" || key == "xmlns" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(key), "on") {
			return errors.New("图表不支持脚本事件")
		}
		if key == "href" && !strings.HasPrefix(attr.Value, "#") {
			return errors.New("图表不支持外部资源")
		}
		value, err := resolveCSS(attr.Value, vars, 0)
		if err != nil {
			return err
		}
		if err = localValue(value); err != nil {
			return err
		}
		if key == "font-family" {
			value = localFont(value)
		}
		attr.Value = value
		attrs = append(attrs, attr)
	}
	for key, value := range styles {
		if key == "font" {
			// Pele's root font shorthand; explicit font attributes survive usvg.
			fields := strings.Fields(value)
			if len(fields) > 0 && strings.HasSuffix(fields[0], "px") {
				attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "font-size"}, Value: strings.TrimSuffix(fields[0], "px")})
			}
			continue
		}
		switch key {
		case "fill", "stroke", "stroke-width", "stroke-dasharray", "opacity", "fill-opacity", "stroke-opacity", "font-size", "font-weight", "font-style", "text-decoration", "rx", "ry", "letter-spacing", "word-spacing", "direction", "text-anchor", "stop-color", "stop-opacity", "color":
		case "font-family":
			resolved, err := resolveCSS(value, vars, 0)
			if err != nil {
				return err
			}
			value = localFont(resolved)
		case "max-width", "height", "background", "text-transform":
			continue
		default:
			return fmt.Errorf("图表包含不支持的样式：%s", key)
		}
		resolved, err := resolveCSS(value, vars, 0)
		if err != nil {
			return err
		}
		if err = localValue(resolved); err != nil {
			return err
		}
		// A CSS declaration overrides a presentation attribute. Encoding both
		// as attributes would create invalid XML and lose styled diagrams.
		replaced := false
		for i := range attrs {
			if attrs[i].Name.Local == key && attrs[i].Name.Space == "" {
				attrs[i].Value = resolved
				replaced = true
			}
		}
		if !replaced {
			attrs = append(attrs, xml.Attr{Name: xml.Name{Local: key}, Value: resolved})
		}
	}
	node.start.Attr = attrs
	for _, child := range node.children {
		if err := normalizeNode(child, vars, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func localFont(value string) string {
	if strings.Contains(value, "Menlo") || strings.Contains(value, "monospace") {
		return diagramMono
	}
	return diagramFont
}

func localValue(value string) error {
	lower := strings.ToLower(value)
	start := strings.Index(lower, "url(")
	if start >= 0 {
		rest := strings.TrimSpace(value[start+4:])
		if !strings.HasPrefix(rest, "#") && !strings.HasPrefix(rest, "'#") && !strings.HasPrefix(rest, "\"#") {
			return errors.New("图表不支持外部资源")
		}
	}
	if strings.Contains(lower, "color-mix(") || strings.Contains(lower, "@import") {
		return errors.New("图表样式无法转换")
	}
	return nil
}

func resolveCSS(value string, vars map[string]string, depth int) (string, error) {
	if depth > 24 {
		return "", errors.New("图表样式循环引用")
	}
	for {
		start := strings.Index(value, "var(")
		if start < 0 {
			return resolveColorMix(value)
		}
		level, end := 1, start+4
		for ; end < len(value) && level > 0; end++ {
			if value[end] == '(' {
				level++
			}
			if value[end] == ')' {
				level--
			}
		}
		if level != 0 {
			return "", errors.New("图表样式无效")
		}
		name, fallback, hasFallback := strings.Cut(value[start+4:end-1], ",")
		resolved, ok := vars[strings.TrimSpace(name)]
		if !ok {
			if !hasFallback {
				return "", errors.New("图表样式缺少变量")
			}
			resolved = strings.TrimSpace(fallback)
		}
		resolved, err := resolveCSS(resolved, vars, depth+1)
		if err != nil {
			return "", err
		}
		value = value[:start] + resolved + value[end:]
	}
}

type srgbColor struct {
	r, g, b, a float64
}

func resolveColorMix(value string) (string, error) {
	for range 32 {
		lower := strings.ToLower(value)
		start := strings.LastIndex(lower, "color-mix(")
		if start < 0 {
			return value, nil
		}
		level, end := 1, start+len("color-mix(")
		for ; end < len(value) && level > 0; end++ {
			if value[end] == '(' {
				level++
			} else if value[end] == ')' {
				level--
			}
		}
		if level != 0 {
			return "", errors.New("图表样式无法转换")
		}
		mixed, err := evaluateColorMix(value[start+len("color-mix(") : end-1])
		if err != nil {
			return "", err
		}
		value = value[:start] + mixed + value[end:]
	}
	return "", errors.New("图表样式无法转换")
}

func evaluateColorMix(inner string) (string, error) {
	parts := splitTopLevel(inner, ',')
	if len(parts) != 3 {
		return "", errors.New("图表样式无法转换")
	}
	if strings.Join(strings.Fields(strings.ToLower(parts[0])), " ") != "in srgb" {
		return "", errors.New("图表样式无法转换")
	}
	c1, p1, has1, err := parseColorMixComponent(parts[1])
	if err != nil {
		return "", err
	}
	c2, p2, has2, err := parseColorMixComponent(parts[2])
	if err != nil {
		return "", err
	}
	switch {
	case !has1 && !has2:
		p1, p2 = 50, 50
	case has1 && !has2:
		p2 = 100 - p1
	case !has1 && has2:
		p1 = 100 - p2
	}
	sum := p1 + p2
	if sum <= 0 {
		return "", errors.New("图表样式无法转换")
	}
	alphaMul := 1.0
	if sum < 100 {
		alphaMul = sum / 100
	}
	w1, w2 := p1/sum, p2/sum
	aInterp := c1.a*w1 + c2.a*w2
	aFinal := aInterp * alphaMul
	if aInterp <= 0 || aFinal <= 0 {
		return "rgba(0,0,0,0)", nil
	}
	r := clampByte((c1.r*c1.a*w1 + c2.r*c2.a*w2) / aInterp)
	g := clampByte((c1.g*c1.a*w1 + c2.g*c2.a*w2) / aInterp)
	b := clampByte((c1.b*c1.a*w1 + c2.b*c2.a*w2) / aInterp)
	if aFinal >= 1-1e-6 {
		return fmt.Sprintf("#%02x%02x%02x", r, g, b), nil
	}
	alpha := math.Round(min(1, aFinal)*10000) / 10000
	return fmt.Sprintf("rgba(%d,%d,%d,%s)", r, g, b, strconv.FormatFloat(alpha, 'f', -1, 64)), nil
}

func splitTopLevel(s string, sep byte) []string {
	var out []string
	level, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			level++
		case ')':
			if level > 0 {
				level--
			}
		case sep:
			if level == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

func splitTopLevelTokens(s string) []string {
	var out []string
	level, start := 0, -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' {
			if start < 0 {
				start = i
			}
			level++
			continue
		}
		if c == ')' {
			if level > 0 {
				level--
			}
			continue
		}
		if level == 0 && (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

func parseColorMixComponent(raw string) (srgbColor, float64, bool, error) {
	tokens := splitTopLevelTokens(strings.TrimSpace(raw))
	switch len(tokens) {
	case 1:
		c, err := parseSRGBColor(tokens[0])
		return c, 0, false, err
	case 2:
		if p, ok := parsePercentage(tokens[1]); ok {
			c, err := parseSRGBColor(tokens[0])
			return c, p, true, err
		}
		if p, ok := parsePercentage(tokens[0]); ok {
			c, err := parseSRGBColor(tokens[1])
			return c, p, true, err
		}
	}
	return srgbColor{}, 0, false, errors.New("图表样式无法转换")
}

func parsePercentage(s string) (float64, bool) {
	if !strings.HasSuffix(s, "%") {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
		return 0, false
	}
	return v, true
}

func parseSRGBColor(raw string) (srgbColor, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "transparent":
		return srgbColor{0, 0, 0, 0}, nil
	case "black":
		return srgbColor{0, 0, 0, 1}, nil
	case "white":
		return srgbColor{255, 255, 255, 1}, nil
	}
	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		switch len(hex) {
		case 3, 4:
			r, err1 := strconv.ParseUint(hex[0:1], 16, 8)
			g, err2 := strconv.ParseUint(hex[1:2], 16, 8)
			b, err3 := strconv.ParseUint(hex[2:3], 16, 8)
			if err1 != nil || err2 != nil || err3 != nil {
				return srgbColor{}, errors.New("图表样式无法转换")
			}
			a := 1.0
			if len(hex) == 4 {
				av, err4 := strconv.ParseUint(hex[3:4], 16, 8)
				if err4 != nil {
					return srgbColor{}, errors.New("图表样式无法转换")
				}
				a = float64(av*17) / 255
			}
			return srgbColor{float64(r * 17), float64(g * 17), float64(b * 17), a}, nil
		case 6, 8:
			r, err1 := strconv.ParseUint(hex[0:2], 16, 8)
			g, err2 := strconv.ParseUint(hex[2:4], 16, 8)
			b, err3 := strconv.ParseUint(hex[4:6], 16, 8)
			if err1 != nil || err2 != nil || err3 != nil {
				return srgbColor{}, errors.New("图表样式无法转换")
			}
			a := 1.0
			if len(hex) == 8 {
				av, err4 := strconv.ParseUint(hex[6:8], 16, 8)
				if err4 != nil {
					return srgbColor{}, errors.New("图表样式无法转换")
				}
				a = float64(av) / 255
			}
			return srgbColor{float64(r), float64(g), float64(b), a}, nil
		}
		return srgbColor{}, errors.New("图表样式无法转换")
	}
	if strings.HasPrefix(s, "rgb(") && strings.HasSuffix(s, ")") {
		parts := splitTopLevel(s[4:len(s)-1], ',')
		if len(parts) == 3 {
			r, ok1 := parseChannel(parts[0])
			g, ok2 := parseChannel(parts[1])
			b, ok3 := parseChannel(parts[2])
			if ok1 && ok2 && ok3 {
				return srgbColor{r, g, b, 1}, nil
			}
		}
		return srgbColor{}, errors.New("图表样式无法转换")
	}
	if strings.HasPrefix(s, "rgba(") && strings.HasSuffix(s, ")") {
		parts := splitTopLevel(s[5:len(s)-1], ',')
		if len(parts) == 4 {
			r, ok1 := parseChannel(parts[0])
			g, ok2 := parseChannel(parts[1])
			b, ok3 := parseChannel(parts[2])
			a, ok4 := parseAlpha(parts[3])
			if ok1 && ok2 && ok3 && ok4 {
				return srgbColor{r, g, b, a}, nil
			}
		}
		return srgbColor{}, errors.New("图表样式无法转换")
	}
	return srgbColor{}, errors.New("图表样式无法转换")
}

func parseChannel(s string) (float64, bool) {
	if p, ok := parsePercentage(s); ok {
		return p * 2.55, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return max(0, min(255, v)), true
}

func parseAlpha(s string) (float64, bool) {
	if p, ok := parsePercentage(s); ok {
		return p / 100, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return max(0, min(1, v)), true
}

func clampByte(v float64) int {
	return int(max(0, min(255, math.Round(v))))
}

func dimensions(root *svgNode) error {
	var w, h float64
	for _, attr := range root.start.Attr {
		if attr.Name.Local == "width" {
			w, _ = strconv.ParseFloat(strings.TrimSuffix(attr.Value, "px"), 64)
		}
		if attr.Name.Local == "height" {
			h, _ = strconv.ParseFloat(strings.TrimSuffix(attr.Value, "px"), 64)
		}
	}
	if w <= 0 || h <= 0 || math.IsNaN(w+h) || math.IsInf(w+h, 0) || w > 16384 || h > 16384 || w*h > 32e6 {
		return errors.New("图表尺寸超出渲染范围")
	}
	return nil
}

func encodeSVG(root *svgNode) ([]byte, error) {
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	var write func(*svgNode) error
	write = func(node *svgNode) error {
		if node.start.Name.Local == "" {
			return encoder.EncodeToken(xml.CharData(node.text))
		}
		start := node.start
		start.Name.Space = ""
		// Keep the default SVG namespace once, not on every descendant.
		if node == root {
			start.Attr = append([]xml.Attr{{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"}}, start.Attr...)
		}
		if err := encoder.EncodeToken(start); err != nil {
			return err
		}
		for _, child := range node.children {
			if err := write(child); err != nil {
				return err
			}
		}
		return encoder.EncodeToken(start.End())
	}
	if err := write(root); err != nil {
		return nil, err
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func checkVector(data []byte) error {
	root, err := readSVG(data)
	if err != nil {
		return err
	}
	if err = dimensions(root); err != nil {
		return err
	}
	var check func(*svgNode) error
	check = func(node *svgNode) error {
		if node.start.Name.Local == "text" || node.start.Name.Local == "tspan" {
			return errors.New("图表字体未完成转换")
		}
		for _, attr := range node.start.Attr {
			if strings.Contains(attr.Value, "var(") {
				return errors.New("图表样式未完成转换")
			}
			if attr.Name.Local == "href" && !strings.HasPrefix(attr.Value, "#") {
				return errors.New("图表不支持外部资源")
			}
			if err := localValue(attr.Value); err != nil {
				return err
			}
		}
		for _, child := range node.children {
			if err := check(child); err != nil {
				return err
			}
		}
		return nil
	}
	return check(root)
}
