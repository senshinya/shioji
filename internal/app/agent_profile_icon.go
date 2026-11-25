package app

import (
	"bytes"
	"encoding/base64"
	"errors"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"shioji/internal/domain"
	"strings"
)

func ReadAgentProfileIcon(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("无法读取图标文件")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
	if err != nil || len(raw) > 5<<20 {
		return "", errors.New("请选择 5 MB 以内的图片")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 24_000_000 ||
		(format != "png" && format != "jpeg" && format != "webp") {
		return "", errors.New("请选择有效的 PNG、JPEG 或 WebP 图片")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("无法读取图标图片")
	}
	scale := min(1, 48.0/float64(max(cfg.Width, cfg.Height)))
	small := image.NewNRGBA(image.Rect(0, 0, max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), img, img.Bounds(), draw.Src, nil)
	var buffer bytes.Buffer
	if err = png.Encode(&buffer, small); err != nil {
		return "", errors.New("无法准备图标图片")
	}
	return domain.ProfilePNGPrefix + base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

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
