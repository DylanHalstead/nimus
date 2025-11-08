package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/DylanHalstead/nimbus"
)

func TestNewRateLimiter(t *testing.T) {
	rate := 10
	capacity := 20
	limiter := NewRateLimiter(rate, capacity)
	defer limiter.Close()

	if limiter == nil {
		t.Fatal("expected limiter, got nil")
	}

	if limiter.rate != rate {
		t.Errorf("expected rate %d, got %d", rate, limiter.rate)
	}

	if limiter.capacity != capacity {
		t.Errorf("expected capacity %d, got %d", capacity, limiter.capacity)
	}

	// sync.Map is always initialized (not nil-able)
	// Just verify we can use it
	limiter.allow("test")

	if limiter.cleanup != time.Minute*5 {
		t.Errorf("expected cleanup duration %v, got %v", time.Minute*5, limiter.cleanup)
	}
}

func TestRateLimiter_Allow_FirstRequest(t *testing.T) {
	limiter := NewRateLimiter(10, 20)
	defer limiter.Close()

	if !limiter.allow("test-key") {
		t.Error("first request should be allowed")
	}

	// Check bucket was created (lock-free load from sync.Map)
	value, exists := limiter.buckets.Load("test-key")
	if !exists {
		t.Error("bucket should be created for new key")
	}

	bucket := value.(*bucket)

	// Should have capacity - 1 tokens left
	expectedTokens := int64(19)
	actualTokens := bucket.tokens.Load()
	if actualTokens != expectedTokens {
		t.Errorf("expected %d tokens, got %d", expectedTokens, actualTokens)
	}
}

func TestRateLimiter_Allow_BurstCapacity(t *testing.T) {
	capacity := 5
	limiter := NewRateLimiter(1, capacity)
	defer limiter.Close()
	key := "test-key"

	// Should allow 'capacity' number of requests immediately
	for i := 0; i < capacity; i++ {
		if !limiter.allow(key) {
			t.Errorf("request %d should be allowed (within burst capacity)", i+1)
		}
	}

	// Next request should be denied (bucket exhausted)
	if limiter.allow(key) {
		t.Error("request beyond capacity should be denied")
	}
}

func TestRateLimiter_Allow_TokenRefill(t *testing.T) {
	rate := 10 // 10 tokens per second
	capacity := 10
	limiter := NewRateLimiter(rate, capacity)
	defer limiter.Close()
	key := "test-key"

	// Exhaust the bucket
	for i := 0; i < capacity; i++ {
		limiter.allow(key)
	}

	// Should be denied immediately
	if limiter.allow(key) {
		t.Error("request should be denied when bucket is empty")
	}

	// Wait for tokens to refill (100ms = 1 token at 10/sec)
	time.Sleep(150 * time.Millisecond)

	// Should have at least 1 token now
	if !limiter.allow(key) {
		t.Error("request should be allowed after token refill")
	}
}

func TestRateLimiter_Allow_MultipleKeys(t *testing.T) {
	limiter := NewRateLimiter(10, 5)
	defer limiter.Close()

	keys := []string{"key1", "key2", "key3"}

	// Each key should have independent buckets
	for _, key := range keys {
		for i := 0; i < 5; i++ {
			if !limiter.allow(key) {
				t.Errorf("request %d for %s should be allowed", i+1, key)
			}
		}

		// Next request should be denied for this key
		if limiter.allow(key) {
			t.Errorf("request beyond capacity for %s should be denied", key)
		}
	}

	// Verify separate buckets were created (count entries in sync.Map)
	count := 0
	limiter.buckets.Range(func(key, value any) bool {
		count++
		return true
	})
	if count != len(keys) {
		t.Errorf("expected %d buckets, got %d", len(keys), count)
	}
}

func TestRateLimiter_TokenRefillDoesNotExceedCapacity(t *testing.T) {
	rate := 100 // 100 tokens per second
	capacity := 5
	limiter := NewRateLimiter(rate, capacity)
	defer limiter.Close()
	key := "test-key"

	// Use one token
	limiter.allow(key)

	// Wait for refill period (should refill to capacity, not beyond)
	time.Sleep(200 * time.Millisecond)

	// Should be able to use exactly 'capacity' tokens
	for i := 0; i < capacity; i++ {
		if !limiter.allow(key) {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// Next should be denied (proves we didn't exceed capacity)
	if limiter.allow(key) {
		t.Error("tokens should not exceed capacity")
	}
}

func TestRateLimit_Middleware(t *testing.T) {
	middleware := RateLimit(10, 5)

	nextCalled := false
	handler := middleware(func(ctx *nimbus.Context) (any, int, error) {
		nextCalled = true
		return map[string]string{"message": "success"}, http.StatusOK, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	ctx := nimbus.NewContext(w, req)

	data, statusCode, err := handler(ctx)

	if !nextCalled {
		t.Error("next handler should be called for first request")
	}

	if statusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, statusCode)
	}

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if data == nil {
		t.Error("expected data, got nil")
	}
}

func TestRateLimit_ExceedsLimit(t *testing.T) {
	capacity := 3
	middleware := RateLimit(1, capacity)

	handler := middleware(func(ctx *nimbus.Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"

	// Make requests up to capacity
	for i := 0; i < capacity; i++ {
		w := httptest.NewRecorder()
		ctx := nimbus.NewContext(w, req)
		_, statusCode, err := handler(ctx)

		if statusCode != http.StatusOK {
			t.Errorf("request %d: expected status %d, got %d", i+1, http.StatusOK, statusCode)
		}
		if err != nil {
			t.Errorf("request %d: expected no error, got %v", i+1, err)
		}
	}

	// Next request should be rate limited
	w := httptest.NewRecorder()
	ctx := nimbus.NewContext(w, req)
	_, statusCode, err := handler(ctx)

	if statusCode != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, statusCode)
	}

	if err == nil {
		t.Error("expected rate limit error, got nil")
	}

		apiErr, ok := err.(*nimbus.APIError)
	if !ok {
		t.Errorf("expected *nimbus.APIError, got %T", err)
	}

	if apiErr.Code != "rate_limit_exceeded" {
		t.Errorf("expected error code 'rate_limit_exceeded', got '%s'", apiErr.Code)
	}
}

func TestRateLimit_DifferentIPs(t *testing.T) {
	middleware := RateLimit(10, 2)

	handler := middleware(func(ctx *nimbus.Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})

	ips := []string{"192.168.1.1:12345", "192.168.1.2:12345", "10.0.0.1:54321"}

	// Each IP should have independent rate limits
	for _, ip := range ips {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = ip

		// Make 2 requests (capacity)
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			ctx := nimbus.NewContext(w, req)
			_, statusCode, _ := handler(ctx)

			if statusCode != http.StatusOK {
				t.Errorf("IP %s request %d: expected status %d, got %d", ip, i+1, http.StatusOK, statusCode)
			}
		}

		// Third request should be rate limited
		w := httptest.NewRecorder()
		ctx := nimbus.NewContext(w, req)
		_, statusCode, _ := handler(ctx)

		if statusCode != http.StatusTooManyRequests {
			t.Errorf("IP %s: expected status %d, got %d", ip, http.StatusTooManyRequests, statusCode)
		}
	}
}

func TestRateLimitByHeader_WithHeader(t *testing.T) {
	middleware := RateLimitByHeader("X-API-Key", 10, 3)

	handler := middleware(func(ctx *nimbus.Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})

	apiKey := "test-api-key-123"
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-API-Key", apiKey)

	// Make requests up to capacity
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		ctx := nimbus.NewContext(w, req)
		_, statusCode, err := handler(ctx)

		if statusCode != http.StatusOK {
			t.Errorf("request %d: expected status %d, got %d", i+1, http.StatusOK, statusCode)
		}
		if err != nil {
			t.Errorf("request %d: expected no error, got %v", i+1, err)
		}
	}

	// Next request should be rate limited
	w := httptest.NewRecorder()
	ctx := nimbus.NewContext(w, req)
	_, statusCode, _ := handler(ctx)

	if statusCode != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, statusCode)
	}
}

func TestRateLimitByHeader_WithoutHeader_FallbackToIP(t *testing.T) {
	middleware := RateLimitByHeader("X-API-Key", 10, 2)

	handler := middleware(func(ctx *nimbus.Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	// No X-API-Key header set

	// Should fall back to IP-based rate limiting
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		ctx := nimbus.NewContext(w, req)
		_, statusCode, _ := handler(ctx)

		if statusCode != http.StatusOK {
			t.Errorf("request %d: expected status %d, got %d", i+1, http.StatusOK, statusCode)
		}
	}

	// Should be rate limited based on IP
	w := httptest.NewRecorder()
	ctx := nimbus.NewContext(w, req)
	_, statusCode, _ := handler(ctx)

	if statusCode != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, statusCode)
	}
}

func TestRateLimitByHeader_DifferentKeys(t *testing.T) {
	middleware := RateLimitByHeader("X-API-Key", 10, 2)

	handler := middleware(func(ctx *nimbus.Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})

	keys := []string{"key1", "key2", "key3"}

	// Each API key should have independent rate limits
	for _, key := range keys {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-API-Key", key)

		// Make requests up to capacity
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			ctx := nimbus.NewContext(w, req)
			_, statusCode, _ := handler(ctx)

			if statusCode != http.StatusOK {
				t.Errorf("key %s request %d: expected status %d, got %d", key, i+1, http.StatusOK, statusCode)
			}
		}

		// Next request should be rate limited
		w := httptest.NewRecorder()
		ctx := nimbus.NewContext(w, req)
		_, statusCode, _ := handler(ctx)

		if statusCode != http.StatusTooManyRequests {
			t.Errorf("key %s: expected status %d, got %d", key, http.StatusTooManyRequests, statusCode)
		}
	}
}

func TestMin(t *testing.T) {
	testCases := []struct {
		a, b     int
		expected int
	}{
		{5, 10, 5},
		{10, 5, 5},
		{7, 7, 7},
		{0, 5, 0},
		{-5, 5, -5},
		{-10, -5, -10},
	}

	for _, tc := range testCases {
		result := min(tc.a, tc.b)
		if result != tc.expected {
			t.Errorf("min(%d, %d) = %d, expected %d", tc.a, tc.b, result, tc.expected)
		}
	}
}

func TestRateLimiter_Close(t *testing.T) {
	limiter := NewRateLimiter(10, 20)

	// Allow a request to ensure limiter is working
	if !limiter.allow("test-key") {
		t.Error("Expected first request to be allowed")
	}

	// Close the limiter - this should stop the cleanup goroutine
	limiter.Close()

	// After closing, the limiter should still work for allow() calls
	// (only the cleanup goroutine stops, not the rate limiting itself)
	if !limiter.allow("test-key-2") {
		t.Error("Expected rate limiter to still work after Close()")
	}

	// Test that Close is idempotent (can be called multiple times)
	// This should not panic
	limiter.Close()
}

func TestRateLimiter_CleanupGoroutineStopped(t *testing.T) {
	// This test verifies the goroutine actually stops
	// We create a limiter with very short cleanup interval
	limiter := &RateLimiter{
		buckets:  sync.Map{}, // Lock-free map
		rate:     10,
		capacity: 20,
		cleanup:  time.Millisecond * 10, // Very short for testing
		done:     make(chan struct{}),
	}

	// Start the cleanup loop
	stopped := make(chan bool, 1)
	go func() {
		limiter.cleanupLoop()
		stopped <- true
	}()

	// Give it time to start
	time.Sleep(time.Millisecond * 5)

	// Close the limiter
	limiter.Close()

	// Wait for cleanup to stop (with timeout)
	select {
	case <-stopped:
		// Success - goroutine stopped
	case <-time.After(time.Second):
		t.Error("Cleanup goroutine did not stop after Close()")
	}
}
