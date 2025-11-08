package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DylanHalstead/nimbus"
)

func TestBodyLimit(t *testing.T) {
	tests := []struct {
		name           string
		limit          int64
		bodySize       int64
		expectStatus   int
		expectError    bool
	}{
		{
			name:         "within limit",
			limit:        1 * MB,
			bodySize:     500 * KB,
			expectStatus: http.StatusOK,
			expectError:  false,
		},
		{
			name:         "at limit",
			limit:        1 * MB,
			bodySize:     1 * MB,
			expectStatus: http.StatusOK,
			expectError:  false,
		},
		{
			name:         "exceeds limit",
			limit:        1 * MB,
			bodySize:     2 * MB,
			expectStatus: http.StatusRequestEntityTooLarge,
			expectError:  true,
		},
		{
			name:         "small limit exceeded",
			limit:        100,
			bodySize:     200,
			expectStatus: http.StatusRequestEntityTooLarge,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := nimbus.NewRouter()

			// Apply body limit middleware
			router.Use(BodyLimit(tt.limit))

			// Add test handler
	router.AddRoute(http.MethodPost, "/test", func(ctx *nimbus.Context) (any, int, error) {
		// Try to read body
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			return nil, 0, err  // This will propagate MaxBytesError
		}
		return map[string]any{
			"size": len(body),
		}, http.StatusOK, nil
	})

		// Create request with body of specified size
		body := make([]byte, tt.bodySize)
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = tt.bodySize // Important for MaxBytesReader
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

			if w.Code != tt.expectStatus {
				t.Errorf("expected status %d, got %d", tt.expectStatus, w.Code)
			}

			if tt.expectError {
				responseBody := w.Body.String()
				if !strings.Contains(responseBody, "payload_too_large") &&
				   !strings.Contains(responseBody, "too large") {
					t.Errorf("expected error response, got: %s", responseBody)
				}
			}
		})
	}
}

func TestBodyLimitWithConfig(t *testing.T) {
	router := nimbus.NewRouter()

	// Custom config with skip paths
	router.Use(BodyLimitWithConfig(BodyLimitConfig{
		MaxBytes:     100,
		ErrorMessage: "Custom error message",
		SkipPaths:    []string{"/health", "/metrics"},
	}))

	router.AddRoute(http.MethodPost, "/test", func(ctx *nimbus.Context) (any, int, error) {
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			return nil, 0, err  // Propagate error from ReadAll
		}
		return map[string]any{"size": len(body)}, http.StatusOK, nil
	})

	router.AddRoute(http.MethodPost, "/health", func(ctx *nimbus.Context) (any, int, error) {
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			return nil, 0, err  // Propagate error from ReadAll
		}
		return map[string]any{"size": len(body)}, http.StatusOK, nil
	})

	t.Run("applies limit to non-skipped path", func(t *testing.T) {
		body := make([]byte, 200) // Exceeds 100 byte limit
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
		req.ContentLength = 200
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected status 413, got %d", w.Code)
		}

		if !strings.Contains(w.Body.String(), "Custom error message") {
			t.Errorf("expected custom error message, got: %s", w.Body.String())
		}
	})

	t.Run("skips limit for skip paths", func(t *testing.T) {
		body := make([]byte, 200) // Exceeds 100 byte limit but should be allowed
		req := httptest.NewRequest(http.MethodPost, "/health", bytes.NewReader(body))
		req.ContentLength = 200
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
	})
}

func TestBodyLimitOnlyAppliesToMethodsWithBody(t *testing.T) {
	router := nimbus.NewRouter()

	// Very restrictive limit
	router.Use(BodyLimit(10))

	router.AddRoute(http.MethodGet, "/test", func(ctx *nimbus.Context) (any, int, error) {
		return map[string]string{"message": "ok"}, http.StatusOK, nil
	})

	router.AddRoute(http.MethodPost, "/test", func(ctx *nimbus.Context) (any, int, error) {
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			return nil, 0, err  // Propagate error
		}
		return map[string]any{"size": len(body)}, http.StatusOK, nil
	})

	t.Run("GET request not affected by body limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
	})

	t.Run("POST request affected by body limit", func(t *testing.T) {
		body := make([]byte, 100) // Exceeds 10 byte limit
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
		req.ContentLength = 100
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected status 413, got %d", w.Code)
		}
	})
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		hasError bool
	}{
		// Valid formats
		{"1B", 1, false},
		{"1KB", 1024, false},
		{"1MB", 1024 * 1024, false},
		{"1GB", 1024 * 1024 * 1024, false},
		
		// With spaces
		{" 1MB ", 1024 * 1024, false},
		
		// Case insensitive
		{"1kb", 1024, false},
		{"1mb", 1024 * 1024, false},
		{"1gb", 1024 * 1024 * 1024, false},
		
		// Decimals
		{"1.5MB", int64(1.5 * 1024 * 1024), false},
		{"0.5GB", int64(0.5 * 1024 * 1024 * 1024), false},
		{"2.5KB", int64(2.5 * 1024), false},
		
		// Short forms
		{"1K", 1024, false},
		{"1M", 1024 * 1024, false},
		{"1G", 1024 * 1024 * 1024, false},
		
		// Just number (assumes bytes)
		{"100", 100, false},
		
		// Invalid formats
		{"", 0, true},
		{"invalid", 0, true},
		{"1XB", 0, true},
		{"MB", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseSize(tt.input)
			
			if tt.hasError {
				if err == nil {
					t.Errorf("expected error for input %q, got nil", tt.input)
				}
				return
			}
			
			if err != nil {
				t.Errorf("unexpected error for input %q: %v", tt.input, err)
				return
			}
			
			if result != tt.expected {
				t.Errorf("for input %q, expected %d bytes, got %d", tt.input, tt.expected, result)
			}
		})
	}
}

func TestBodyLimitFromString(t *testing.T) {
	router := nimbus.NewRouter()
	router.Use(BodyLimitFromString("1KB"))

	router.AddRoute(http.MethodPost, "/test", func(ctx *nimbus.Context) (any, int, error) {
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			return nil, 0, err  // Propagate error
		}
		return map[string]any{"size": len(body)}, http.StatusOK, nil
	})

	t.Run("within limit", func(t *testing.T) {
		body := make([]byte, 512) // 512 bytes (< 1KB)
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
	})

	t.Run("exceeds limit", func(t *testing.T) {
		body := make([]byte, 2048) // 2KB (> 1KB)
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
		req.ContentLength = 2048
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected status 413, got %d", w.Code)
		}
	})
}

func TestBodyLimitPresets(t *testing.T) {
	tests := []struct {
		name     string
		preset   nimbus.Middleware
		expected int64
	}{
		{"API", BodyLimitAPI(), DefaultAPILimit},
		{"Upload", BodyLimitUpload(), DefaultUploadLimit},
		{"Webhook", BodyLimitWebhook(), DefaultWebhookLimit},
		{"Stream", BodyLimitStream(), DefaultStreamLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := nimbus.NewRouter()
			router.Use(tt.preset)

			router.AddRoute(http.MethodPost, "/test", func(ctx *nimbus.Context) (any, int, error) {
				body, err := io.ReadAll(ctx.Request.Body)
				if err != nil {
					return nil, 0, err  // Propagate error
				}
				return map[string]any{"size": len(body)}, http.StatusOK, nil
			})

		// Test with body just under limit
		underLimit := make([]byte, tt.expected-100)
		req1 := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(underLimit))
		req1.ContentLength = tt.expected - 100
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)

		if w1.Code != http.StatusOK {
			t.Errorf("%s preset: expected status 200 for body under limit, got %d", tt.name, w1.Code)
		}

		// Test with body over limit
		overLimit := make([]byte, tt.expected+1000)
		req2 := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(overLimit))
		req2.ContentLength = tt.expected + 1000
		w2 := httptest.NewRecorder()
		router.ServeHTTP(w2, req2)

		if w2.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s preset: expected status 413 for body over limit, got %d", tt.name, w2.Code)
		}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{100, "100B"},
		{1024, "1.00KB"},
		{1536, "1.50KB"},
		{1048576, "1.00MB"},
		{2097152, "2.00MB"},
		{1073741824, "1.00GB"},
		{2147483648, "2.00GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatBytes(%d) = %q, expected %q", tt.bytes, result, tt.expected)
			}
		})
	}
}

func TestBodyLimitPanicOnInvalidConfig(t *testing.T) {
	t.Run("panic on zero MaxBytes", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic for MaxBytes = 0")
			}
		}()
		BodyLimitWithConfig(BodyLimitConfig{MaxBytes: 0})
	})

	t.Run("panic on negative MaxBytes", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic for negative MaxBytes")
			}
		}()
		BodyLimitWithConfig(BodyLimitConfig{MaxBytes: -1})
	})

	t.Run("panic on invalid size string", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic for invalid size string")
			}
		}()
		BodyLimitFromString("invalid")
	})
}

func TestBodyLimitWithJSON(t *testing.T) {
	router := nimbus.NewRouter()
	router.Use(BodyLimit(100)) // Very small limit

	type TestRequest struct {
		Data string `json:"data"`
	}

	router.AddRoute(http.MethodPost, "/test", func(ctx *nimbus.Context) (any, int, error) {
		body, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			return nil, 0, err
		}
		// In real code, you'd unmarshal into a struct here
		_ = body
		return map[string]string{"status": "ok"}, http.StatusOK, nil
	})

	t.Run("small JSON accepted", func(t *testing.T) {
		json := `{"data":"small"}`
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(json))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
	})

	t.Run("large JSON rejected", func(t *testing.T) {
		// Create JSON larger than 100 bytes
		largeData := strings.Repeat("x", 200)
		json := `{"data":"` + largeData + `"}`
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(json))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected status 413, got %d", w.Code)
		}
	})
}

