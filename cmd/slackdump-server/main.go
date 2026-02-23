package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rusq/slackdump/v4/internal/server"
	"github.com/rusq/slackdump/v4/internal/server/auth"
	"github.com/rusq/slackdump/v4/internal/server/engine"
	"github.com/rusq/slackdump/v4/internal/server/storage"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr              = flag.String("addr", envOr("SLACKDUMP_ADDR", ":8080"), "listen address")
		dataDir           = flag.String("data-dir", envOr("SLACKDUMP_DATA_DIR", "./data"), "data directory")
		adminKey          = flag.String("admin-key", os.Getenv("SLACKDUMP_ADMIN_KEY"), "admin API key (required)")
		encKeyHex         = flag.String("encryption-key", os.Getenv("SLACKDUMP_ENCRYPTION_KEY"), "32-byte hex encryption key (required)")
		maxConcurrent     = flag.Int("max-concurrent", envOrInt("SLACKDUMP_MAX_CONCURRENT", 4), "max concurrent exports")
		jwtPrivateKeyPath = flag.String("jwt-private-key", os.Getenv("SLACKDUMP_JWT_PRIVATE_KEY"), "path to Ed25519 PEM private key for JWT signing")
		sqldPublicURL     = flag.String("sqld-public-url", os.Getenv("SLACKDUMP_SQLD_PUBLIC_URL"), "public URL for sqld (returned in /connect)")
		sqldAdminURL      = flag.String("sqld-admin-url", os.Getenv("SLACKDUMP_SQLD_ADMIN_URL"), "sqld admin API URL for namespace management")
		dbPath            = flag.String("db", os.Getenv("SLACKDUMP_DB"), "management database path (default: <data-dir>/server.sqlite)")
		storageBackend    = flag.String("storage-backend", envOr("SLACKDUMP_STORAGE_BACKEND", "filesystem"), "storage backend: filesystem or s3")
		s3Endpoint        = flag.String("s3-endpoint", os.Getenv("SLACKDUMP_S3_ENDPOINT"), "S3 endpoint URL")
		s3Bucket          = flag.String("s3-bucket", os.Getenv("SLACKDUMP_S3_BUCKET"), "S3 bucket name")
		s3Region          = flag.String("s3-region", envOr("SLACKDUMP_S3_REGION", "auto"), "S3 region")
		s3AccessKey       = flag.String("s3-access-key", os.Getenv("SLACKDUMP_S3_ACCESS_KEY"), "S3 access key ID")
		s3SecretKey       = flag.String("s3-secret-key", os.Getenv("SLACKDUMP_S3_SECRET_KEY"), "S3 secret access key")
	)
	flag.Parse()

	if *adminKey == "" {
		return errors.New("--admin-key or SLACKDUMP_ADMIN_KEY is required")
	}
	if *encKeyHex == "" {
		return errors.New("--encryption-key or SLACKDUMP_ENCRYPTION_KEY is required")
	}
	encKey, err := hex.DecodeString(*encKeyHex)
	if err != nil {
		return fmt.Errorf("invalid encryption key: %w", err)
	}
	if len(encKey) != 32 {
		return fmt.Errorf("encryption key must be 32 bytes (64 hex chars), got %d bytes", len(encKey))
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *dbPath == "" {
		*dbPath = filepath.Join(*dataDir, "server.sqlite")
	}
	st, err := store.New(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	defer st.Close()

	// Initialize storage backend.
	var fileStore storage.Storage
	switch *storageBackend {
	case "s3":
		s3Store, s3Err := storage.NewS3Storage(storage.S3Config{
			Endpoint:  *s3Endpoint,
			Bucket:    *s3Bucket,
			Region:    *s3Region,
			AccessKey: *s3AccessKey,
			SecretKey: *s3SecretKey,
		})
		if s3Err != nil {
			return fmt.Errorf("init S3 storage: %w", s3Err)
		}
		fileStore = s3Store
		slog.Info("storage backend: S3", "endpoint", *s3Endpoint, "bucket", *s3Bucket)
	default:
		fileStore = storage.NewFilesystemStorage(*dataDir)
		slog.Info("storage backend: filesystem")
	}

	eng := engine.New(st, encKey, *dataDir, *maxConcurrent, fileStore)

	// Re-queue any jobs that were interrupted by a previous crash/restart.
	if err := eng.ResumeJobs(ctx); err != nil {
		return fmt.Errorf("resume jobs: %w", err)
	}

	sched := engine.NewScheduler(st, eng)

	// Load JWT token issuer if configured.
	var issuer *auth.TokenIssuer
	if *jwtPrivateKeyPath != "" {
		issuer, err = auth.NewTokenIssuer(*jwtPrivateKeyPath)
		if err != nil {
			return fmt.Errorf("load JWT private key: %w", err)
		}
		slog.Info("JWT token issuer loaded", "key_path", *jwtPrivateKeyPath)
	}

	// Configure engine for sqld writes if both URL and issuer are available.
	if *sqldPublicURL != "" && issuer != nil {
		eng.SetSqld(*sqldPublicURL, issuer)
		slog.Info("engine configured for sqld writes", "sqld_url", *sqldPublicURL)
	}

	cfg := server.Config{
		Addr:              *addr,
		DataDir:           *dataDir,
		AdminKey:          *adminKey,
		EncryptionKey:     encKey,
		MaxConcurrent:     *maxConcurrent,
		JWTPrivateKeyPath: *jwtPrivateKeyPath,
		SqldPublicURL:     *sqldPublicURL,
		SqldAdminURL:      *sqldAdminURL,
	}
	srv := server.NewServer(cfg, st, eng, issuer, fileStore)

	// Start scheduler in background.
	go func() {
		if err := sched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("scheduler error", "error", err)
		}
	}()

	// Start HTTP server in background.
	errC := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", cfg.Addr, "data_dir", cfg.DataDir, "docs", "http://localhost"+cfg.Addr+"/docs")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errC <- err
		}
		close(errC)
	}()

	// Wait for shutdown signal or server error.
	select {
	case <-ctx.Done():
		slog.Info("shutting down...")
	case err := <-errC:
		return err
	}

	// Graceful shutdown with timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	// Wait for running exports to finish.
	eng.Wait()
	slog.Info("server stopped")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}
