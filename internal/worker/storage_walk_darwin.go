package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Keep each directory open while reading its children. Re-resolving every
// descendant from the top-level os.Root makes a deep worktree need many extra
// openat/stat calls per file. Relative, no-follow descriptors retain confinement
// without that repeated traversal. The count remains logical regular-file bytes.
func walkStorageFiles(ctx context.Context, dir *os.File, prefix string, visit func(string, int64) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(256)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			var st unix.Stat_t
			if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				if errors.Is(err, unix.ENOENT) {
					continue
				}
				return err
			}
			relative := filepath.Join(prefix, name)
			switch st.Mode & unix.S_IFMT {
			case unix.S_IFREG:
				if err := visit(relative, st.Size); err != nil {
					return err
				}
			case unix.S_IFDIR:
				if relative == "attachments" {
					continue
				}
				fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if err != nil {
					if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
						continue
					}
					return err
				}
				child := os.NewFile(uintptr(fd), relative)
				err = walkStorageFiles(ctx, child, relative, visit)
				closeErr := child.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
