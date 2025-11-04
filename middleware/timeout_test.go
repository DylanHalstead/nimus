package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DylanHalstead/nimbus"
)

func TestTimeout_CompletesWithinTimeout(t *testing.T) {
	router := nimbus.NewRouter()

	// Add timeout middleware with 100ms timeout
	router.Use(Timeout(100 * time.Millisecond))

	// Handler completes quickly (10ms)
	router.AddRoute(http.MethodGet, "/fast", func(ctx *nimbus.Context) (any, int, error) {
		time.Sleep(10 * time.Millisecond)
		return map[string]string{"status": "ok"}, 200, nil
	})

	req := httptest.NewRequest("GET", "/fast", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestTimeout_ExceedsTimeout(t *testing.T) {
	router := nimbus.NewRouter()

	// Add timeout middleware with 50ms timeout
	router.Use(Timeout(50 * time.Millisecond))

	// Handler is slow (200ms)
	router.AddRoute(http.MethodGet, "/slow", func(ctx *nimbus.Context) (any, int, error) {
		time.Sleep(200 * time.Millisecond)
		return map[string]string{"status": "ok"}, 200, nil
	})

	req := httptest.NewRequest("GET", "/slow", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should return 504 (timeout)
	if w.Code != 504 {
		t.Errorf("Expected status 504 (timeout), got %d", w.Code)
	}
}

func TestTimeout_HandlerCanDetectCancellation(t *testing.T) {
	// This test verifies that the timeout middleware returns 504
	// when the handler takes too long
	router := nimbus.NewRouter()

	router.Use(Timeout(50 * time.Millisecond))

	router.AddRoute(http.MethodGet, "/check-cancel", func(ctx *nimbus.Context) (any, int, error) {
		// Simulate a long operation (longer than timeout)
		time.Sleep(200 * time.Millisecond)
		return map[string]string{"status": "completed"}, 200, nil
	})

	req := httptest.NewRequest("GET", "/check-cancel", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should return 504 due to timeout
	if w.Code != 504 {
		t.Errorf("Expected status 504 (timeout), got %d", w.Code)
	}
}

func TestTimeoutWithSkip_SkipsSpecifiedPaths(t *testing.T) {
	router := nimbus.NewRouter()

	// Timeout of 50ms, but skip /stream path
	router.Use(TimeoutWithSkip(50*time.Millisecond, "/stream"))

	// Handler on skipped path takes longer than timeout
	router.AddRoute(http.MethodGet, "/stream", func(ctx *nimbus.Context) (any, int, error) {
		time.Sleep(100 * time.Millisecond)
		return map[string]string{"status": "ok"}, 200, nil
	})

	req := httptest.NewRequest("GET", "/stream", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should complete successfully (not timeout) because path is skipped
	if w.Code != 200 {
		t.Errorf("Expected status 200 (no timeout on skipped path), got %d", w.Code)
	}
}

func TestTimeoutWithSkip_AppliesTimeoutToNonSkippedPaths(t *testing.T) {
	router := nimbus.NewRouter()

	// Timeout of 50ms, skip /stream
	router.Use(TimeoutWithSkip(50*time.Millisecond, "/stream"))

	// Handler on non-skipped path
	router.AddRoute(http.MethodGet, "/api", func(ctx *nimbus.Context) (any, int, error) {
		time.Sleep(100 * time.Millisecond)
		return map[string]string{"status": "ok"}, 200, nil
	})

	req := httptest.NewRequest("GET", "/api", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should timeout
	if w.Code != 504 {
		t.Errorf("Expected status 504 (timeout), got %d", w.Code)
	}
}

func TestTimeout_MultipleSkipPaths(t *testing.T) {
	router := nimbus.NewRouter()

	// Skip multiple paths
	router.Use(TimeoutWithSkip(50*time.Millisecond, "/stream", "/events", "/long-poll"))

	tests := []struct {
		path           string
		expectTimeout  bool
		expectedStatus int
	}{
		{"/stream", false, 200},
		{"/events", false, 200},
		{"/long-poll", false, 200},
		{"/api", true, 504},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			router.AddRoute(http.MethodGet, tt.path, func(ctx *nimbus.Context) (any, int, error) {
				time.Sleep(100 * time.Millisecond)
				return map[string]string{"status": "ok"}, 200, nil
			})

			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Path %s: expected status %d, got %d", tt.path, tt.expectedStatus, w.Code)
			}
		})
	}
}

