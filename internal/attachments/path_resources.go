package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"shioji/internal/domain"
)

// A path resource is an opaque immutable file, never a text or image input.
// Its only path is resolved by the owning worker at submission time.
func DescribePathResource(name string, data []byte) (domain.Attachment, error) {
	a, err := Describe(name, []byte("resource"))
	if err != nil {
		return a, err
	}
	if len(data) == 0 || len(data) > 8<<20 {
		return domain.Attachment{}, errors.New("文件为空或超过 8 MiB")
	}
	digest := sha256.Sum256(data)
	a.Kind, a.MIME = domain.PathAttachmentResource, "application/octet-stream"
	a.Size, a.SHA256 = int64(len(data)), hex.EncodeToString(digest[:])
	return a, nil
}

func (r *Repository) SavePathResource(name string, data []byte) (domain.Attachment, error) {
	a, err := DescribePathResource(name, data)
	if err != nil {
		return a, err
	}
	return r.save(a, data)
}
