package attachments

import (
	"errors"
	"os"
	"path/filepath"
)

func (r *Repository) DeleteArchived(workspace, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.base(workspace, id); err != nil {
		return err
	}
	root, err := os.OpenRoot(r.Directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(workspace)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("附件目录不是独立目录")
	}
	for _, suffix := range []string{"", ".json", ".part"} {
		if err = root.Remove(filepath.Join(workspace, id+suffix)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (r *Repository) DeleteArchivedWorkspace(workspace string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !validID(workspace) || workspace == StagingWorkspace {
		return errors.New("invalid archive workspace identity")
	}
	root, err := os.OpenRoot(r.Directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	// RemoveAll removes a link itself, never its target. OpenRoot constrains
	// all traversal to the private attachment repository.
	return root.RemoveAll(workspace)
}
