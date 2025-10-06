package pool

import (
	"testing"
	"time"
)

func TestPool_IntTasks(t *testing.T) {
	p := NewPool[int](3)

	for i := 0; i < 5; i++ {
		val := i
		p.Submit(func() int {
			return val * val
		})
	}

	results := p.CloseAndCollect()
	expected := []int{0, 1, 4, 9, 16}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, v := range expected {
		if results[i] != v {
			t.Errorf("result %d: expected %d, got %d", i, v, results[i])
		}
	}
}

func TestPool_StringTasks(t *testing.T) {
	p := NewPool[string](2)

	words := []string{"go", "pool", "test"}
	for _, w := range words {
		word := w
		p.Submit(func() string {
			return word + "_done"
		})
	}

	results := p.CloseAndCollect()
	expected := []string{"go_done", "pool_done", "test_done"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, v := range expected {
		if results[i] != v {
			t.Errorf("result %d: expected %s, got %s", i, v, results[i])
		}
	}
}

func TestPool_ConcurrentSubmit(t *testing.T) {
	p := NewPool[int](4)
	const tasks = 20

	done := make(chan struct{})
	go func() {
		for i := 0; i < tasks; i++ {
			val := i
			p.Submit(func() int {
				time.Sleep(10 * time.Millisecond)
				return val
			})
		}
		close(done)
	}()

	<-done
	results := p.CloseAndCollect()
	if len(results) != tasks {
		t.Fatalf("expected %d results, got %d", tasks, len(results))
	}
}

func TestPool_NoTasks(t *testing.T) {
	p := NewPool[int](2)
	results := p.CloseAndCollect()
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestPool_TaskOrder(t *testing.T) {
	p := NewPool[int](2)
	for i := 0; i < 5; i++ {
		val := i
		p.Submit(func() int { return val })
	}
	results := p.CloseAndCollect()
	for i, v := range results {
		if v != i {
			t.Errorf("expected result %d, got %d", i, v)
		}
	}
}

func TestPool_Wait(t *testing.T) {
	p := NewPool[int](2)
	for i := 0; i < 3; i++ {
		val := i
		p.Submit(func() int { return val * 2 })
	}

	// Wait should block until all tasks are done
	p.Wait()

	// After Wait, all jobs should be completed, but results are not collected
	// Collect results manually from the channels
	results := make([]int, 0, 3)
	for _, resChan := range p.results {
		results = append(results, <-resChan)
	}

	expected := []int{0, 2, 4}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, v := range expected {
		if results[i] != v {
			t.Errorf("result %d: expected %d, got %d", i, v, results[i])
		}
	}
}

func TestPool_Wait_NoTasks(t *testing.T) {
	p := NewPool[int](2)
	p.Wait()
	if len(p.results) != 0 {
		t.Errorf("expected 0 results, got %d", len(p.results))
	}
}

func TestPool_Wait_MultipleCalls(t *testing.T) {
	p := NewPool[int](2)
	for i := 0; i < 2; i++ {
		val := i
		p.Submit(func() int { return val })
	}
	p.Wait()
	// Calling Wait again should not panic or block
	p.Wait()
	for i, resChan := range p.results {
		res := <-resChan
		if res != i {
			t.Errorf("expected %d, got %d", i, res)
		}
	}
}

func TestPool_Close(t *testing.T) {
	p := NewPool[int](2)
	for i := 0; i < 3; i++ {
		val := i
		p.Submit(func() int { return val })
	}

	p.Close()

	// After Close, submitting new tasks should panic
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on Submit after Close, but did not panic")
		}
	}()
	p.Submit(func() int { return 42 })
}

func TestPool_Close_NoTasks(t *testing.T) {
	p := NewPool[int](2)
	p.Close()
	// After Close, submitting new tasks should panic
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on Submit after Close, but did not panic")
		}
	}()
	p.Submit(func() int { return 42 })
}
