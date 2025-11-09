package nimbus

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRouter_GET(t *testing.T) {
	router := NewRouter()

	router.AddRoute(http.MethodGet, "/test", func(ctx *Context) (any, int, error) {
		return map[string]any{"message": "success"}, http.StatusOK, nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestRouter_PathParameters(t *testing.T) {
	router := NewRouter()

	router.AddRoute(http.MethodGet, "/users/:id", func(ctx *Context) (any, int, error) {
		id := ctx.PathParams["id"]
		return map[string]any{"id": id}, http.StatusOK, nil
	})

	req := httptest.NewRequest("GET", "/users/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestRouter_NotFound(t *testing.T) {
	router := NewRouter()

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

func TestRouter_Middleware(t *testing.T) {
	router := NewRouter()

	called := false
	middleware := func(next Handler) Handler {
		return func(ctx *Context) (any, int, error) {
			called = true
			return next(ctx)
		}
	}

	router.Use(middleware)
	router.AddRoute(http.MethodGet, "/test", func(ctx *Context) (any, int, error) {
		return map[string]any{"message": "ok"}, http.StatusOK, nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if !called {
		t.Error("Middleware was not called")
	}
}

func TestRouter_Group(t *testing.T) {
	router := NewRouter()

	api := router.Group("/api/v1")
	api.AddRoute(http.MethodGet, "/users", func(ctx *Context) (any, int, error) {
		return map[string]any{"users": []string{}}, http.StatusOK, nil
	})

	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestRouter_WithPathParams(t *testing.T) {
	router := NewRouter()

	type UserParams struct {
		ID string `path:"id"`
	}

	userParamsValidator := NewValidator(&UserParams{})

	// Use WithTyped for type-safe parameter handling
	handler := func(ctx *Context, req *TypedRequest[UserParams, struct{}, struct{}]) (any, int, error) {
		return map[string]any{"id": req.Params.ID}, http.StatusOK, nil
	}

	router.AddRoute(http.MethodGet, "/users/:id", WithTyped(handler, userParamsValidator, nil, nil))

	req := httptest.NewRequest("GET", "/users/123", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

// TestMatchPattern has been removed as matchPattern() function was optimized away.
// Route matching is now handled by the radix tree implementation.
// See tree_test.go for comprehensive route matching tests.

// TestConcurrentAddAndServe tests for race conditions between route registration and serving
// Run with: go test -race -run TestConcurrentAddAndServe
func TestConcurrentAddAndServe(t *testing.T) {
	router := NewRouter()
	
	// Add initial route
	router.AddRoute(http.MethodGet, "/initial", func(ctx *Context) (any, int, error) {
		return "initial", 200, nil
	})
	
	var wg sync.WaitGroup
	
	// Concurrent route registration (writers)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				router.AddRoute(http.MethodGet, "/dynamic/:id", func(ctx *Context) (any, int, error) {
					return "ok", 200, nil
				})
			}
		}(i)
	}
	
	// Concurrent request handling (readers)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				req := httptest.NewRequest(http.MethodGet, "/dynamic/123", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
			}
		}()
	}
	
	wg.Wait()
}

// TestConcurrentTreeMutation tests the specific tree mutation race condition
// that was fixed by path copying optimization. Run with: go test -race -run TestConcurrentTreeMutation
func TestConcurrentTreeMutation(t *testing.T) {
	router := NewRouter()
	
	var wg sync.WaitGroup
	
	// Multiple goroutines adding routes to the same method (shares same tree)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				// Different paths but same tree - will trigger insertWithCopy() concurrently
				router.AddRoute(http.MethodPost, "/api/resource/:id/action/:action", func(ctx *Context) (any, int, error) {
					return "ok", 200, nil
				})
			}
		}(i)
	}
	
	// Concurrent readers hitting the same tree
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				req := httptest.NewRequest(http.MethodPost, "/api/resource/123/action/delete", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
			}
		}()
	}
	
	wg.Wait()
}
