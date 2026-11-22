package app

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type FileLinkPosition struct {
	Line, Column, LastLine int // One-based; zero means no source location.
}

var fileLinkLocation = regexp.MustCompile(`:([0-9]+)(?::([0-9]+))?$`)
var fileLinkFragment = regexp.MustCompile(`^L([0-9]+)(?:-L?([0-9]+))?$`)

// FileLinkTarget interprets a Markdown destination without opening it.
func FileLinkTarget(dest string) (string, FileLinkPosition, error) {
	var position FileLinkPosition
	if match := fileLinkLocation.FindStringSubmatch(dest); match != nil {
		position.Line, _ = strconv.Atoi(match[1])
		position.Column, _ = strconv.Atoi(match[2])
		dest = strings.TrimSuffix(dest, match[0])
	}
	u, err := url.Parse(dest)
	if err != nil || u.Opaque != "" || u.User != nil || (u.Scheme != "" && u.Scheme != "file") || (u.Host != "" && u.Host != "localhost") || strings.HasPrefix(dest, "//") {
		return "", position, errors.New("不支持的文件链接")
	}
	if match := fileLinkFragment.FindStringSubmatch(u.Fragment); match != nil {
		position.Line, _ = strconv.Atoi(match[1])
		position.LastLine, _ = strconv.Atoi(match[2])
	}
	name := u.Path
	if u.Scheme == "file" && !path.IsAbs(name) {
		return "", position, errors.New("文件地址必须是绝对路径")
	}
	if name == "" || len(name) > 4096 || strings.ContainsAny(name, "\x00\r\n\\") {
		return "", position, errors.New("文件路径无效")
	}
	return name, position, nil
}
