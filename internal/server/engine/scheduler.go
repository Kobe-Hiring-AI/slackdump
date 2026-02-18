package engine

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

const defaultTickInterval = 5 * time.Minute

// Scheduler periodically checks for due export schedules and submits them
// to the engine for execution.
type Scheduler struct {
	store    *store.Store
	engine   *Engine
	interval time.Duration
}

// NewScheduler creates a new Scheduler that checks for due schedules at a
// default interval of 5 minutes.
func NewScheduler(s *store.Store, engine *Engine) *Scheduler {
	return &Scheduler{
		store:    s,
		engine:   engine,
		interval: defaultTickInterval,
	}
}

// Run blocks until ctx is cancelled, periodically polling for due schedules.
// On each tick it creates export jobs for any due schedules and updates their
// next run time.
func (s *Scheduler) Run(ctx context.Context) error {
	lg := slog.With("component", "scheduler")

	// Check immediately on startup for catch-up.
	s.tick(ctx, lg)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			lg.InfoContext(ctx, "scheduler shutting down")
			return ctx.Err()
		case <-ticker.C:
			s.tick(ctx, lg)
		}
	}
}

// tick performs a single scheduler cycle: query due schedules, create jobs,
// and update next_run.
func (s *Scheduler) tick(ctx context.Context, lg *slog.Logger) {
	now := time.Now().UTC()
	due, err := s.store.Schedules.ListDue(ctx, now)
	if err != nil {
		lg.ErrorContext(ctx, "failed to list due schedules", "error", err)
		return
	}
	for _, sched := range due {
		lg.InfoContext(ctx, "triggering scheduled export", "schedule_id", sched.ID, "tenant_id", sched.TenantID)

		job := &store.ExportJob{
			ID:          generateID(),
			TenantID:    sched.TenantID,
			Status:      store.JobStatusPending,
			TriggeredBy: "scheduler",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.store.Jobs.Create(ctx, job); err != nil {
			lg.ErrorContext(ctx, "failed to create scheduled job", "schedule_id", sched.ID, "error", err)
			continue
		}

		s.engine.SubmitExport(ctx, job)

		nextRun := computeNextRun(now, sched.HourUTC, sched.JitterMin)
		if err := s.store.Schedules.UpdateLastRun(ctx, sched.ID, now, nextRun); err != nil {
			lg.ErrorContext(ctx, "failed to update schedule last run", "schedule_id", sched.ID, "error", err)
		}
	}
}

// computeNextRun calculates the next run time given the current time, the
// desired hour in UTC, and a jitter range in minutes. If the computed time
// for today has already passed, it schedules for the next day.
func computeNextRun(now time.Time, hourUTC int, jitterMin int) time.Time {
	jitter := time.Duration(0)
	if jitterMin > 0 {
		jitter = time.Duration(rand.IntN(jitterMin)) * time.Minute
	}

	candidate := time.Date(now.Year(), now.Month(), now.Day(), hourUTC, 0, 0, 0, time.UTC).Add(jitter)
	if !candidate.After(now) {
		// Already past today's scheduled time; schedule for tomorrow.
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

// generateID produces a unique ID for export jobs. It uses a timestamp-based
// approach for simplicity.
func generateID() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}
