package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"shioji/internal/domain"
)

// Export writes a verified local snapshot to the location chosen in Save As.
// Complete the temporary copy before replacing an existing destination.
func (r *Repository) Export(a domain.Attachment, destination string) error {
	source, err := r.Path(StagingWorkspace, a)
	if err != nil {
		return err
	}
	return ExportFile(a, source, destination)
}

// ExportFile is also used while a disposable display copy is pinned by its
// cache owner. It verifies bytes before replacing the selected destination.
func ExportFile(a domain.Attachment, source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != a.Size || hex.EncodeToString(digest[:]) != a.SHA256 {
		return errors.New("本机文件副本校验失败，请重新下载")
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".shioji-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), destination)
}
