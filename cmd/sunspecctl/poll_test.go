// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPollLoopSingleIteration(t *testing.T) {
	var iterations []int
	err := pollLoop(context.Background(), time.Millisecond, 1, func(ctx context.Context, i int) error {
		iterations = append(iterations, i)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(iterations) != 1 || iterations[0] != 1 {
		t.Fatalf("expected [1], got %v", iterations)
	}
}

func TestPollLoopMultipleIterations(t *testing.T) {
	var iterations []int
	err := pollLoop(context.Background(), time.Millisecond, 3, func(ctx context.Context, i int) error {
		iterations = append(iterations, i)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(iterations) != 3 {
		t.Fatalf("expected 3 iterations, got %d", len(iterations))
	}
	for idx, want := range []int{1, 2, 3} {
		if iterations[idx] != want {
			t.Fatalf("iteration %d: got %d, want %d", idx, iterations[idx], want)
		}
	}
}

func TestPollLoopStopsOnError(t *testing.T) {
	errBoom := errors.New("boom")
	var iterations []int
	err := pollLoop(context.Background(), time.Millisecond, 5, func(ctx context.Context, i int) error {
		iterations = append(iterations, i)
		if i == 2 {
			return errBoom
		}
		return nil
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got %v", err)
	}
	if len(iterations) != 2 {
		t.Fatalf("expected 2 iterations before error, got %d", len(iterations))
	}
}

func TestPollLoopRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var iterations []int
	err := pollLoop(ctx, 100*time.Millisecond, 0, func(ctx context.Context, i int) error {
		iterations = append(iterations, i)
		if i == 2 {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(iterations) < 2 || len(iterations) > 3 {
		t.Fatalf("expected 2-3 iterations, got %d", len(iterations))
	}
}

func TestPollLoopZeroCountRunsUntilCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var count int
	err := pollLoop(ctx, 10*time.Millisecond, 0, func(ctx context.Context, i int) error {
		count = i
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count < 2 {
		t.Fatalf("expected at least 2 iterations with infinite loop, got %d", count)
	}
}

func TestPollLoopIterationGetsTimeout(t *testing.T) {
	flagTimeout = 50 * time.Millisecond
	defer func() { flagTimeout = 10 * time.Second }()

	var deadlines []time.Time
	err := pollLoop(context.Background(), time.Millisecond, 2, func(ctx context.Context, i int) error {
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected context deadline")
		}
		deadlines = append(deadlines, dl)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deadlines) != 2 {
		t.Fatalf("expected 2 deadlines, got %d", len(deadlines))
	}
	if !deadlines[1].After(deadlines[0]) {
		t.Fatal("expected each iteration to get its own timeout")
	}
}

// An error caused by the cancellation itself is not reported: Ctrl-C during a
// read ends the loop quietly.
func TestPollLoopSwallowsErrorAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	err := pollLoop(ctx, time.Millisecond, 5, func(iterCtx context.Context, i int) error {
		calls++
		cancel()
		<-iterCtx.Done() // the per-iteration context is derived from ctx
		return iterCtx.Err()
	})
	if err != nil {
		t.Fatalf("err = %v, want nil when the failure is due to cancellation", err)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1", calls)
	}
}

// An iteration that outlives --timeout fails the loop with the deadline error.
func TestPollLoopIterationTimeoutIsAnError(t *testing.T) {
	flagTimeout = 20 * time.Millisecond
	defer func() { flagTimeout = 10 * time.Second }()

	err := pollLoop(context.Background(), time.Millisecond, 3, func(ctx context.Context, i int) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
