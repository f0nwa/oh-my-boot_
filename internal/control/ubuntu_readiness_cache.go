package control

import (
	"context"
	"sync"
	"time"
)

// ubuntuReadinessCache provides non-blocking cached readiness status for Ubuntu
// resources with a fast-path timeout and background singleflight refresh.
type ubuntuReadinessCache struct {
	mu          sync.Mutex
	lastResult  error
	lastChecked time.Time
	checking    bool
	ttl         time.Duration
	fastTimeout time.Duration
}

// newUbuntuReadinessCache initializes the cache with the given TTL and fast timeout.
// If ttl or fastTimeout are non-positive, sensible defaults (30s and 150ms) are used.
func newUbuntuReadinessCache(ttl, fastTimeout time.Duration) *ubuntuReadinessCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if fastTimeout <= 0 {
		fastTimeout = 150 * time.Millisecond
	}
	return &ubuntuReadinessCache{
		ttl:         ttl,
		fastTimeout: fastTimeout,
	}
}

// Check evaluates readiness using the provided check function. If a fresh result
// exists in the cache, it is returned immediately. If a check is already in progress,
// the last known result is returned without starting duplicate checks (singleflight).
// Otherwise, it starts a check: if it finishes within fastTimeout, the fresh result is returned;
// if it takes longer, the last known result is returned immediately while the check continues
// in the background with a 5-second timeout to populate the cache.
func (c *ubuntuReadinessCache) Check(ctx context.Context, check func(context.Context) error) error {
	if c == nil {
		if check == nil {
			return nil
		}
		return check(ctx)
	}
	if check == nil {
		return nil
	}

	c.mu.Lock()
	if !c.lastChecked.IsZero() && time.Since(c.lastChecked) < c.ttl {
		res := c.lastResult
		c.mu.Unlock()
		return res
	}
	if c.checking {
		res := c.lastResult
		c.mu.Unlock()
		return res
	}
	c.checking = true
	lastRes := c.lastResult
	startedAt := time.Now()
	c.mu.Unlock()

	bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)

	go func() {
		defer bgCancel()
		var err error
		defer func() {
			c.mu.Lock()
			if c.lastChecked.Before(startedAt) {
				c.lastResult = err
				c.lastChecked = time.Now()
			}
			c.checking = false
			c.mu.Unlock()
		}()
		err = check(bgCtx)
		done <- err
	}()

	timer := time.NewTimer(c.fastTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		select {
		case err := <-done:
			return err
		default:
			return lastRes
		}
	case <-ctx.Done():
		select {
		case err := <-done:
			return err
		default:
			return lastRes
		}
	}
}

// Update explicitly updates the cached readiness state with the given error and current timestamp.
func (c *ubuntuReadinessCache) Update(err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastResult = err
	c.lastChecked = time.Now()
}
