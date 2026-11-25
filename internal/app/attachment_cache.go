package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"strings"
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

// LinkedAudio exports a verified snapshot under its media name so Launch
// Services can pick a player. The copy is counted in the linked-files cache.
func (c *AttachmentCache) LinkedAudio(ctx context.Context, target AttachmentTarget, originals *attachments.Repository, a domain.Attachment, directory string) (LinkedFile, error) {
	if a.Kind != domain.ToolAudioResource || strings.ContainsAny(a.Name, "/\\\x00\r\n") ||
		!(a.MIME == "audio/wav" && strings.EqualFold(filepath.Ext(a.Name), ".wav") ||
			a.MIME == "audio/mpeg" && strings.EqualFold(filepath.Ext(a.Name), ".mp3")) {
		return LinkedFile{}, errors.New("无效的工具音频副本")
	}
	identity := sha256.Sum256([]byte(target.DeviceID + "\x00" + target.WorkspaceID + "\x00" + a.ID + "\x00" + a.SHA256))
	folder := filepath.Join(directory, hex.EncodeToString(identity[:]))
	out := LinkedFile{Resource: domain.FileResource{Name: a.Name, MIME: a.MIME, Size: a.Size},
		Path: filepath.Join(folder, a.Name), SHA256: a.SHA256}
	err := c.Read(ctx, target, originals, a, func(path string) error {
		linkedFileCacheMu.RLock()
		defer linkedFileCacheMu.RUnlock()
		if err := os.MkdirAll(folder, 0700); err != nil {
			return err
		}
		return attachments.ExportFile(a, path, out.Path)
	})
	return out, err
}
