// Package portforward owns application-local TCP mappings, independent of sessions.
package portforward

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

type Target struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func (t Target) Normalize() (Target, error) {
	if t.Port < 1 || t.Port > 65535 || t.Host == "" || strings.ContainsAny(t.Host, "/\\@?#") || strings.IndexFunc(t.Host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return t, errors.New("请输入有效目标地址与 1–65535 之间的端口")
	}
	t.Host = strings.TrimSuffix(strings.ToLower(t.Host), ".")
	if strings.HasPrefix(t.Host, "[") && strings.HasSuffix(t.Host, "]") {
		t.Host = t.Host[1 : len(t.Host)-1]
	}
	if ip, err := netip.ParseAddr(t.Host); err == nil {
		if ip.IsLinkLocalUnicast() && ip.Zone() == "" {
			return t, errors.New("链路本地 IPv6 地址需要 scope")
		}
		t.Host = ip.Unmap().String()
		return t, nil
	}
	if len(t.Host) > 253 {
		return t, errors.New("目标主机名过长")
	}
	for _, label := range strings.Split(t.Host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return t, errors.New("目标主机名无效")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return t, errors.New("目标主机名无效")
			}
		}
	}
	return t, nil
}
func (t Target) Validate() error { _, err := t.Normalize(); return err }
func (t Target) Address() string { return net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) }
func URLTarget(raw string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Target{}, errors.New("此链接不能创建端口转发")
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil {
			return Target{}, err
		}
	}
	t, err := (Target{u.Hostname(), port}).Normalize()
	if err != nil {
		return t, err
	}
	if t.Host == "localhost" {
		return t, nil
	}
	ip, err := netip.ParseAddr(t.Host)
	if err != nil {
		return t, errors.New("仅支持本机或局域网链接")
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() {
		if ip.Is4() {
			t.Host = "127.0.0.1"
		} else {
			t.Host = "::1"
		}
		return t, nil
	}
	if ip.IsLoopback() || ip.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return t, nil
	}
	return t, errors.New("仅支持本机或局域网链接")
}
func LocalURL(raw string, port int) (string, error) {
	if _, err := URLTarget(raw); err != nil {
		return "", err
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("本机端口无效")
	}
	u, _ := url.Parse(raw)
	u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return u.String(), nil
}
