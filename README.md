# GoPool - Generic Worker Pool for Go

A high-performance, generic worker pool implementation in Go with support for context cancellation, timeouts, and comprehensive statistics tracking.

## Features

- **Generic Type Support**: Works with any return type using Go generics
- **Context Awareness**: Full support for context cancellation and timeouts
- **Configurable**: Buffer size, default timeouts, and worker count
- **Statistics**: Real-time monitoring of pool performance
- **Thread Safety**: Atomic operations and proper synchronization
- **Graceful Shutdown**: Both graceful and forceful shutdown options
- **Dynamic Resizing**: Adjust worker count at runtime
- **Timeout Support**: Per-job and global timeout configuration

## Installation

```bash
go get github.com/felipe-rochac/gopool
```

## Quick Start

```go
package main

import (
    "fmt"
    "time"
    
    "github.com/felipe-rochac/gopool/pkg/pool"
)

func main() {
    // Create a pool with 4 workers
    p := pool.NewPool[int](4)
    defer p.Close()
    
    // Submit some tasks
    for i := 0; i < 10; i++ {
        num := i
        p.Submit(func() int {
            return num * num
        })
    }
    
    // Collect all results
    results := p.CloseAndCollect()
    fmt.Printf("Results: %v\n", results)
}
```

## Configuration Options

### Buffer Size

```go
// Create a pool with buffered job queue
p := pool.NewPool[string](4, pool.WithBufferSize(100))
```

### Default Timeout

```go
// Set a default timeout for all jobs
p := pool.NewPool[int](4, pool.WithTimeout(5*time.Second))
```

### Combined Options

```go
p := pool.NewPool[int](4,
    pool.WithBufferSize(50),
    pool.WithTimeout(10*time.Second),
)
```

## Usage Examples

### Basic Task Submission

```go
// Simple task
err := p.Submit(func() int {
    return 42
})

// Task with specific timeout
err := p.SubmitWithTimeout(func() string {
    time.Sleep(2 * time.Second)
    return "completed"
}, 5*time.Second)
```

### Context-Aware Tasks

```go
// Context-aware task
err := p.SubmitCtx(func(ctx context.Context) bool {
    select {
    case <-time.After(1 * time.Second):
        return true
    case <-ctx.Done():
        return false // Cancelled
    }
})

// Context-aware task with timeout
err := p.SubmitCtxWithTimeout(func(ctx context.Context) string {
    // Your context-aware logic here
    return "result"
}, 3*time.Second)
```

### Collecting Results

```go
// Method 1: Close and collect all results
results := p.CloseAndCollect()

// Method 2: Wait for completion, then close
p.Wait()
p.Close()
```

### Pool Statistics

```go
stats := p.Stats()
fmt.Printf("Workers: %d\n", stats.WorkersCount)
fmt.Printf("Queued Jobs: %d\n", stats.QueuedJobs)
fmt.Printf("Completed: %d\n", stats.CompletedJobs)
fmt.Printf("Failed: %d\n", stats.FailedJobs)
fmt.Printf("Active Workers: %d\n", stats.ActiveWorkers)
```

### Dynamic Pool Resizing

```go
// Start with 4 workers
p := pool.NewPool[int](4)

// Scale up to 8 workers
p.Resize(8)

// Check if pool is empty
if p.IsEmpty() {
    fmt.Println("No jobs running or queued")
}
```

### Shutdown Options

```go
// Graceful shutdown - waits for current jobs to complete
p.Close()
p.Wait()

// Forceful shutdown - cancels all running jobs
p.Shutdown()
```

## Advanced Examples

### HTTP Request Pool

```go
import "net/http"

type HTTPResult struct {
    URL    string
    Status int
    Error  error
}

func main() {
    p := pool.NewPool[HTTPResult](10, pool.WithTimeout(30*time.Second))
    
    urls := []string{
        "https://api.github.com",
        "https://httpbin.org/delay/2",
        "https://jsonplaceholder.typicode.com/posts/1",
    }
    
    for _, url := range urls {
        u := url
        p.SubmitCtx(func(ctx context.Context) HTTPResult {
            req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
            resp, err := http.DefaultClient.Do(req)
            if err != nil {
                return HTTPResult{URL: u, Error: err}
            }
            defer resp.Body.Close()
            return HTTPResult{URL: u, Status: resp.StatusCode}
        })
    }
    
    results := p.CloseAndCollect()
    for _, result := range results {
        if result.Error != nil {
            fmt.Printf("Error fetching %s: %v\n", result.URL, result.Error)
        } else {
            fmt.Printf("%s: %d\n", result.URL, result.Status)
        }
    }
}
```

### CPU-Intensive Tasks with Progress Monitoring

```go
import "runtime"

func main() {
    p := pool.NewPool[int](runtime.NumCPU())
    
    // Submit 1000 CPU-intensive tasks
    for i := 0; i < 1000; i++ {
        num := i
        p.Submit(func() int {
            // Simulate CPU work
            result := 0
            for j := 0; j < 1000000; j++ {
                result += j * num
            }
            return result
        })
    }
    
    // Monitor progress
    go func() {
        for {
            stats := p.Stats()
            fmt.Printf("Progress: %d/%d completed\n", 
                stats.CompletedJobs, stats.CompletedJobs+int64(stats.QueuedJobs))
            
            if p.IsEmpty() {
                break
            }
            time.Sleep(1 * time.Second)
        }
    }()
    
    results := p.CloseAndCollect()
    fmt.Printf("All %d tasks completed\n", len(results))
}
```

## API Reference

### Types

#### `Pool[R any]`
The main pool structure managing worker goroutines.

#### `Job[R any]`
Represents a task with its execution function and result channel.

#### `PoolStats`
Contains statistics about the pool's current state.

### Methods

#### Pool Creation
- `NewPool[R any](workers int, opts ...PoolOption) *Pool[R]`

#### Configuration Options
- `WithBufferSize(size int) PoolOption`
- `WithTimeout(timeout time.Duration) PoolOption`

#### Task Submission
- `Submit(task func() R) error`
- `SubmitWithTimeout(task func() R, timeout time.Duration) error`
- `SubmitCtx(task func(context.Context) R) error`
- `SubmitCtxWithTimeout(task func(context.Context) R, timeout time.Duration) error`

#### Pool Management
- `Close()` - Graceful shutdown
- `Shutdown()` - Forceful shutdown with context cancellation
- `Wait()` - Wait for all submitted tasks to complete
- `CloseAndCollect() []R` - Close and collect all results

#### Monitoring
- `Stats() PoolStats` - Get current pool statistics
- `IsEmpty() bool` - Check if pool has no queued or running jobs
- `Resize(newWorkerCount int)` - Dynamically adjust worker count

## Error Handling

The pool returns errors in the following scenarios:
- Submitting tasks to a closed pool
- Pool shutting down during task submission

```go
err := p.Submit(func() int { return 42 })
if err != nil {
    log.Printf("Failed to submit task: %v", err)
}
```

## Performance Considerations

1. **Buffer Size**: Use buffered channels for high-throughput scenarios
2. **Worker Count**: Generally set to `runtime.NumCPU()` for CPU-bound tasks
3. **Timeouts**: Set appropriate timeouts to prevent resource leaks
4. **Context Usage**: Use context-aware tasks for better cancellation support

## Thread Safety

All pool operations are thread-safe and can be called from multiple goroutines concurrently.

## License

MIT License - see LICENSE file for details.