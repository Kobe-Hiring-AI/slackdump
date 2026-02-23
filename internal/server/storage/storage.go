package storage

import "context"

// Storage abstracts file storage operations so the export engine can work
// with either local filesystem or a remote object store (S3, R2, etc.).
type Storage interface {
	Upload(ctx context.Context, key string, localPath string) error
	Download(ctx context.Context, key string, localPath string) error
	Exists(ctx context.Context, key string) (bool, error)
}
