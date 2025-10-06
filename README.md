# gopool

Effortlessly manage concurrent tasks in Go with **gopool**—a streamlined worker pool library.  
**gopool** abstracts the complexity of worker pool management, reducing boilerplate and optimizing resource usage.  
Focus on your core business logic while ensuring scalable, reliable, and efficient task processing in your Go applications.

## Usage Example

```go
package main

import (
    "fmt"
    "github.com/felipe-rochac/gopool"
)

func main() {
    // Create a pool with 5 workers
    pool := gopool.NewPool(5)

    // Submit tasks to the pool
    for i := 0; i < 10; i++ {
        n := i
        pool.Submit(func() {
            fmt.Printf("Processing task %d\n", n)
        })
    }

    // Wait for all tasks to complete
    pool.Wait()
}
```

## Collecting Results from Goroutines

You can retrieve results from goroutines by submitting tasks that send their output to a channel:

```go
package main

import (
    "fmt"
    "github.com/felipe-rochac/gopool"
)

func main() {
    pool := gopool.NewPool(5)
    results := make(chan int, 10)

    for i := 0; i < 10; i++ {
        n := i
        pool.Submit(func() {
            // Simulate some computation
            results <- n * n
        })
    }

    // Close the results channel and collect results after all tasks are done
    pool.CloseAndCollect(results, func(res int) {
        fmt.Printf("Result: %d\n", res)
    })
}
```
```

## Features

- Simple API for submitting tasks
- Automatic worker management
- Graceful shutdown and waiting for completion
