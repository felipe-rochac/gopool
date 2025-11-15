package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Job is the wrapper for a task, carrying its return channel
type Job[R any] struct {
	Do      func() R
	DoCtx   func(context.Context) R // Context-aware task
	Result  chan R
	Timeout time.Duration
}

// PoolStats provides statistics about the pool
type PoolStats struct {
	WorkersCount  int
	QueuedJobs    int
	CompletedJobs int64
	FailedJobs    int64
	ActiveWorkers int64
}

// Pool manages the generic worker goroutines
type Pool[R any] struct {
	jobs          chan Job[R]
	results       []chan R
	wg            sync.WaitGroup
	workers       int
	closed        int32
	ctx           context.Context
	cancel        context.CancelFunc
	completedJobs int64
	failedJobs    int64
	activeWorkers int64
	mu            sync.RWMutex
}

// PoolOption allows configuration of the pool
type PoolOption func(*poolConfig)

type poolConfig struct {
	bufferSize int
	timeout    time.Duration
}

// WithBufferSize sets the job queue buffer size
func WithBufferSize(size int) PoolOption {
	return func(c *poolConfig) {
		c.bufferSize = size
	}
}

// WithTimeout sets a default timeout for jobs
func WithTimeout(timeout time.Duration) PoolOption {
	return func(c *poolConfig) {
		c.timeout = timeout
	}
}

// NewPool initializes and starts the specified number of generic workers
func NewPool[R any](workers int, opts ...PoolOption) *Pool[R] {
	config := &poolConfig{
		bufferSize: 0, // Unbuffered by default
		timeout:    0, // No timeout by default
	}

	for _, opt := range opts {
		opt(config)
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pool[R]{
		jobs:    make(chan Job[R], config.bufferSize),
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
	}

	// Launch worker goroutines
	for range workers {
		go p.worker()
	}
	return p
}

// worker function processes jobs from the channel
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

// processJob handles individual job execution with timeout support
func (p *Pool[R]) processJob(job Job[R]) {
	defer p.wg.Done()

	var result R
	var success bool

	// Create job context with timeout if specified
	jobCtx := p.ctx
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		jobCtx, cancel = context.WithTimeout(p.ctx, job.Timeout)
		defer cancel()
	}

	// Execute job with timeout handling
	done := make(chan struct{})
	go func() {
		defer close(done)
		if job.DoCtx != nil {
			result = job.DoCtx(jobCtx)
		} else {
			result = job.Do()
		}
		success = true
	}()

	select {
	case <-done:
		if success {
			atomic.AddInt64(&p.completedJobs, 1)
			job.Result <- result
		}
	case <-jobCtx.Done():
		atomic.AddInt64(&p.failedJobs, 1)
		// Don't send to result channel on timeout
	}
}

// Submit adds a task to the pool
func (p *Pool[R]) Submit(task func() R) error {
	return p.SubmitWithTimeout(task, 0)
}

// SubmitCtx adds a context-aware task to the pool
func (p *Pool[R]) SubmitCtx(task func(context.Context) R) error {
	return p.SubmitCtxWithTimeout(task, 0)
}

// SubmitWithTimeout adds a task with a specific timeout
func (p *Pool[R]) SubmitWithTimeout(task func() R, timeout time.Duration) error {
	if atomic.LoadInt32(&p.closed) == 1 {
		return errors.New("pool is closed")
	}

	resultChan := make(chan R, 1)
	job := Job[R]{
		Do:      task,
		Result:  resultChan,
		Timeout: timeout,
	}

	p.mu.Lock()
	p.results = append(p.results, resultChan)
	p.mu.Unlock()

	p.wg.Add(1)

	select {
	case p.jobs <- job:
		return nil
	case <-p.ctx.Done():
		p.wg.Done()
		return errors.New("pool is shutting down")
	}
}

// SubmitCtxWithTimeout adds a context-aware task with timeout
func (p *Pool[R]) SubmitCtxWithTimeout(task func(context.Context) R, timeout time.Duration) error {
	if atomic.LoadInt32(&p.closed) == 1 {
		return errors.New("pool is closed")
	}

	resultChan := make(chan R, 1)
	job := Job[R]{
		DoCtx:   task,
		Result:  resultChan,
		Timeout: timeout,
	}

	p.mu.Lock()
	p.results = append(p.results, resultChan)
	p.mu.Unlock()

	p.wg.Add(1)

	select {
	case p.jobs <- job:
		return nil
	case <-p.ctx.Done():
		p.wg.Done()
		return errors.New("pool is shutting down")
	}
}

// CloseAndCollect safely stops the pool and returns all results
func (p *Pool[R]) CloseAndCollect() []R {
	p.Close()
	p.Wait()

	p.mu.RLock()
	collectedResults := make([]R, 0, len(p.results))

	for _, resChan := range p.results {
		select {
		case result := <-resChan:
			collectedResults = append(collectedResults, result)
		default:
			// Skip channels that don't have results (failed/timeout jobs)
		}
	}
	p.mu.RUnlock()

	return collectedResults
}

// Wait waits for all submitted tasks to complete
func (p *Pool[R]) Wait() {
	p.wg.Wait()
}

// Close gracefully shuts down the pool
func (p *Pool[R]) Close() {
	if atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		close(p.jobs)
	}
}

// Shutdown forcefully shuts down the pool with context cancellation
func (p *Pool[R]) Shutdown() {
	p.cancel()
	p.Close()
}

// Stats returns current pool statistics
func (p *Pool[R]) Stats() PoolStats {
	p.mu.RLock()
	queuedJobs := len(p.results)
	p.mu.RUnlock()

	return PoolStats{
		WorkersCount:  p.workers,
		QueuedJobs:    queuedJobs,
		CompletedJobs: atomic.LoadInt64(&p.completedJobs),
		FailedJobs:    atomic.LoadInt64(&p.failedJobs),
		ActiveWorkers: atomic.LoadInt64(&p.activeWorkers),
	}
}

// Resize changes the number of workers (experimental)
func (p *Pool[R]) Resize(newWorkerCount int) {
	if newWorkerCount <= 0 {
		return
	}

	currentWorkers := p.workers
	diff := newWorkerCount - currentWorkers

	if diff > 0 {
		// Add workers
		for i := 0; i < diff; i++ {
			go p.worker()
		}
	}
	// Note: Reducing workers is more complex and requires additional signaling

	p.workers = newWorkerCount
}

// IsEmpty returns true if no jobs are queued or being processed
func (p *Pool[R]) IsEmpty() bool {
	p.mu.RLock()
	queued := len(p.results)
	p.mu.RUnlock()

	return queued == 0 && atomic.LoadInt64(&p.activeWorkers) == 0
}
