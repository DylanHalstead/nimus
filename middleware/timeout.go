package middleware

import (
	"context"
	"time"

	"github.com/DylanHalstead/nimbus"
)

// Timeout middleware adds a deadline to requests.
// If the handler doesn't complete within the timeout, it returns a 504 Gateway Timeout.
//
// Example usage:
//
//	router.Use(middleware.Timeout(5 * time.Second))
//
// This is useful for preventing slow handlers from tying up resources.
func Timeout(timeout time.Duration) nimbus.MiddlewareFunc {
	return func(next nimbus.HandlerFunc) nimbus.HandlerFunc {
		return func(ctx *nimbus.Context) (any, int, error) {
			// Create timeout context from request's context
			timeoutCtx, cancel := context.WithTimeout(ctx.Request.Context(), timeout)
			defer cancel()

			// Replace request's context with timeout version
			ctx.Request = ctx.Request.WithContext(timeoutCtx)

			// Channel to receive handler result
			type result struct {
				data   any
				status int
				err    error
			}
			resultChan := make(chan result, 1)

			// Run handler in goroutine
			go func() {
				data, status, err := next(ctx)
				resultChan <- result{data, status, err}
			}()

			// Wait for either completion or timeout
			select {
			case res := <-resultChan:
				return res.data, res.status, res.err
			case <-timeoutCtx.Done():
				// Timeout occurred
				return nil, 504, nimbus.NewAPIError("timeout", "request timeout exceeded")
			}
		}
	}
}

// TimeoutWithSkip is like Timeout but skips certain paths.
// This is useful if you want timeouts on most endpoints but not on long-polling
// or streaming endpoints.
//
// Example:
//
//	router.Use(middleware.TimeoutWithSkip(5*time.Second, "/stream", "/events"))
func TimeoutWithSkip(timeout time.Duration, skipPaths ...string) nimbus.MiddlewareFunc {
	skipMap := make(map[string]bool, len(skipPaths))
	for _, path := range skipPaths {
		skipMap[path] = true
	}

	return func(next nimbus.HandlerFunc) nimbus.HandlerFunc {
		return func(ctx *nimbus.Context) (any, int, error) {
			// Skip timeout for certain paths
			if skipMap[ctx.Request.URL.Path] {
				return next(ctx)
			}

			// Apply timeout to request's context
			timeoutCtx, cancel := context.WithTimeout(ctx.Request.Context(), timeout)
			defer cancel()

			ctx.Request = ctx.Request.WithContext(timeoutCtx)

			type result struct {
				data   any
				status int
				err    error
			}
			resultChan := make(chan result, 1)

			go func() {
				data, status, err := next(ctx)
				resultChan <- result{data, status, err}
			}()

			select {
			case res := <-resultChan:
				return res.data, res.status, res.err
			case <-timeoutCtx.Done():
				return nil, 504, nimbus.NewAPIError("timeout", "request timeout exceeded")
			}
		}
	}
}

