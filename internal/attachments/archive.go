package attachments

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
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
	for _, suffix := range []string{"", ".json", ".part", ".retained"} {
		if err = root.Remove(filepath.Join(workspace, id+suffix)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Diff comments own a typed edge to their immutable source snapshot. Read it
// before deletion so the worker can persist children in its cleanup outbox.
func (r *Repository) ArchiveDependencies(workspace, id string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.base(workspace, id); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(r.Directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return archiveDependencies(root, workspace, id)
}

func archiveDependencies(root *os.Root, workspace, id string) ([]string, error) {
	meta, err := root.ReadFile(filepath.Join(workspace, id+".json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a domain.Attachment
	if json.Unmarshal(meta, &a) != nil || a.ID != id {
		return nil, errors.New("invalid archived resource descriptor")
	}
	if a.Kind != domain.DiffCommentResource {
		return nil, nil
	}
	data, err := root.ReadFile(filepath.Join(workspace, id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record domain.DiffCommentRecord
	if json.Unmarshal(data, &record) != nil || record.Snapshot.Kind != domain.DiffSnapshotResource || !validID(record.Snapshot.ID) {
		return nil, errors.New("invalid archived Diff comment")
	}
	return []string{record.Snapshot.ID}, nil
}

// Also protect snapshots held by another comment, including unsent originals.
// The workspace directory is private; OpenRoot constrains link traversal.
func (r *Repository) ArchiveResourceReferenced(workspace, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.base(workspace, id); err != nil {
		return false, err
	}
	root, err := os.OpenRoot(r.Directory)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Close()
	dir, err := root.Open(workspace)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		other := strings.TrimSuffix(entry.Name(), ".json")
		if !validID(other) {
			continue
		}
		dependencies, e := archiveDependencies(root, workspace, other)
		if e != nil {
			return false, e
		}
		for _, dependency := range dependencies {
			if dependency == id {
				return true, nil
			}
		}
	}
	return false, nil
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
