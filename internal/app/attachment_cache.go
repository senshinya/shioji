package app

import (
	"context"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"sync"
	"time"
)

// Only this dedicated repository is disposable. Legacy mixed attachment files
// stay in originals; their provenance cannot safely be reconstructed.
type AttachmentCache struct {
	mu         sync.Mutex
	repository *attachments.Repository
}

func NewAttachmentCache(directory string) *AttachmentCache {
	return &AttachmentCache{repository: attachments.New(directory)}
}

// Read keeps the file pinned through decoding or Save As. Cleanup cannot race
// a download, an image decode, a text read or an export.
func (c *AttachmentCache) Read(ctx context.Context, target AttachmentTarget, originals *attachments.Repository, a domain.Attachment, consume func(string) error) error {
	if path, err := originals.Path(attachments.StagingWorkspace, a); err == nil {
		return consume(path)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	path, err := target.Download(ctx, c.repository, a)
	if err != nil {
		return err
	}
	return consume(path)
}

func (c *AttachmentCache) Storage(clean bool) (domain.StorageReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.repository.Storage(time.Now(), clean, true)
}
