package engine

import (
	"testing"
	"time"
)

func TestComputeNextRun_FutureToday(t *testing.T) {
	// Current time is 08:00 UTC, schedule is for 14:00 UTC, no jitter.
	now := time.Date(2025, 6, 15, 8, 0, 0, 0, time.UTC)
	next := computeNextRun(now, 14, 0)

	want := time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("expected %v, got %v", want, next)
	}
}

func TestComputeNextRun_PastToday(t *testing.T) {
	// Current time is 16:00 UTC, schedule was for 10:00 UTC => tomorrow.
	now := time.Date(2025, 6, 15, 16, 0, 0, 0, time.UTC)
	next := computeNextRun(now, 10, 0)

	want := time.Date(2025, 6, 16, 10, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("expected %v, got %v", want, next)
	}
}

func TestComputeNextRun_ExactlyNow(t *testing.T) {
	// Current time equals schedule time exactly => should schedule tomorrow.
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	next := computeNextRun(now, 10, 0)

	want := time.Date(2025, 6, 16, 10, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("expected %v, got %v", want, next)
	}
}

func TestComputeNextRun_WithJitter(t *testing.T) {
	now := time.Date(2025, 6, 15, 8, 0, 0, 0, time.UTC)
	hourUTC := 14
	jitterMin := 30

	// Run multiple times to verify jitter stays in range.
	for range 100 {
		next := computeNextRun(now, hourUTC, jitterMin)
		earliest := time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC)
		latest := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
		// next could be tomorrow if jitter pushes it past the current time
		// but since now is 08:00 and schedule is 14:00, that won't happen.
		if next.Before(earliest) {
			t.Errorf("next %v is before earliest %v", next, earliest)
		}
		if next.After(latest) {
			// If jitter pushes past now it wraps to tomorrow, which is fine
			// but in this case 14:29 < 8:00 won't happen.
			latestTomorrow := latest.Add(24 * time.Hour)
			if next.After(latestTomorrow) {
				t.Errorf("next %v is after latest tomorrow %v", next, latestTomorrow)
			}
		}
	}
}

func TestComputeNextRun_Midnight(t *testing.T) {
	now := time.Date(2025, 6, 15, 23, 30, 0, 0, time.UTC)
	next := computeNextRun(now, 0, 0)

	// 00:00 today is already past, so should be tomorrow at 00:00.
	want := time.Date(2025, 6, 16, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("expected %v, got %v", want, next)
	}
}
