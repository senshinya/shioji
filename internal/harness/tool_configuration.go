package harness

import (
	"errors"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"shioji/internal/domain"
)

func RetainedToolValues(values map[string]string) []domain.ToolServiceValue {
	out := []domain.ToolServiceValue{}
	for name := range values {
		out = append(out, domain.ToolServiceValue{Name: name, Retained: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func ValidateToolConfiguration(in domain.ToolServiceConfiguration) error {
	if strings.TrimSpace(in.Name) != in.Name || in.Name == "" || len(in.Name) > 128 || strings.ContainsAny(in.Name, "\x00\r\n") {
		return errors.New("请输入有效的服务名称")
	}
	if in.Transport != "command" && in.Transport != "http" {
		return errors.New("请选择连接方式")
	}
	if in.Transport == "command" {
		if strings.TrimSpace(in.Command) == "" || len(in.Command) > 4096 || strings.ContainsRune(in.Command, 0) {
			return errors.New("请输入执行命令")
		}
		if in.Directory != "" && !strings.HasPrefix(in.Directory, "/") {
			return errors.New("工作目录须为所属设备的绝对路径")
		}
	} else {
		u, e := url.Parse(in.URL)
		if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || len(in.URL) > 8192 {
			return errors.New("请输入不含凭据的 HTTP 或 HTTPS 地址")
		}
	}
	if in.StartupSeconds < 0 || in.StartupSeconds > 600 {
		return errors.New("启动超时须在 0 到 600 秒之间")
	}
	if len(in.Arguments) > 256 || len(in.Environment) > 128 || len(in.Headers) > 128 || len(in.InheritedEnvironment) > 128 {
		return errors.New("配置项过多")
	}
	for _, arg := range in.Arguments {
		if len(arg) > 16<<10 || strings.ContainsRune(arg, 0) {
			return errors.New("命令参数无效")
		}
	}
	for _, name := range append(append([]string{}, in.InheritedEnvironment...), in.TokenEnvironment) {
		if name != "" && !toolEnvironmentName(name) {
			return errors.New("环境变量名无效")
		}
	}
	return nil
}
func toolEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
func MergeToolValues(rows []domain.ToolServiceValue, previous map[string]string, headers bool) (map[string]string, error) {
	values := map[string]string{}
	seen := map[string]bool{}
	for _, entry := range rows {
		if entry.Name == "" || len(entry.Name) > 256 || strings.ContainsAny(entry.Name, "\x00\r\n:") || !headers && !toolEnvironmentName(entry.Name) {
			return nil, errors.New("请检查变量或请求头名称")
		}
		identity := entry.Name
		if headers {
			identity = strings.ToLower(identity)
			for _, r := range entry.Name {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
					return nil, errors.New("请求头名称无效")
				}
			}
		}
		if seen[identity] {
			return nil, errors.New("变量或请求头名称不能重复")
		}
		seen[identity] = true
		if len(entry.Value) > 64<<10 || strings.ContainsRune(entry.Value, 0) || headers && strings.ContainsAny(entry.Value, "\r\n") {
			return nil, errors.New("变量或请求头值无效")
		}
		value := entry.Value
		if entry.Retained {
			var ok bool
			value, ok = previous[entry.Name]
			if !ok || entry.Value != "" {
				return nil, errors.New("原有私密值已变化，请重新打开编辑")
			}
		}
		values[entry.Name] = value
	}
	return values, nil
}
