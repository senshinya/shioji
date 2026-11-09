package worker

import (
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"sort"
	"strings"
	"unicode/utf8"
)

func (s *Server) directories(q protocol.DirectoryQuery) (domain.DirectoryPage, error) {
	var out domain.DirectoryPage
	home, err := os.UserHomeDir()
	if err != nil {
		return out, err
	}
	name := q.Path
	if name == "" || name == "~" {
		name = home
	} else if strings.HasPrefix(name, "~/") {
		name = filepath.Join(home, name[2:])
	}
	if !filepath.IsAbs(name) || len(name) > 4096 || !utf8.ValidString(name) || strings.ContainsRune(name, 0) || q.Offset < 0 {
		return out, errors.New("请输入设备上的绝对目录路径")
	}
	name = filepath.Clean(name)
	entries, err := os.ReadDir(name)
	if err != nil {
		if os.IsPermission(err) {
			return out, &protocol.RemoteError{Code: "permissionDenied", Message: "无法访问此文件夹"}
		}
		if os.IsNotExist(err) {
			return out, &protocol.RemoteError{Code: "notFound", Message: "找不到此文件夹"}
		}
		return out, errors.New("无法读取此文件夹")
	}
	folders := make([]domain.DirectoryEntry, 0)
	for _, entry := range entries {
		if !q.Hidden && strings.HasPrefix(entry.Name(), ".") || !utf8.ValidString(entry.Name()) {
			continue
		}
		full := filepath.Join(name, entry.Name())
		dir := entry.IsDir()
		if !dir && entry.Type()&os.ModeSymlink != 0 {
			info, e := os.Stat(full)
			dir = e == nil && info.IsDir()
		}
		if dir {
			folders = append(folders, domain.DirectoryEntry{Name: entry.Name(), Path: full})
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		a, b := strings.ToLower(folders[i].Name), strings.ToLower(folders[j].Name)
		if a == b {
			return folders[i].Name < folders[j].Name
		}
		return a < b
	})
	start := min(q.Offset, len(folders))
	end := min(start+200, len(folders))
	out = domain.DirectoryPage{Path: name, Parent: filepath.Dir(name), Home: home, Entries: folders[start:end], Next: end, More: end < len(folders)}
	return out, nil
}
