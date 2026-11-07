// Package queue provides a bounded worker pool that runs at most one job per
// key at a time, so a burst of webhooks for one PR cannot review it twice.
package queue

import (
	"context"
	"log/slog"
	"sync"
)

// Job is a unit of work.
type Job func(ctx context.Context)

type item struct {
	key string
	job Job
}

// Pool is a fixed-size worker pool.
type Pool struct {
	log      *slog.Logger
	ch       chan item
	workers  int
	mu       sync.Mutex
	inflight map[string]struct{}
	closed   bool
	wg       sync.WaitGroup
}

// New creates a pool. Start must be called before Submit has any effect.
func New(workers, capacity int, log *slog.Logger) *Pool {
	if workers < 1 {
		workers = 1
	}
	if capacity < 1 {
		capacity = 1
	}
	if log == nil {
		log = slog.Default()
	}
	return &Pool{
		log:      log,
		ch:       make(chan item, capacity),
		workers:  workers,
		inflight: map[string]struct{}{},
	}
}

// Start launches the workers. They stop when ctx is cancelled.
func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for it := range p.ch {
				p.run(ctx, it)
			}
		}()
	}
}

func (p *Pool) run(ctx context.Context, it item) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("job panicked", "key", it.key, "panic", r)
		}
		p.mu.Lock()
		delete(p.inflight, it.key)
		p.mu.Unlock()
	}()
	it.job(ctx)
}

// Submit enqueues a job unless the key is already queued/running or the queue
// is full. It never blocks.
func (p *Pool) Submit(key string, job Job) bool {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return false
	}
	if _, busy := p.inflight[key]; busy {
		p.mu.Unlock()
		return false
	}
	p.inflight[key] = struct{}{}
	p.mu.Unlock()

	select {
	case p.ch <- item{key: key, job: job}:
		return true
	default:
		p.mu.Lock()
		delete(p.inflight, key)
		p.mu.Unlock()
		p.log.Warn("queue full, rejecting job", "key", key)
		return false
	}
}

// Inflight reports how many keys are queued or running.
func (p *Pool) Inflight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inflight)
}

// Shutdown stops accepting jobs and waits for workers to drain.
func (p *Pool) Shutdown(ctx context.Context) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.ch)
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
