package engine

import (
	"context"
	"log/slog"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// ExportFunc is the function signature for running an export job.
type ExportFunc func(ctx context.Context, job *store.ExportJob) error

// WorkerPool manages concurrent export job execution with a semaphore-based
// limit.
type WorkerPool struct {
	sem     *semaphore.Weighted
	wg      sync.WaitGroup
	mu      sync.Mutex
	running map[string]context.CancelFunc // job ID -> cancel func
}

// NewWorkerPool creates a new pool that allows at most maxConcurrent jobs to
// run simultaneously.
func NewWorkerPool(maxConcurrent int) *WorkerPool {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &WorkerPool{
		sem:     semaphore.NewWeighted(int64(maxConcurrent)),
		running: make(map[string]context.CancelFunc),
	}
}

// Submit enqueues a job for execution. It acquires the semaphore in a
// goroutine, so it returns immediately. The provided fn is called once
// the semaphore slot is available.
func (p *WorkerPool) Submit(ctx context.Context, job *store.ExportJob, fn ExportFunc) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		// Block until a slot is available or the context is cancelled.
		if err := p.sem.Acquire(ctx, 1); err != nil {
			slog.ErrorContext(ctx, "worker: failed to acquire semaphore", "job_id", job.ID, "error", err)
			return
		}
		defer p.sem.Release(1)

		jobCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		p.mu.Lock()
		p.running[job.ID] = cancel
		p.mu.Unlock()

		defer func() {
			p.mu.Lock()
			delete(p.running, job.ID)
			p.mu.Unlock()
		}()

		if err := fn(jobCtx, job); err != nil {
			slog.ErrorContext(ctx, "worker: job failed", "job_id", job.ID, "error", err)
		}
	}()
}

// Wait blocks until all submitted jobs have completed.
func (p *WorkerPool) Wait() {
	p.wg.Wait()
}

// Cancel cancels a running job identified by jobID. It is a no-op if the
// job is not currently running.
func (p *WorkerPool) Cancel(jobID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cancel, ok := p.running[jobID]; ok {
		cancel()
	}
}

// IsRunning reports whether the job identified by jobID is currently
// executing.
func (p *WorkerPool) IsRunning(jobID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.running[jobID]
	return ok
}
