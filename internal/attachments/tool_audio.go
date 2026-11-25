package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"shioji/internal/domain"
)

// DescribeToolAudio checks the bytes independently of the provider and uses the
// same filename rules as other immutable attachments.
func DescribeToolAudio(name string, data []byte) (domain.Attachment, error) {
	if len(data) == 0 || len(data) > 8<<20 {
		return domain.Attachment{}, errors.New("音频为空或超过 8 MiB")
	}
	a, err := Describe(name, []byte("resource"))
	if err != nil {
		return a, err
	}
	switch ext, detected := strings.ToLower(filepath.Ext(name)), http.DetectContentType(data); {
	case ext == ".wav" && detected == "audio/wave":
		a.MIME = "audio/wav"
	case ext == ".mp3" && detected == "audio/mpeg":
		a.MIME = "audio/mpeg"
	default:
		return domain.Attachment{}, errors.New("工具结果不是支持的 WAV 或 MP3 音频")
	}
	digest := sha256.Sum256(data)
	a.Kind, a.Size, a.SHA256 = domain.ToolAudioResource, int64(len(data)), hex.EncodeToString(digest[:])
	return a, nil
}
