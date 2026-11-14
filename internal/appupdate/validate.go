// Package appupdate connects the GUI updater to Shioji's release identity.
// It does not own worker, harness or Tailnet lifecycle.
package appupdate

import (
	"context"
	"debug/macho"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type bundleInfo struct {
	Identifier string `json:"CFBundleIdentifier"`
	Executable string `json:"CFBundleExecutable"`
	Version    string `json:"CFBundleShortVersionString"`
	Build      string `json:"CFBundleVersion"`
	Minimum    string `json:"LSMinimumSystemVersion"`
}

var numericVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?$`)
var signingTeam = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func validateInfo(p bundleInfo, version, system string) error {
	if p.Identifier != "dev.shioji.app" || p.Executable != "shioji" {
		return errors.New("更新包不是 Shioji")
	}
	if p.Version != version || p.Build != version || !numericVersion.MatchString(version) {
		return errors.New("更新包版本与发布信息不一致")
	}
	if !numericVersion.MatchString(p.Minimum) || !numericVersion.MatchString(system) {
		return errors.New("无法确认更新所需的 macOS 版本")
	}
	minimum, current := strings.Split(p.Minimum, "."), strings.Split(system, ".")
	for i := 0; i < 3; i++ {
		m, c := 0, 0
		if i < len(minimum) {
			m, _ = strconv.Atoi(minimum[i])
		}
		if i < len(current) {
			c, _ = strconv.Atoi(current[i])
		}
		if m > c {
			return fmt.Errorf("此更新需要 macOS %s 或更新版本", p.Minimum)
		}
		if m < c {
			break
		}
	}
	return nil
}

// Validate inspects an unpacked bundle without executing candidate code.
// The team comes from the release build, never from the downloaded manifest.
func Validate(ctx context.Context, bundle, version, team string) error {
	if !signingTeam.MatchString(team) {
		return errors.New("此版本未配置更新签名身份")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	requirement := `identifier "dev.shioji.app" and anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = "` + team + `"`
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R="+requirement, bundle).Run(); err != nil {
		return errors.New("更新包的开发者签名校验失败")
	}
	data, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", filepath.Join(bundle, "Contents", "Info.plist")).Output()
	if err != nil {
		return errors.New("无法读取更新包信息")
	}
	var p bundleInfo
	if json.Unmarshal(data, &p) != nil {
		return errors.New("更新包信息无效")
	}
	system, err := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return errors.New("无法读取当前 macOS 版本")
	}
	if err := validateInfo(p, version, strings.TrimSpace(string(system))); err != nil {
		return err
	}
	binary := filepath.Join(bundle, "Contents", "MacOS", "shioji")
	if f, err := macho.Open(binary); err == nil {
		defer f.Close()
		if f.Cpu == macho.CpuArm64 {
			return ctx.Err()
		}
	}
	if f, err := macho.OpenFat(binary); err == nil {
		defer f.Close()
		for _, arch := range f.Arches {
			if arch.Cpu == macho.CpuArm64 {
				return ctx.Err()
			}
		}
	}
	return errors.New("更新包不支持 Apple Silicon")
}
