package nimbus

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
)

const (
	ContextKeyValidatedBody   = "validated_body"
	ContextKeyValidatedQuery  = "validated_query"
	ContextKeyValidatedParams = "validated_params"

	StatusCodeKey = "status_code"
)

// A sync.Pool for Context objects to reduce allocations.
var contextPool = sync.Pool{
	New: func() any {
		return &Context{
			// https://github.com/golang/go/blob/45eee553e29770a264c378bccbb80c44807609f4/src/internal/runtime/maps/map.go#L24
			// pre-allocate one bucket (8 entries) to avoid rehashing and allocation overhead
			// PathParams is not pre-allocated - it's set by the router only when needed (nil for static routes)
			PathParams: nil, // Saves 272 bytes per static route request
			// values is not pre-allocated - it's created on first Set() call (lazy initialization)
			values: nil, // Saves 272 bytes when no context values are used
		}
	},
}

// Context is a wrapper around http request/response with helpers.
// Access context.Context via c.Request.Context() for cancellation, timeouts, and tracing.
// It is request-scoped and should be passed through the handler chain.
type Context struct {
	Writer  http.ResponseWriter
	Request *http.Request
	// PathParams contains extracted path parameters from the route (e.g., :id, :name).
	PathParams map[string]string
	// queryCache stores parsed query parameters to avoid re-parsing on each Query() call.
	// Lazily initialized on first Query() access. Saves significant overhead for endpoints
	// that access multiple query parameters (pagination, filtering, search, etc.).
	queryCache url.Values
	// values is a request-scoped key-value store for middleware communication.
	// Used to pass data between middleware and handlers (e.g., request_id, user, validated_body).
	// Private to force use of the Context.Set and Context.Get methods.
	values map[string]any
}

// NewContext grabs a context from the pool and initializes it.
// Access context.Context via ctx.Request.Context() for cancellation,
// timeouts, and distributed tracing.
func NewContext(w http.ResponseWriter, r *http.Request) *Context {
	ctx := contextPool.Get().(*Context)
	ctx.Writer = w
	ctx.Request = r
	return ctx
}

// Reset the context for reuse.
func (c *Context) reset() {
	c.Writer = nil
	c.Request = nil

	// Strategy: Keep maps allocated if they're small (≤8 entries = 1 bucket)
	// Only recreate if they grew too large (to prevent memory bloat from pooling huge maps)

	// PathParams may be nil for static routes, so check before clearing
	if c.PathParams != nil {
		if len(c.PathParams) > 8 {
			// Map grew too large, recreate with reasonable capacity (1 bucket)
			c.PathParams = make(map[string]string, 8)
		} else {
			// Map is small, just clear and reuse the allocation
			clear(c.PathParams)
		}
	}

	// Clear query cache (will be repopulated on next request if Query() is called)
	c.queryCache = nil

	// values may be nil if never used, check before clearing
	if c.values != nil {
		if len(c.values) > 8 {
			// Map grew too large, recreate with reasonable capacity (1 bucket)
			c.values = make(map[string]any, 8)
		} else {
			// Map is small, just clear and reuse the allocation
			clear(c.values)
		}
	}
}

// Release the context to the pool for reuse.
// Should be called after request handling is complete.
func (c *Context) Release() {
	c.reset()
	contextPool.Put(c)
}

// Param retrieves a path parameter by name safely (handles nil PathParams).
// Returns empty string if parameter doesn't exist.
// Example: id := ctx.Param("id")
func (c *Context) Param(name string) string {
	if c.PathParams == nil {
		return ""
	}
	return c.PathParams[name]
}

// Query retrieves a query parameter by name.
// The parsed query parameters are cached after the first call to avoid re-parsing
// on subsequent Query() calls. This provides significant performance benefits for
// endpoints that access multiple query parameters (e.g., pagination, filtering, search).
// Benchmark: 5x faster and 80% less memory for handlers accessing 5+ query params.
func (c *Context) Query(name string) string {
	if c.queryCache == nil {
		c.queryCache = c.Request.URL.Query()
	}
	return c.queryCache.Get(name)
}

// Bind and validate query parameters using a schema to a struct.
func (c *Context) BindAndValidateQuery(target any, schema *Schema) error {
	return ValidateQuery(c.Request.URL.Query(), target, schema)
}

// Bind and validate JSON using a schema to a struct.
func (c *Context) BindAndValidateJSON(target any, schema *Schema) error {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}

	return ValidateJSON(body, target, schema)
}

// Set writer with standardized validation error response.
// Returns (nil, 0, nil) to signal the handler that the response has been written.
func (c *Context) SendValidationError(errors ValidationErrors) (any, int, error) {
	return c.JSON(http.StatusBadRequest, map[string]any{
		"error":   "validation_failed",
		"message": "Request validation failed",
		"details": errors,
	})
}

// Set writer the statusCode and data as JSON.
// Returns (nil, 0, nil) to signal the handler that the response has been written.
func (c *Context) JSON(statusCode int, data any) (any, int, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, 0, err
	}
	return c.Data(statusCode, "application/json", jsonData)
}

// Set writer with plain text response.
// Returns (nil, 0, nil) to signal the handler that the response has been written.
func (c *Context) String(statusCode int, format string) (any, int, error) {
	return c.Data(statusCode, "text/plain", []byte(format))
}

// Set writer with HTML response.
// Returns (nil, 0, nil) to signal the handler that the response has been written.
func (c *Context) HTML(statusCode int, html string) (any, int, error) {
	return c.Data(statusCode, "text/html; charset=utf-8", []byte(html))
}

// Set writer with raw bytes as response.
// Returns (nil, 0, nil) to signal the handler that the response has been written.
func (c *Context) Data(statusCode int, contentType string, data []byte) (any, int, error) {
	c.Set(StatusCodeKey, statusCode) // Store for logging
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(statusCode)
	_, err := c.Writer.Write(data)
	return nil, 0, err
}

// Set writer with redirect response; redirect to the given location.
// Status code should be 301 (http.StatusMovedPermanently), 302 (http.StatusFound), 307 (http.StatusTemporaryRedirect), or 308 (http.StatusPermanentRedirect).
func (c *Context) Redirect(statusCode int, location string) {
	c.Set(StatusCodeKey, statusCode) // Store for logging
	http.Redirect(c.Writer, c.Request, location, statusCode)
}

// Header sets a response header.
func (c *Context) Header(key, value string) {
	c.Writer.Header().Set(key, value)
}

// GetHeader gets a request header.
func (c *Context) GetHeader(key string) string {
	return c.Request.Header.Get(key)
}

// Set stores a value in the context.
// Lazy-initializes the values map on first use.
func (c *Context) Set(key string, value any) {
	if c.values == nil {
		c.values = make(map[string]any, 8)
	}
	c.values[key] = value
}

// Get retrieves a value from the context.
func (c *Context) Get(key string) (any, bool) {
	if c.values == nil {
		return nil, false
	}
	value, exists := c.values[key]
	return value, exists
}

// GetString retrieves a string value from the context.
func (c *Context) GetString(key string) string {
	if c.values == nil {
		return ""
	}
	if value, ok := c.values[key]; ok {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return ""
}

// GetInt retrieves an int value from the context.
func (c *Context) GetInt(key string) int {
	if c.values == nil {
		return 0
	}
	if value, ok := c.values[key]; ok {
		if i, ok := value.(int); ok {
			return i
		}
	}
	return 0
}

// GetBool retrieves a bool value from the context.
func (c *Context) GetBool(key string) bool {
	if c.values == nil {
		return false
	}
	if value, ok := c.values[key]; ok {
		if b, ok := value.(bool); ok {
			return b
		}
	}
	return false
}

// Body returns the request body as bytes.
func (c *Context) Body() ([]byte, error) {
	return io.ReadAll(c.Request.Body)
}

// Method returns the HTTP method.
func (c *Context) Method() string {
	return c.Request.Method
}
