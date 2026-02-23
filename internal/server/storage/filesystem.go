package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FilesystemStorage is a no-op storage for local-only deployments.
// Upload does nothing (the file is already on disk). Download copies from
// the data directory when source and destination differ.
type FilesystemStorage struct {
	dataDir string
}

func NewFilesystemStorage(dataDir string) *FilesystemStorage {
	return &FilesystemStorage{dataDir: dataDir}
}

func (f *FilesystemStorage) Upload(_ context.Context, _ string, _ string) error {
	return nil // file is already on local disk
}

func (f *FilesystemStorage) Download(_ context.Context, key string, localPath string) error {
	src := filepath.Join(f.dataDir, key)
	if src == localPath {
		return nil
	}
	return copyFile(src, localPath)
}

func (f *FilesystemStorage) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(filepath.Join(f.dataDir, key))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return out.Close()
}
