package domain

import (
	"encoding/base64"
	"image/png"
	"strings"
)

const ProfilePNGPrefix = "data:image/png;base64,"

func ValidProfileIcon(icon string) bool {
	switch icon {
	case "", "profile-sliders", "profile-terminal", "profile-code", "profile-cube", "profile-spark", "profile-bolt", "profile-orbit", "claude":
		return true
	}
	if !strings.HasPrefix(icon, ProfilePNGPrefix) || len(icon) > 20<<10 {
		return false
	}
	data := base64.NewDecoder(base64.StdEncoding, strings.NewReader(strings.TrimPrefix(icon, ProfilePNGPrefix)))
	cfg, err := png.DecodeConfig(data)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 48 || cfg.Height > 48 {
		return false
	}
	data = base64.NewDecoder(base64.StdEncoding, strings.NewReader(strings.TrimPrefix(icon, ProfilePNGPrefix)))
	_, err = png.Decode(data)
	return err == nil
}
