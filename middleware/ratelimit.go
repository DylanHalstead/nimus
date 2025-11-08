package middleware

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DylanHalstead/nimbus"
)

// RateLimiter implements a lock-free token bucket rate limiter using atomic operations.
// Uses sync.Map for lock-free concurrent access and atomic.Int64 for token counts.
// This design eliminates mutex contention and scales linearly with concurrent requests.
type RateLimiter struct {
	buckets   sync.Map      // key (string) -> *bucket (lock-free map)
	rate      int           // tokens per second
	capacity  int           // maximum burst size
	cleanup   time.Duration // how often to remove stale buckets
	done      chan struct{} // signal to stop cleanup goroutine
	closeOnce sync.Once     // ensures Close() is called only once
}

// bucket represents a lock-free token bucket using atomic operations.
// All fields are accessed atomically to avoid lock contention.
type bucket struct {
	tokens   atomic.Int64 // current token count (atomic for lock-free updates)
	lastSeen atomic.Int64 // last access time in Unix nanoseconds (atomic for lock-free updates)
}

// NewRateLimiter creates a new lock-free rate limiter using atomic operations.
// 
// Parameters:
//   - rate: tokens added per second (e.g., 10 = 10 requests per second)
//   - capacity: maximum burst size (e.g., 20 = allow bursts of 20 requests)
//
// The rate limiter uses sync.Map for lock-free concurrent access and atomic operations
// for token updates, providing excellent performance under high concurrency.
func NewRateLimiter(rate, capacity int) *RateLimiter {
	rl := &RateLimiter{
		buckets:  sync.Map{}, // lock-free map
		rate:     rate,
		capacity: capacity,
		cleanup:  time.Minute * 5,
		done:     make(chan struct{}),
	}

	// Start cleanup goroutine (runs lock-free)
	go rl.cleanupLoop()

	return rl
}

// Close stops the cleanup goroutine and releases resources
// Can be called multiple times safely (only closes once)
// Should be called when the rate limiter is no longer needed
func (rl *RateLimiter) Close() {
	rl.closeOnce.Do(func() {
		close(rl.done)
		unregisterLimiter(rl)
	})
}

// cleanupLoop periodically removes stale buckets to prevent memory leaks.
// Runs lock-free by iterating over sync.Map and deleting expired entries.
// Stops when Close() is called on the RateLimiter.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Lock-free cleanup: iterate and delete stale entries
			now := time.Now().UnixNano()
			cleanupThreshold := now - int64(rl.cleanup)

			// Range over sync.Map (lock-free iteration)
			rl.buckets.Range(func(key, value any) bool {
				b := value.(*bucket)
				lastSeen := b.lastSeen.Load()

				// Delete buckets that haven't been accessed recently
				if lastSeen < cleanupThreshold {
					rl.buckets.Delete(key)
				}

				return true // continue iteration
			})

		case <-rl.done:
			// Stop cleanup loop
			return
		}
	}
}

// allow checks if a request should be allowed using lock-free atomic operations.
// Implements the token bucket algorithm with compare-and-swap (CAS) for thread safety.
// 
// Algorithm:
// 1. Load or create bucket atomically
// 2. Calculate token refill based on time elapsed
// 3. Try to consume a token with atomic CAS
// 4. If CAS fails (race condition), retry
//
// This approach provides true lock-free performance with no contention.
func (rl *RateLimiter) allow(key string) bool {
	now := time.Now().UnixNano()

	// Load or create bucket atomically (lock-free)
	value, loaded := rl.buckets.LoadOrStore(key, &bucket{})
	b := value.(*bucket)

	// If this is a new bucket, initialize it
	if !loaded {
		b.tokens.Store(int64(rl.capacity - 1))
		b.lastSeen.Store(now)
		return true // first request always allowed
	}

	// Token bucket algorithm with atomic compare-and-swap (CAS)
	// Loop until we successfully update or determine we're rate limited
	for {
		// Load current state atomically
		currentTokens := b.tokens.Load()
		lastSeen := b.lastSeen.Load()

		// Calculate elapsed time and token refill
		elapsedNanos := now - lastSeen
		elapsedSeconds := float64(elapsedNanos) / float64(time.Second)
		refill := int64(elapsedSeconds * float64(rl.rate))

		// Calculate new token count (capped at capacity)
		newTokens := currentTokens + refill
		if newTokens > int64(rl.capacity) {
			newTokens = int64(rl.capacity)
		}

		// Check if we have tokens available
		if newTokens <= 0 {
			// Rate limited - no tokens available
			// Try to update lastSeen to prevent stale timestamp
			b.lastSeen.CompareAndSwap(lastSeen, now)
			return false
		}

		// Try to consume a token atomically (CAS loop)
		if b.tokens.CompareAndSwap(currentTokens, newTokens-1) {
			// Successfully consumed a token
			// Update lastSeen timestamp (best effort, not critical if it fails)
			b.lastSeen.CompareAndSwap(lastSeen, now)
			return true
		}

		// CAS failed due to race condition, retry
		// Another goroutine modified tokens between Load and CompareAndSwap
		// The loop will retry with the new value
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// rateLimiterRegistry keeps track of all created rate limiters for cleanup
var (
	rateLimiterRegistry = make(map[*RateLimiter]bool)
	registryMu          sync.Mutex
)

// registerLimiter adds a rate limiter to the registry
func registerLimiter(rl *RateLimiter) {
	registryMu.Lock()
	rateLimiterRegistry[rl] = true
	registryMu.Unlock()
}

// unregisterLimiter removes a rate limiter from the registry
func unregisterLimiter(rl *RateLimiter) {
	registryMu.Lock()
	delete(rateLimiterRegistry, rl)
	registryMu.Unlock()
}

// ShutdownAllRateLimiters stops all rate limiter cleanup goroutines
// Call this when shutting down your application
func ShutdownAllRateLimiters() {
	registryMu.Lock()
	limiters := make([]*RateLimiter, 0, len(rateLimiterRegistry))
	for rl := range rateLimiterRegistry {
		limiters = append(limiters, rl)
	}
	registryMu.Unlock()

	for _, rl := range limiters {
		rl.Close()
	}
}

// RateLimitWithRouter returns a rate limiting middleware and registers cleanup with the router.
// Limits requests per IP address.
// The rate limiter's cleanup goroutine will be automatically stopped when router.Shutdown() is called.
// This is the recommended way to use rate limiting.
func RateLimitWithRouter(router interface{ RegisterCleanup(func()) }, requestsPerSecond, burst int) nimbus.Middleware {
	limiter := NewRateLimiter(requestsPerSecond, burst)
	router.RegisterCleanup(limiter.Close)

	return func(next nimbus.Handler) nimbus.Handler {
		return func(ctx *nimbus.Context) (any, int, error) {
			// Use IP address as key
			key := ctx.Request.RemoteAddr

			if !limiter.allow(key) {
				return nil, http.StatusTooManyRequests, nimbus.NewAPIError("rate_limit_exceeded", "Too many requests, please try again later")
			}

			return next(ctx)
		}
	}
}

// RateLimit returns a rate limiting middleware
// Limits requests per IP address
// DEPRECATED: Use RateLimitWithRouter instead for automatic cleanup.
// Note: The rate limiter's cleanup goroutine will run until the application exits
// or ShutdownAllRateLimiters() is called
func RateLimit(requestsPerSecond, burst int) nimbus.Middleware {
	limiter := NewRateLimiter(requestsPerSecond, burst)
	registerLimiter(limiter)

	return func(next nimbus.Handler) nimbus.Handler {
		return func(ctx *nimbus.Context) (any, int, error) {
			// Use IP address as key
			key := ctx.Request.RemoteAddr

			if !limiter.allow(key) {
				return nil, http.StatusTooManyRequests, nimbus.NewAPIError("rate_limit_exceeded", "Too many requests, please try again later")
			}

			return next(ctx)
		}
	}
}

// RateLimitByHeaderWithRouter returns a rate limiting middleware based on a header value
// and registers cleanup with the router.
// Useful for API key based rate limiting.
// The rate limiter's cleanup goroutine will be automatically stopped when router.Shutdown() is called.
// This is the recommended way to use rate limiting.
func RateLimitByHeaderWithRouter(router interface{ RegisterCleanup(func()) }, header string, requestsPerSecond, burst int) nimbus.Middleware {
	limiter := NewRateLimiter(requestsPerSecond, burst)
	router.RegisterCleanup(limiter.Close)

	return func(next nimbus.Handler) nimbus.Handler {
		return func(ctx *nimbus.Context) (any, int, error) {
			key := ctx.GetHeader(header)
			if key == "" {
				key = ctx.Request.RemoteAddr
			}

			if !limiter.allow(key) {
				return nil, http.StatusTooManyRequests, nimbus.NewAPIError("rate_limit_exceeded", "Too many requests, please try again later")
			}

			return next(ctx)
		}
	}
}

// RateLimitByHeader returns a rate limiting middleware based on a header value
// Useful for API key based rate limiting
// DEPRECATED: Use RateLimitByHeaderWithRouter instead for automatic cleanup.
// Note: The rate limiter's cleanup goroutine will run until the application exits
// or ShutdownAllRateLimiters() is called
func RateLimitByHeader(header string, requestsPerSecond, burst int) nimbus.Middleware {
	limiter := NewRateLimiter(requestsPerSecond, burst)
	registerLimiter(limiter)

	return func(next nimbus.Handler) nimbus.Handler {
		return func(ctx *nimbus.Context) (any, int, error) {
			key := ctx.GetHeader(header)
			if key == "" {
				key = ctx.Request.RemoteAddr
			}

			if !limiter.allow(key) {
				return nil, http.StatusTooManyRequests, nimbus.NewAPIError("rate_limit_exceeded", "Too many requests, please try again later")
			}

			return next(ctx)
		}
	}
}
