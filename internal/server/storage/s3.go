package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Storage uploads and downloads export files to/from an S3-compatible
// object store (AWS S3, Supabase Storage, Cloudflare R2, MinIO, etc.).
type S3Storage struct {
	client *s3.Client
	bucket string
}

// S3Config holds the configuration for S3Storage.
type S3Config struct {
	Endpoint  string // e.g. "https://…storage.supabase.co/storage/v1/s3"
	Bucket    string
	Region    string // default "auto"
	AccessKey string
	SecretKey string
}

func NewS3Storage(cfg S3Config) (*S3Storage, error) {
	if cfg.Region == "" {
		cfg.Region = "auto"
	}

	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		UsePathStyle: true,
	})

	return &S3Storage{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3Storage) Upload(ctx context.Context, key string, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("s3 upload: open %s: %w", localPath, err)
	}
	defer f.Close()

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	if err != nil {
		return fmt.Errorf("s3 upload: put %s: %w", key, err)
	}
	slog.InfoContext(ctx, "uploaded to S3", "key", key, "bucket", s.bucket)
	return nil
}

func (s *S3Storage) Download(ctx context.Context, key string, localPath string) error {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3 download: get %s: %w", key, err)
	}
	defer resp.Body.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("s3 download: mkdir: %w", err)
	}

	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("s3 download: create %s: %w", localPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("s3 download: copy: %w", err)
	}
	slog.InfoContext(ctx, "downloaded from S3", "key", key, "bucket", s.bucket)
	return out.Close()
}

func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// HeadObject returns an error for non-existent keys.
		return false, nil
	}
	return true, nil
}
