package main

import (
	"fmt"
	pool "gopool/pkg"
	"time"
)

func main() {
	// 1. Initialize
	p := pool.NewPool[string](3)

	// 2. Submit tasks (Submit returns nothing, no channels to manage)
	p.Submit(func() string { return "Result 1" })
	p.Submit(func() string { time.Sleep(100 * time.Millisecond); return "Result 2" })
	p.Submit(func() string { return "Result 3" })

	// 3. Collect all results in one blocking call
	finalResults := p.CloseAndCollect()

	fmt.Println("All results collected:")
	for _, result := range finalResults {
		fmt.Println(result)
	}
	// Output:
	// All results collected:
	// Result 1
	// Result 2
	// Result 3
}
