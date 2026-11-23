package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"shioji/internal/domain"
	"shioji/internal/systemexec"
)

func (Adapter) OpenSessionOutputFile(ctx context.Context, session domain.Session, path string) (*os.File, error) {
	thread := session.ThreadID
	if !filepath.IsLocal(thread) || thread == "." || filepath.Base(thread) != thread {
		return nil, nil
	}
	dirs := []string{"antigravity-cli", "brain", thread}
	outputDir := filepath.Join(dirs...)
	if !strings.Contains(path, "/"+outputDir+"/") {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, systemexec.ShellEnvironmentTimeout)
	defer cancel()
	env, _ := systemexec.Environment(ctx, nil)
	profile := skillProfileDirectory(env)
	if !filepath.IsAbs(profile) {
		return nil, nil
	}
	rel, err := filepath.Rel(filepath.Join(profile, outputDir), path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return nil, nil
	}
	root, err := os.OpenRoot(profile)
	if err != nil {
		return nil, err
	}
	// Walk the fixed hierarchy without letting a symlink rebind this session
	// to another directory. OpenRoot confines the final read to the held root.
	for _, name := range dirs {
		child, err := openArtifactDirectory(root, name)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = child
	}
	defer root.Close()
	return root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
}

func openArtifactDirectory(root *os.Root, name string) (*os.Root, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, os.ErrPermission
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := child.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		child.Close()
		return nil, os.ErrPermission
	}
	return child, nil
}
