package control

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUbuntuReadinessCacheFastPath(t *testing.T) {
	var calls atomic.Int32
	expectedErr := errors.New("fast failure")
	check := func(ctx context.Context) error {
		calls.Add(1)
		time.Sleep(5 * time.Millisecond)
		return expectedErr
	}

	cache := newUbuntuReadinessCache(500*time.Millisecond, 150*time.Millisecond)

	start := time.Now()
	err := cache.Check(context.Background(), check)
	elapsed := time.Since(start)

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("fast path took too long: %v", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}

	// Second call within TTL should return cached result without calling check again.
	err2 := cache.Check(context.Background(), check)
	if !errors.Is(err2, expectedErr) {
		t.Fatalf("expected cached error %v, got %v", expectedErr, err2)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected calls to remain 1 within TTL, got %d", got)
	}
}

func TestUbuntuReadinessCacheSlowPathDoesNotBlock(t *testing.T) {
	checkStarted := make(chan struct{})
	checkFinished := make(chan struct{})
	expectedErr := errors.New("slow boot error")

	slowCheck := func(ctx context.Context) error {
		close(checkStarted)
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
		close(checkFinished)
		return expectedErr
	}

	// Fast timeout 50ms, slow check takes 300ms.
	cache := newUbuntuReadinessCache(1*time.Second, 50*time.Millisecond)

	start := time.Now()
	err := cache.Check(context.Background(), slowCheck)
	elapsed := time.Since(start)

	// First cold call should return nil (optimistic / cold) within fastTimeout without blocking for 300ms.
	if err != nil {
		t.Fatalf("expected nil on cold slow path, got %v", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("slow path blocked for too long: %v", elapsed)
	}

	// Verify the background check started.
	select {
	case <-checkStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("background check was not started")
	}

	// Wait for background check to finish.
	select {
	case <-checkFinished:
	case <-time.After(1 * time.Second):
		t.Fatal("background check timed out")
	}

	// Small pause to allow mutex update in background goroutine.
	time.Sleep(20 * time.Millisecond)

	// Subsequent check should return the settled result from cache immediately.
	startCached := time.Now()
	cachedErr := cache.Check(context.Background(), func(ctx context.Context) error {
		t.Fatal("check function should not be invoked after cache has settled")
		return nil
	})
	if !errors.Is(cachedErr, expectedErr) {
		t.Fatalf("expected settled error %v, got %v", expectedErr, cachedErr)
	}
	if time.Since(startCached) > 20*time.Millisecond {
		t.Fatalf("cached read took too long: %v", time.Since(startCached))
	}
}

func TestUbuntuReadinessCacheConcurrentSingleflight(t *testing.T) {
	var calls atomic.Int32
	unblockCheck := make(chan struct{})
	checkStarted := make(chan struct{})
	var startOnce sync.Once

	check := func(ctx context.Context) error {
		calls.Add(1)
		startOnce.Do(func() { close(checkStarted) })
		<-unblockCheck
		return nil
	}

	cache := newUbuntuReadinessCache(1*time.Second, 50*time.Millisecond)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = cache.Check(context.Background(), check)
		}()
	}

	// Wait until the singleflight check starts.
	select {
	case <-checkStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("check did not start in time")
	}

	// Ensure other goroutines had a chance to enter cache.Check while checking is true.
	time.Sleep(100 * time.Millisecond)
	close(unblockCheck)

	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 call from concurrent requests, got %d", got)
	}
}

func TestUbuntuReadinessCacheUpdate(t *testing.T) {
	cache := newUbuntuReadinessCache(1*time.Minute, 50*time.Millisecond)

	neverCall := func(ctx context.Context) error {
		t.Fatal("check function must not be called when cache is explicitly updated")
		return nil
	}

	errSample := errors.New("sample error")
	cache.Update(errSample)

	if err := cache.Check(context.Background(), neverCall); !errors.Is(err, errSample) {
		t.Fatalf("expected %v, got %v", errSample, err)
	}

	// Update with nil (ready)
	cache.Update(nil)
	if err := cache.Check(context.Background(), neverCall); err != nil {
		t.Fatalf("expected nil after ready update, got %v", err)
	}

	// Update with another error
	errAnother := errors.New("another error")
	cache.Update(errAnother)
	if err := cache.Check(context.Background(), neverCall); !errors.Is(err, errAnother) {
		t.Fatalf("expected %v, got %v", errAnother, err)
	}
}

func TestUbuntuReadinessCacheNilSafe(t *testing.T) {
	var c *ubuntuReadinessCache
	c.Update(errors.New("ignored"))

	called := false
	err := c.Check(context.Background(), func(ctx context.Context) error {
		called = true
		return errors.New("fallback")
	})
	if !called || err == nil || err.Error() != "fallback" {
		t.Fatalf("nil cache should pass through to check func, got called=%v err=%v", called, err)
	}
}

func TestUbuntuReadinessCacheTTLExpiration(t *testing.T) {
	var calls atomic.Int32
	check := func(ctx context.Context) error {
		calls.Add(1)
		return nil
	}

	// 50ms TTL
	cache := newUbuntuReadinessCache(50*time.Millisecond, 20*time.Millisecond)

	if err := cache.Check(context.Background(), check); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}

	// Wait for TTL to expire
	time.Sleep(70 * time.Millisecond)

	if err := cache.Check(context.Background(), check); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls after TTL expiration, got %d", got)
	}
}
