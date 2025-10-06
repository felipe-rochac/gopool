package pool

import (
	"sync"
)

// Job is the wrapper for a task, carrying its return channel
type Job[R any] struct {
	Do     func() R
	Result chan R // Channel to send the result back on
}

// Pool manages the generic worker goroutines
type Pool[R any] struct {
	jobs    chan Job[R]
	results []chan R // New: Stores the result channels internally
	wg      sync.WaitGroup
}

// NewPool initializes and starts the specified number of generic workers
func NewPool[R any](workers int) *Pool[R] {
	p := &Pool[R]{
		// Buffered job channel for the queue
		jobs: make(chan Job[R]),
	}

	// Launch worker goroutines
	for range workers {
		go p.worker()
	}
	return p
}

// worker function processes jobs from the channel
func (p *Pool[R]) worker() {
	for job := range p.jobs {
		// Execute the function and capture the generic result
		result := job.Do()
		job.Result <- result // Send the result back on the dedicated channel
		p.wg.Done()
	}
}

// Submit is now simple and returns nothing.
func (p *Pool[R]) Submit(task func() R) {
	resultChan := make(chan R, 1)

	p.wg.Add(1)
	p.jobs <- Job[R]{Do: task, Result: resultChan}

	// Store the result channel reference internally
	p.results = append(p.results, resultChan)
}

// CloseAndCollect safely stops the pool, waits for all workers,
// and returns all collected results in order.
func (p *Pool[R]) CloseAndCollect() []R {
	// 1. Close the job channel to signal workers to stop reading
	close(p.jobs)

	// 2. Wait for all submitted tasks to complete
	p.wg.Wait()

	// 3. Collect and return all results
	collectedResults := make([]R, 0, len(p.results))

	for _, resChan := range p.results {
		// This is safe because wg.Wait() ensures all tasks are done
		collectedResults = append(collectedResults, <-resChan)
	}

	return collectedResults
}

// Wait allows external code to wait for all submitted tasks to complete
func (p *Pool[R]) Wait() {
	p.wg.Wait()
}

// Close stops accepting new jobs and closes the job channel
func (p *Pool[R]) Close() {
	close(p.jobs)
}
