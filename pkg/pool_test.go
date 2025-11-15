package pool

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubmitAndCollectBasic(t *testing.T) {
	p := NewPool[int](4, WithBufferSize(8))
	defer p.Shutdown()

	var sum int64
	var chans []<-chan int
	for i := 0; i < 10; i++ {
		ch, err := p.Submit(func() int { return i * 2 })
		if err != nil {
			t.Fatal(err)
		}
		chans = append(chans, ch)
	}

	// Wait for jobs and read results directly (no CloseAndCollect).
	for _, ch := range chans {
		v := <-ch
		atomic.AddInt64(&sum, int64(v))
	}

	if got, wantMin := sum, int64(0); got <= wantMin {
		t.Fatalf("unexpected sum %d", got)
	}

	st := p.Stats()
	if st.CompletedJobs != 10 {
		t.Fatalf("completed=%d want=10", st.CompletedJobs)
	}
}

func TestTimeoutRequiresCtx(t *testing.T) {
	p := NewPool[int](2, WithBufferSize(4))
	defer p.Shutdown()

	_, err := p.SubmitWithTimeout(func() int { return 1 }, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for timeout with non-ctx task")
	}
}

func TestSubmitCtxWithTimeout(t *testing.T) {
	p := NewPool[int](2, WithBufferSize(4))
	defer p.Shutdown()

	start := time.Now()
	ch, err := p.SubmitCtxWithTimeout(func(ctx context.Context) int {
		select {
		case <-time.After(50 * time.Millisecond):
			return 42
		case <-ctx.Done():
			return -1
		}
	}, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	v := <-ch
	if v != 42 {
		t.Fatalf("got %d want 42", v)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("returned too quickly")
	}
}

func TestSubmitCtxTimeoutTriggersFailure(t *testing.T) {
	p := NewPool[int](2, WithBufferSize(4))
	defer p.Shutdown()

	ch, err := p.SubmitCtxWithTimeout(func(ctx context.Context) int {
		// ignore ctx until timeout happens
		select {
		case <-time.After(200 * time.Millisecond):
			return 99
		case <-ctx.Done():
			// simulate slow cleanup
			<-time.After(200 * time.Millisecond)
			return -1
		}
	}, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// channel should be closed without a value (receive returns zero, ok=false)
	if v, ok := <-ch; ok {
		t.Fatalf("expected closed channel without value, got %d", v)
	}

	// give worker some time to update counters
	time.Sleep(20 * time.Millisecond)
	if p.Stats().FailedJobs < 1 {
		t.Fatal("expected at least 1 failed job")
	}
}

func TestShutdownCancelsAndCloseStopsQueue(t *testing.T) {
	p := NewPool[int](2, WithBufferSize(1))
	defer p.Shutdown()

	// Long running job
	_, _ = p.SubmitCtxWithTimeout(func(ctx context.Context) int {
		select {
		case <-ctx.Done():
			return -1
		case <-time.After(500 * time.Millisecond):
			return 1
		}
	}, 0)

	p.Shutdown()
	p.Close() // idempotent

	// After shutdown, new submissions should fail
	if _, err := p.Submit(func() int { return 1 }); err == nil {
		t.Fatal("expected error submitting after shutdown")
	}
}

func TestPanicRecovery(t *testing.T) {
	p := NewPool[int](2, WithBufferSize(4))
	defer p.Shutdown()

	ch, err := p.Submit(func() int {
		panic("boom")
	})
	if err != nil {
		t.Fatal(err)
	}
	// channel should be closed without a value
	if _, ok := <-ch; ok {
		t.Fatal("expected closed result channel after panic")
	}

	time.Sleep(10 * time.Millisecond)
	if p.Stats().FailedJobs != 1 {
		t.Fatalf("failed=%d want=1", p.Stats().FailedJobs)
	}

	// Worker should still be alive
	if p.Stats().ActiveWorkers != 1 {
		t.Fatalf("activeWorkers=%d want=1", p.Stats().ActiveWorkers)
	}
}

func TestCloseAndCollect(t *testing.T) {
	p := NewPool[int](2, WithBufferSize(4))
	defer p.Shutdown()

	for i := 1; i <= 5; i++ {
		_, _ = p.Submit(func() int { return 7 })
	}
	out := p.CloseAndCollect()
	if len(out) != 5 {
		t.Fatalf("collected=%d want=5", len(out))
	}
	// Ensure internal slice cleared (memory hygiene); this is a white-box sanity check.
	if got := len(p.results); got != 0 {
		t.Fatalf("results slice not cleared, len=%d", got)
	}
}

func TestStatsQueuedReflectsJobChan(t *testing.T) {
	p := NewPool[int](1, WithBufferSize(2))
	defer p.Shutdown()

	// Block the single worker so queued reflects the buffer len
	block := make(chan struct{})
	_, _ = p.SubmitCtxWithTimeout(func(ctx context.Context) int {
		<-block
		return 1
	}, 0)

	// Enqueue two more; one will sit in queue
	_, _ = p.Submit(func() int { return 2 })
	_, _ = p.Submit(func() int { return 3 })

	st := p.Stats()
	if st.QueuedJobs == 0 {
		t.Fatalf("QueuedJobs=%d want>0", st.QueuedJobs)
	}

	close(block)
	p.Wait()
}

func TestGoroutineDoesNotLeakOnClose(t *testing.T) {
	before := runtime.NumGoroutine()

	p := NewPool[int](8, WithBufferSize(8))
	for i := 0; i < 100; i++ {
		_, _ = p.Submit(func() int { return i })
	}
	p.Close()
	p.Wait()
	p.Shutdown()

	// give scheduler a moment
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	// This is a heuristic check; allow a few extra goroutines due to test runner noise.
	if after > before+10 {
		t.Fatalf("possible leak: goroutines before=%d after=%d", before, after)
	}
}

func TestResizeAddsWorkers(t *testing.T) {
	p := NewPool[int](2, WithBufferSize(4))
	defer p.Shutdown()

	if p.Stats().ActiveWorkers != 1 {
		t.Fatalf("want 1 worker, got %d", p.Stats().ActiveWorkers)
	}
	p.Resize(4)
	time.Sleep(10 * time.Millisecond)
	if p.Stats().ActiveWorkers != 4 {
		t.Fatalf("resize failed: got %d", p.Stats().ActiveWorkers)
	}
}

// Optional: demonstrates returning an error via value type (if you decide to add it later).
var _ = errors.New // silence unused import warning if you add error variants later
