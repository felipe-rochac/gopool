package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Job is a unit of work executed by the pool.
// Either DoCtx or Do must be provided. If Timeout > 0, DoCtx is required.
type Job[R any] struct {
	Do      func() R                // non-context task (no timeout enforcement)
	DoCtx   func(context.Context) R // context-aware task (supports timeout/cancel)
	Result  chan R                  // caller receives exactly one value OR channel is closed with no value on failure/timeout
	Timeout time.Duration           // per-job timeout (requires DoCtx)
}

// PoolStats provides runtime statistics about the pool.
type PoolStats struct {
	WorkersCount  int
	QueuedJobs    int // length of the job queue
	CompletedJobs int64
	FailedJobs    int64
	ActiveWorkers int64 // currently running worker goroutines
}

// Pool manages worker goroutines processing submitted jobs.
type Pool[R any] struct {
	jobs           chan Job[R]
	results        []chan R // kept only for CloseAndCollect
	wg             sync.WaitGroup
	workers        int
	closed         int32
	ctx            context.Context
	cancel         context.CancelFunc
	defaultTimeout time.Duration

	completedJobs int64
	failedJobs    int64
	activeWorkers int64
	activeJobs    int64

	mu sync.RWMutex
}

// PoolOption customizes the pool at construction.
type PoolOption func(*poolConfig)

type poolConfig struct {
	bufferSize int
	timeout    time.Duration
}

// WithBufferSize sets the job queue buffer size.
func WithBufferSize(size int) PoolOption {
	return func(c *poolConfig) { c.bufferSize = size }
}

// WithTimeout sets a default timeout for jobs (applies to SubmitCtx* when no explicit timeout provided).
func WithTimeout(timeout time.Duration) PoolOption {
	return func(c *poolConfig) { c.timeout = timeout }
}

// NewPool starts a pool with N workers.
func NewPool[R any](workers int, opts ...PoolOption) *Pool[R] {
	if workers <= 0 {
		workers = 1
	}

	cfg := &poolConfig{
		bufferSize: 0,
		timeout:    0,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool[R]{
		jobs:           make(chan Job[R], cfg.bufferSize),
		workers:        workers,
		ctx:            ctx,
		cancel:         cancel,
		defaultTimeout: cfg.timeout,
		results:        make([]chan R, 0, 64),
	}

	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p
}

func (p *Pool[R]) worker() {
	atomic.AddInt64(&p.activeWorkers, 1)
	defer atomic.AddInt64(&p.activeWorkers, -1)

	for {
		select {
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			p.processJob(job)
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Pool[R]) processJob(job Job[R]) {
	atomic.AddInt64(&p.activeJobs, 1)
	defer atomic.AddInt64(&p.activeJobs, -1)
	defer p.wg.Done()

	// Panic safety: one bad job should not kill the worker.
	defer func() {
		if r := recover(); r != nil {
			atomic.AddInt64(&p.failedJobs, 1)
			// signal completion to consumer even on failure
			close(job.Result)
		}
	}()

	// Derive job context with timeout if configured.
	jobCtx := p.ctx
	if job.Timeout <= 0 && p.defaultTimeout > 0 {
		job.Timeout = p.defaultTimeout
	}
	if job.Timeout > 0 {
		if job.Do != nil && job.DoCtx == nil {
			// timeouts require cooperation; fail fast for non-ctx job with timeout requested
			atomic.AddInt64(&p.failedJobs, 1)
			close(job.Result)
			return
		}
		var cancel context.CancelFunc
		jobCtx, cancel = context.WithTimeout(p.ctx, job.Timeout)
		defer cancel()
	}

	var (
		result R
	)

	switch {
	case job.DoCtx != nil:
		result = job.DoCtx(jobCtx)
	case job.Do != nil:
		// No timeout enforcement here; Do ignores ctx.
		result = job.Do()
	default:
		atomic.AddInt64(&p.failedJobs, 1)
		close(job.Result)
		return
	}

	// If context timed out or was canceled while running, mark as failed.
	select {
	case <-jobCtx.Done():
		atomic.AddInt64(&p.failedJobs, 1)
		close(job.Result)
		return
	default:
	}

	// Deliver result and close the channel so callers can range/receive deterministically.
	atomic.AddInt64(&p.completedJobs, 1)
	job.Result <- result
	close(job.Result)
}

// Submit enqueues a non-context job (no timeout). Returns the job's result channel.
func (p *Pool[R]) Submit(task func() R) (<-chan R, error) {
	return p.SubmitWithTimeout(task, 0)
}

// SubmitCtx enqueues a context-aware job (cooperates with cancel/timeout). Returns the job's result channel.
func (p *Pool[R]) SubmitCtx(task func(context.Context) R) (<-chan R, error) {
	return p.SubmitCtxWithTimeout(task, 0)
}

// SubmitWithTimeout enqueues a non-context job with a requested timeout (not supported).
// If timeout > 0 and the task is not context-aware, the job is rejected.
func (p *Pool[R]) SubmitWithTimeout(task func() R, timeout time.Duration) (<-chan R, error) {
	if atomic.LoadInt32(&p.closed) == 1 {
		return nil, errors.New("pool is closed")
	}
	if timeout > 0 {
		return nil, errors.New("timeout requires SubmitCtx/DoCtx task")
	}

	resultChan := make(chan R, 1)
	job := Job[R]{Do: task, Result: resultChan}

	p.mu.Lock()
	p.results = append(p.results, resultChan)
	p.mu.Unlock()

	p.wg.Add(1)
	select {
	case p.jobs <- job:
		return resultChan, nil
	case <-p.ctx.Done():
		p.wg.Done()
		return nil, errors.New("pool is shutting down")
	}
}

// SubmitCtxWithTimeout enqueues a context-aware job with an optional timeout. Returns the job's result channel.
func (p *Pool[R]) SubmitCtxWithTimeout(task func(context.Context) R, timeout time.Duration) (<-chan R, error) {
	if atomic.LoadInt32(&p.closed) == 1 {
		return nil, errors.New("pool is closed")
	}
	if timeout == 0 {
		timeout = p.defaultTimeout
	}

	resultChan := make(chan R, 1)
	job := Job[R]{DoCtx: task, Result: resultChan, Timeout: timeout}

	p.mu.Lock()
	p.results = append(p.results, resultChan)
	p.mu.Unlock()

	p.wg.Add(1)
	select {
	case p.jobs <- job:
		return resultChan, nil
	case <-p.ctx.Done():
		p.wg.Done()
		return nil, errors.New("pool is shutting down")
	}
}

// Close stops accepting new jobs and closes the queue. Existing jobs continue.
func (p *Pool[R]) Close() {
	if atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		close(p.jobs)
	}
}

// Shutdown cancels all workers and also closes the queue.
func (p *Pool[R]) Shutdown() {
	p.cancel()
	p.Close()
}

// Wait blocks until all submitted jobs have finished.
func (p *Pool[R]) Wait() { p.wg.Wait() }

// CloseAndCollect waits for completion, then collects available results once and clears the internal slice.
// Results of timed-out/failed jobs won't be present (their channels are closed without a value).
func (p *Pool[R]) CloseAndCollect() []R {
	p.Close()
	p.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	collected := make([]R, 0, len(p.results))
	for _, ch := range p.results {
		if ch == nil {
			continue
		}
		// since each result chan is closed by the worker, a single receive is enough:
		if v, ok := <-ch; ok {
			collected = append(collected, v)
		}
	}
	p.results = nil // avoid unbounded growth across runs
	return collected
}

// Stats returns current statistics.
func (p *Pool[R]) Stats() PoolStats {
	return PoolStats{
		WorkersCount:  p.workers,
		QueuedJobs:    len(p.jobs),
		CompletedJobs: atomic.LoadInt64(&p.completedJobs),
		FailedJobs:    atomic.LoadInt64(&p.failedJobs),
		ActiveWorkers: atomic.LoadInt64(&p.activeWorkers),
	}
}

// Resize adds more workers (downscale is not implemented).
func (p *Pool[R]) Resize(newWorkerCount int) {
	if newWorkerCount <= p.workers {
		p.workers = newWorkerCount
		return
	}
	diff := newWorkerCount - p.workers
	for i := 0; i < diff; i++ {
		go p.worker()
	}
	p.workers = newWorkerCount
}

// IsEmpty reports whether no jobs are queued and no jobs are being processed.
// (Workers can still be alive; use ActiveWorkers for that.)
func (p *Pool[R]) IsEmpty() bool {
	return len(p.jobs) == 0 && atomic.LoadInt64(&p.activeJobs) == 0
}
