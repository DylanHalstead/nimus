package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DylanHalstead/nimbus"
)

// Common size constants for convenience
const (
	B  = 1
	KB = 1024 * B
	MB = 1024 * KB
	GB = 1024 * MB
)

// Recommended limits for different use cases
const (
	DefaultAPILimit      = 1 * MB   // Standard JSON API requests
	DefaultUploadLimit   = 10 * MB  // File uploads
	DefaultWebhookLimit  = 5 * MB   // Webhook payloads
	DefaultStreamLimit   = 100 * MB // Streaming endpoints
)

// BodyLimitConfig defines configuration for request body size limits
type BodyLimitConfig struct {
	// MaxBytes is the maximum allowed size in bytes
	// Use the constants (KB, MB, GB) for readability
	MaxBytes int64

	// ErrorMessage is a custom error message (optional)
	// Default: "Request body too large. Maximum size is X"
	ErrorMessage string

	// SkipPaths are paths to skip body limit checking (e.g., health checks)
	SkipPaths []string
}

// BodyLimit returns middleware that limits request body size to prevent DoS attacks.
// Uses Go's standard http.MaxBytesReader under the hood.
//
// Examples:
//
//	// Default limit (1MB) for all routes
//	router.Use(middleware.BodyLimit(middleware.DefaultAPILimit))
//
//	// Custom limit with size constants
//	router.Use(middleware.BodyLimit(10 * middleware.MB))
//
//	// Different limits for different route groups
//	api := router.Group("/api")
//	api.Use(middleware.BodyLimit(1 * middleware.MB))  // 1MB for API
//
//	uploads := router.Group("/uploads")
//	uploads.Use(middleware.BodyLimit(100 * middleware.MB))  // 100MB for uploads
//
//	// With custom configuration
//	router.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
//	    MaxBytes:     5 * middleware.MB,
//	    ErrorMessage: "Your upload is too large. Max 5MB allowed.",
//	    SkipPaths:    []string{"/health", "/metrics"},
//	}))
func BodyLimit(maxBytes int64) nimbus.Middleware {
	return BodyLimitWithConfig(BodyLimitConfig{
		MaxBytes: maxBytes,
	})
}

// BodyLimitWithConfig returns middleware with custom configuration
func BodyLimitWithConfig(config BodyLimitConfig) nimbus.Middleware {
	// Validate config
	if config.MaxBytes <= 0 {
		panic("BodyLimit: MaxBytes must be greater than 0")
	}

	// Set default error message
	if config.ErrorMessage == "" {
		config.ErrorMessage = fmt.Sprintf("Request body too large. Maximum size is %s", 
			formatBytes(config.MaxBytes))
	}

	return func(next nimbus.Handler) nimbus.Handler {
		return func(ctx *nimbus.Context) (any, int, error) {
			path := ctx.Request.URL.Path

			// Skip body limit for certain paths
			for _, skipPath := range config.SkipPaths {
				if path == skipPath {
					return next(ctx)
				}
			}

			// Only apply limit to requests with bodies (POST, PUT, PATCH)
			method := ctx.Request.Method
			if method != http.MethodPost && 
			   method != http.MethodPut && 
			   method != http.MethodPatch {
				return next(ctx)
			}

			// Wrap the request body with MaxBytesReader
			// This prevents reading more than MaxBytes from the body
			// Returns http.MaxBytesError if limit is exceeded
			ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, config.MaxBytes)

			// Call next handler
			data, status, err := next(ctx)

			// Check if error is due to body size limit
			if err != nil {
				// http.MaxBytesReader returns this specific error
				if isMaxBytesError(err) {
					return nil, http.StatusRequestEntityTooLarge, 
						nimbus.NewAPIError("payload_too_large", config.ErrorMessage)
				}
			}

			return data, status, err
		}
	}
}

// BodyLimitFromString parses a human-readable size string and returns middleware
// Supports formats like "1MB", "500KB", "2.5GB"
//
// Examples:
//
//	router.Use(middleware.BodyLimitFromString("1MB"))
//	router.Use(middleware.BodyLimitFromString("500KB"))
//	router.Use(middleware.BodyLimitFromString("2.5GB"))
func BodyLimitFromString(size string) nimbus.Middleware {
	bytes, err := ParseSize(size)
	if err != nil {
		panic(fmt.Sprintf("BodyLimit: invalid size string %q: %v", size, err))
	}
	return BodyLimit(bytes)
}

// ParseSize converts a human-readable size string to bytes
// Supports: "1B", "1KB", "1MB", "1GB" (case-insensitive)
// Also supports decimals: "1.5MB", "0.5GB"
func ParseSize(size string) (int64, error) {
	size = strings.TrimSpace(strings.ToUpper(size))
	
	// Extract number and unit
	var numStr string
	var unit string
	
	for i, c := range size {
		if (c >= '0' && c <= '9') || c == '.' {
			numStr += string(c)
		} else {
			unit = size[i:]
			break
		}
	}
	
	if numStr == "" {
		return 0, fmt.Errorf("invalid size format: %s", size)
	}
	
	// Parse the numeric value
	value, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", numStr)
	}
	
	// Convert based on unit
	var multiplier int64
	switch unit {
	case "B", "":
		multiplier = B
	case "KB", "K":
		multiplier = KB
	case "MB", "M":
		multiplier = MB
	case "GB", "G":
		multiplier = GB
	default:
		return 0, fmt.Errorf("unknown unit: %s (use B, KB, MB, or GB)", unit)
	}
	
	return int64(value * float64(multiplier)), nil
}

// formatBytes converts bytes to human-readable format
func formatBytes(bytes int64) string {
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2fMB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2fKB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// isMaxBytesError checks if an error is caused by exceeding body size limit
func isMaxBytesError(err error) bool {
	if err == nil {
		return false
	}
	
	// Check error message
	// http.MaxBytesReader returns "http: request body too large"
	errMsg := err.Error()
	return strings.Contains(errMsg, "request body too large") ||
	       strings.Contains(errMsg, "http: request body too large")
}

// Preset middleware functions for common use cases

// BodyLimitAPI returns middleware with API-friendly defaults (1MB)
// Use this for standard JSON API endpoints
func BodyLimitAPI() nimbus.Middleware {
	return BodyLimit(DefaultAPILimit)
}

// BodyLimitUpload returns middleware for file upload endpoints (10MB)
// Use this for routes that accept file uploads
func BodyLimitUpload() nimbus.Middleware {
	return BodyLimit(DefaultUploadLimit)
}

// BodyLimitWebhook returns middleware for webhook endpoints (5MB)
// Use this for webhook receivers (GitHub, Stripe, etc.)
func BodyLimitWebhook() nimbus.Middleware {
	return BodyLimit(DefaultWebhookLimit)
}

// BodyLimitStream returns middleware for streaming endpoints (100MB)
// Use this for routes that handle large streaming data
func BodyLimitStream() nimbus.Middleware {
	return BodyLimit(DefaultStreamLimit)
}

