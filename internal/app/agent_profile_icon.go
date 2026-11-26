package app

import (
	"encoding/base64"
	"image"
	"image/png"
	"strings"

	"shioji/internal/domain"
)

func AgentProfileIconImage(value string) image.Image {
	if !strings.HasPrefix(value, domain.ProfilePNGPrefix) || !domain.ValidProfileIcon(value) {
		return nil
	}
	data := base64.NewDecoder(base64.StdEncoding, strings.NewReader(strings.TrimPrefix(value, domain.ProfilePNGPrefix)))
	img, err := png.Decode(data)
	if err != nil {
		return nil
	}
	return img
}
