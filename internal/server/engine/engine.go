package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/rusq/slackdump/v4/auth"
	"github.com/rusq/slackdump/v4/internal/chunk/backend/dbase"
	"github.com/rusq/slackdump/v4/internal/chunk/backend/dbase/repository"
	"github.com/rusq/slackdump/v4/internal/chunk/control"
	"github.com/rusq/slackdump/v4/internal/client"
	"github.com/rusq/slackdump/v4/internal/network"
	"github.com/rusq/slackdump/v4/internal/server/storage"
	"github.com/rusq/slackdump/v4/internal/server/store"
	"github.com/rusq/slackdump/v4/internal/structures"
	"github.com/rusq/slackdump/v4/stream"
)

// Engine wires the slackdump export pipeline to the server's store layer.
type Engine struct {
	store         *store.Store
	encryptionKey []byte
	dataDir       string
	pool          *WorkerPool
	storage       storage.Storage // file storage backend (filesystem or S3)
}

// New creates a new Engine. maxConcurrent controls how many export jobs can
// run simultaneously. If st is nil, a FilesystemStorage is used.
func New(s *store.Store, encryptionKey []byte, dataDir string, maxConcurrent int, st storage.Storage) *Engine {
	if st == nil {
		st = storage.NewFilesystemStorage(dataDir)
	}
	return &Engine{
		store:         s,
		encryptionKey: encryptionKey,
		dataDir:       dataDir,
		pool:          NewWorkerPool(maxConcurrent),
		storage:       st,
	}
}

// ResumeJobs finds any jobs left in pending or running state from a previous
// server instance and re-submits them to the worker pool.
func (e *Engine) ResumeJobs(ctx context.Context) error {
	jobs, err := e.store.Jobs.ListResumable(ctx)
	if err != nil {
		return fmt.Errorf("engine: list resumable jobs: %w", err)
	}
	for _, job := range jobs {
		slog.InfoContext(ctx, "resuming interrupted job", "job_id", job.ID, "tenant_id", job.TenantID)
		e.SubmitExport(ctx, job)
	}
	if n := len(jobs); n > 0 {
		slog.InfoContext(ctx, "resumed interrupted jobs", "count", n)
	}
	return nil
}

// SubmitExport enqueues an export job for asynchronous execution in the
// worker pool.
func (e *Engine) SubmitExport(ctx context.Context, job *store.ExportJob) {
	e.pool.Submit(ctx, job, e.RunExport)
}

// RunExport executes the full slackdump export pipeline for the given job.
// It updates the job status in the store on start, completion, or failure.
func (e *Engine) RunExport(ctx context.Context, job *store.ExportJob) error {
	lg := slog.With("job_id", job.ID, "tenant_id", job.TenantID)

	// Mark job as running.
	if err := e.store.Jobs.SetRunning(ctx, job.ID); err != nil {
		return fmt.Errorf("engine: set running: %w", err)
	}

	outputDir, err := e.runPipeline(ctx, lg, job)
	if err != nil {
		lg.ErrorContext(ctx, "export failed", "error", err)
		if sErr := e.store.Jobs.SetFailed(ctx, job.ID, err.Error()); sErr != nil {
			lg.ErrorContext(ctx, "failed to mark job as failed", "error", sErr)
		}
		return err
	}

	// Upload to storage after export completes.
	if e.storage != nil {
		tenant, tErr := e.store.Tenants.Get(ctx, job.TenantID)
		if tErr == nil {
			localPath := filepath.Join(outputDir, "slackdump.sqlite")
			s3Key := filepath.Join(job.TenantID, tenant.Workspace, "slackdump.sqlite")
			if uErr := e.storage.Upload(ctx, s3Key, localPath); uErr != nil {
				lg.ErrorContext(ctx, "storage upload failed (non-fatal)", "error", uErr)
			}
		}
	}

	if err := e.store.Jobs.SetCompleted(ctx, job.ID, outputDir); err != nil {
		return fmt.Errorf("engine: set completed: %w", err)
	}
	lg.InfoContext(ctx, "export completed", "output", outputDir)

	e.sendCompletionNotification(ctx, lg, job.TenantID)

	return nil
}

// runPipeline performs the actual export work and returns the output directory.
// It maintains a single archive per tenant, performing incremental updates
// (resume) when an existing archive is found.
func (e *Engine) runPipeline(ctx context.Context, lg *slog.Logger, job *store.ExportJob) (string, error) {
	// 1. Get credentials.
	cred, err := e.store.Credentials.GetByTenant(ctx, job.TenantID)
	if err != nil {
		return "", fmt.Errorf("get credentials: %w", err)
	}

	// Look up tenant to get workspace name.
	tenant, err := e.store.Tenants.Get(ctx, job.TenantID)
	if err != nil {
		return "", fmt.Errorf("get tenant: %w", err)
	}

	// 2. Decrypt token and cookie.
	token, err := store.Decrypt(e.encryptionKey, cred.TokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	var cookie []byte
	if len(cred.CookieEnc) > 0 {
		cookie, err = store.Decrypt(e.encryptionKey, cred.CookieEnc)
		if err != nil {
			return "", fmt.Errorf("decrypt cookie: %w", err)
		}
	}

	// 3. Create auth provider.
	prov, err := auth.NewValueAuth(string(token), string(cookie))
	if err != nil {
		return "", fmt.Errorf("create auth: %w", err)
	}

	// 4. Create Slack client.
	slackClient, err := client.New(ctx, prov)
	if err != nil {
		return "", fmt.Errorf("create client: %w", err)
	}

	// 5. Prepare output directory: {dataDir}/{tenantID}/{workspace}
	outputDir := filepath.Join(e.dataDir, job.TenantID, tenant.Workspace)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}

	// 6. Set up per-job log file.
	logDir := filepath.Join(e.dataDir, job.TenantID, tenant.Workspace, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.Create(filepath.Join(logDir, job.ID+".log"))
	if err != nil {
		return "", fmt.Errorf("create log file: %w", err)
	}
	defer logFile.Close()

	lg = slog.New(
		slog.NewTextHandler(
			io.MultiWriter(os.Stderr, logFile),
			&slog.HandlerOptions{Level: slog.LevelDebug},
		),
	).With("job_id", job.ID, "tenant_id", job.TenantID)

	// 7. Open database connection (local SQLite).
	var conn *sqlx.DB
	var list *structures.EntityList

	dbFile := filepath.Join(outputDir, "slackdump.sqlite")

	// Build resume list from existing local archive if present.
	if _, statErr := os.Stat(dbFile); statErr == nil {
		lg.InfoContext(ctx, "existing archive found, building resume list")
		src, openErr := dbase.Open(ctx, dbFile)
		if openErr != nil {
			return "", fmt.Errorf("open existing archive: %w", openErr)
		}
		latestMap, latestErr := src.Latest(ctx)
		src.Close()
		if latestErr != nil {
			return "", fmt.Errorf("read latest timestamps: %w", latestErr)
		}
		list = buildResumeList(latestMap, 7*24*time.Hour)
		lg.InfoContext(ctx, "resume list built", "channels", list.IncludeCount())
	}

	conn, err = sqlx.Open(repository.Driver, dbFile)
	if err != nil {
		return "", fmt.Errorf("open export db: %w", err)
	}
	defer conn.Close()

	// 8. Parse channel filter and merge with resume list.
	if channels := strings.TrimSpace(job.Channels); channels != "" {
		filterList, parseErr := parseChannels(channels)
		if parseErr != nil {
			return "", fmt.Errorf("parse channels: %w", parseErr)
		}
		if list != nil && filterList != nil {
			for id, item := range filterList.Index() {
				if _, exists := list.Get(id); !exists {
					list.Index()[id] = item
				}
			}
		} else if filterList != nil {
			list = filterList
		}
	}

	// 9. Create dbase processor.
	dbp, err := dbase.New(ctx, conn, dbase.SessionInfo{Mode: "server-export"})
	if err != nil {
		return "", fmt.Errorf("create db processor: %w", err)
	}

	// 11. Create stream with exclusive fetch (skip last known message).
	str := stream.New(slackClient, network.DefLimits, stream.OptInclusive(false))

	// 12. Create controller with per-job logger and refresh enabled.
	ctrl, err := control.New(
		ctx,
		str,
		dbp,
		control.WithFlags(control.Flags{RecordFiles: true, Refresh: true}),
		control.WithLogger(lg),
	)
	if err != nil {
		return "", fmt.Errorf("create controller: %w", err)
	}
	defer func() {
		if cErr := ctrl.Close(); cErr != nil {
			lg.ErrorContext(ctx, "error closing controller", "error", cErr)
		}
	}()

	// 13. Run export (with conversation transformer for resume tracking).
	start := time.Now()
	if err := ctrl.Run(ctx, list); err != nil {
		return "", fmt.Errorf("export: %w", err)
	}
	lg.InfoContext(ctx, "export pipeline finished", "took", time.Since(start))

	return outputDir, nil
}

// buildResumeList converts a map of per-channel latest timestamps into an
// EntityList suitable for incremental resume. Threads are excluded — only
// top-level channels are included. The lookBack duration is subtracted from
// each timestamp to provide overlap and avoid missing messages near boundaries.
func buildResumeList(latest map[structures.SlackLink]time.Time, lookBack time.Duration) *structures.EntityList {
	items := make([]structures.EntityItem, 0, len(latest))
	for sl, ts := range latest {
		if sl.IsThread() {
			continue
		}
		items = append(items, structures.EntityItem{
			Id:      sl.String(),
			Oldest:  ts.Add(-lookBack),
			Include: true,
		})
	}
	return structures.NewEntityListFromItems(items...)
}

// parseChannels splits a space-or-comma separated channel string into an
// EntityList.
func parseChannels(s string) (*structures.EntityList, error) {
	// Support both comma and space separated channel lists.
	s = strings.ReplaceAll(s, ",", " ")
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return nil, nil
	}
	return structures.NewEntityList(parts)
}

// Wait blocks until all submitted jobs finish.
func (e *Engine) Wait() {
	e.pool.Wait()
}

// Cancel cancels a running export job by its ID.
func (e *Engine) Cancel(jobID string) {
	e.pool.Cancel(jobID)
}
