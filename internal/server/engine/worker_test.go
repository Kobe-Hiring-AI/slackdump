package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

func TestWorkerPool_ConcurrencyLimit(t *testing.T) {
	const maxConcurrent = 2
	const totalJobs = 6

	pool := NewWorkerPool(maxConcurrent)

	var running atomic.Int32
	var maxSeen atomic.Int32

	ctx := context.Background()
	for i := range totalJobs {
		job := &store.ExportJob{ID: idFromInt(i)}
		pool.Submit(ctx, job, func(ctx context.Context, _ *store.ExportJob) error {
			cur := running.Add(1)
			// Track peak concurrency.
			for {
				old := maxSeen.Load()
				if cur <= old || maxSeen.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			running.Add(-1)
			return nil
		})
	}

	pool.Wait()

	if peak := maxSeen.Load(); peak > maxConcurrent {
		t.Errorf("peak concurrency %d exceeded limit %d", peak, maxConcurrent)
	}
	if r := running.Load(); r != 0 {
		t.Errorf("expected 0 running after Wait, got %d", r)
	}
}

func TestWorkerPool_Cancel(t *testing.T) {
	pool := NewWorkerPool(1)
	ctx := context.Background()

	started := make(chan struct{})
	job := &store.ExportJob{ID: "cancel-me"}

	pool.Submit(ctx, job, func(ctx context.Context, _ *store.ExportJob) error {
		close(started)
		// Block until cancelled.
		<-ctx.Done()
		return ctx.Err()
	})

	// Wait for the job to start running.
	<-started

	if !pool.IsRunning("cancel-me") {
		t.Fatal("expected job to be running before cancel")
	}

	pool.Cancel("cancel-me")
	pool.Wait()

	if pool.IsRunning("cancel-me") {
		t.Error("expected job to no longer be running after cancel + wait")
	}
}

func TestWorkerPool_WaitCompletes(t *testing.T) {
	pool := NewWorkerPool(4)
	ctx := context.Background()

	var mu sync.Mutex
	completed := make(map[string]bool)

	const n = 10
	for i := range n {
		job := &store.ExportJob{ID: idFromInt(i)}
		pool.Submit(ctx, job, func(_ context.Context, j *store.ExportJob) error {
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			completed[j.ID] = true
			mu.Unlock()
			return nil
		})
	}

	pool.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(completed) != n {
		t.Errorf("expected %d completed jobs, got %d", n, len(completed))
	}
}

func TestWorkerPool_CancelNonExistent(t *testing.T) {
	pool := NewWorkerPool(1)
	// Should not panic.
	pool.Cancel("does-not-exist")
}

func TestWorkerPool_IsRunning(t *testing.T) {
	pool := NewWorkerPool(1)

	if pool.IsRunning("nope") {
		t.Error("expected IsRunning to be false for unknown job")
	}
}

func idFromInt(i int) string {
	return "job-" + time.Duration(i).String()
}
