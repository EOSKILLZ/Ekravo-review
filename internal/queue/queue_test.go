package queue

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func testPool() *Pool {
	return New(1, 10, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSingleFlightPerKey(t *testing.T) {
	p := testPool()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	var runs atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	job := func(context.Context) {
		runs.Add(1)
		close(started)
		<-release
	}

	if !p.Submit("repo#1", job) {
		t.Fatal("first submit rejected")
	}
	<-started

	if p.Submit("repo#1", job) {
		t.Fatal("duplicate key should be rejected while running")
	}
	if !p.Submit("repo#2", func(context.Context) {}) {
		t.Fatal("different key should be accepted")
	}

	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for p.Inflight() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("inflight not cleared: %d", p.Inflight())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1", runs.Load())
	}
	p.Shutdown(context.Background())
}

func TestQueueFullIsRejectedWithoutBlocking(t *testing.T) {
	p := New(1, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Not started: one job fills the buffer, the next is rejected.
	block := make(chan struct{})
	defer close(block)

	if !p.Submit("a", func(context.Context) {}) {
		t.Fatal("first submit rejected")
	}
	if p.Submit("b", func(context.Context) {}) {
		t.Fatal("full queue should reject")
	}
}

func TestShutdownRunsQueuedJobs(t *testing.T) {
	p := testPool()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)

	var ran atomic.Int32
	for _, key := range []string{"a", "b", "c"} {
		if !p.Submit(key, func(context.Context) { ran.Add(1) }) {
			t.Fatalf("submit %s rejected", key)
		}
	}
	p.Shutdown(context.Background())

	if ran.Load() != 3 {
		t.Fatalf("ran = %d, want 3", ran.Load())
	}
}
