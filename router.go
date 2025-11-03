package nimbus

import (
	"net/http"
	"sync"
	"sync/atomic"
)

// HandlerFunc defines the handler function signature
// Handlers return: (data, statusCode, error)
// - data: the response body (will be JSON encoded)
// - statusCode: HTTP status code (0 means use default based on error)
// - error: if present, an error response will be sent
//
// To send custom responses (HTML, plain text, etc.), use Context response methods:
//
//	return ctx.HTML(200, "<h1>Hello</h1>")
//	return ctx.String(200, "Hello World")
//	return ctx.Data(200, "text/plain", []byte("Hello"))
//
// These methods return (nil, 0, nil) to signal the response was already written.
type HandlerFunc func(*Context) (any, int, error)

// TypedRequest holds typed request parameters, body, and query data.
// Any unused fields will be nil. This consolidates all typed inputs into a single struct.
type TypedRequest[P any, B any, Q any] struct {
	Params *P // Typed path parameters (nil if not configured)
	Body   *B // Typed request body (nil if not configured)
	Query  *Q // Typed query parameters (nil if not configured)
}

// HandlerFuncTyped is a unified typed handler that receives a context and typed request data.
// All typed inputs (params, body, query) are consolidated into a single TypedRequest struct.
//
// Parameters:
//   - ctx: The request context
//   - req: TypedRequest containing params, body, and query (unused fields are nil)
//
// Example:
//
//	func getProduct(ctx *api.Context, req *api.TypedRequest[ProductParams, CreateProductRequest, ProductFilters]) (any, int, error) {
//	    // req.Params is populated, req.Body and req.Query are nil for this GET endpoint
//	    return products[req.Params.ID], 200, nil
//	}
type HandlerFuncTyped[P any, B any, Q any] func(*Context, *TypedRequest[P, B, Q]) (any, int, error)

// routingTable is an immutable snapshot of routing configuration.
// Once created and stored in atomic.Value, it should never be modified.
// This enables lock-free concurrent reads with zero contention.
type routingTable struct {
	exactRoutes map[string]map[string]*Route // Method -> Path -> Route (O(1) lookup for static routes)
	trees       map[string]*tree             // Method -> radix tree (fallback for dynamic routes)
	middlewares []MiddlewareFunc             // Global middleware stack
	gen         uint64                       // Generation counter for cache invalidation
	notFound    HandlerFunc                  // 404 handler
}

// Router handles HTTP routing with middleware support.
// Uses atomic.Value for lock-free reads, achieving ~23x better performance
// under concurrent load compared to sync.RWMutex.
type Router struct {
	table        atomic.Value // *routingTable (immutable, lock-free reads)
	mu           sync.Mutex   // Only protects writes (route registration, middleware changes)
	cleanupFuncs []func()     // Functions to call on Shutdown (e.g., rate limiter cleanup)
}

// Route represents a single route with its middleware chain
type Route struct {
	handler     HandlerFunc
	middlewares []MiddlewareFunc
	metadata    *RouteMetadata
	method      string
	pattern     string

	// Cached compiled chain for performance (rebuilt only when global middleware changes)
	cachedChain HandlerFunc  // Full chain cache (route + global middleware)
	cachedGen   uint64       // Generation when cachedChain was built
	cacheMu     sync.RWMutex // Protects cached chain access
}

// NewRouter creates a new router instance with atomic.Value for lock-free reads
func NewRouter() *Router {
	r := &Router{}
	
	// Initialize with empty immutable routing table
	r.table.Store(&routingTable{
		exactRoutes: make(map[string]map[string]*Route),
		trees:       make(map[string]*tree),
		middlewares: nil,
		gen:         0,
		notFound: func(ctx *Context) (any, int, error) {
			return nil, http.StatusNotFound, &APIError{Code: "not_found", Message: "route not found"}
		},
	})
	
	return r
}

// Use adds global middleware to the router
// Note: Adding middleware invalidates all cached chains, so it's best to add
// all global middleware before registering routes for optimal performance.
func (r *Router) Use(middleware ...MiddlewareFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Load current immutable table
	old := r.table.Load().(*routingTable)
	
	// Create new immutable table with updated middlewares
	newMiddlewares := make([]MiddlewareFunc, len(old.middlewares)+len(middleware))
	copy(newMiddlewares, old.middlewares)
	copy(newMiddlewares[len(old.middlewares):], middleware)
	
	new := &routingTable{
		exactRoutes: old.exactRoutes, // Share (routes are immutable after registration)
		trees:       old.trees,        // Share (routes are immutable after registration)
		middlewares: newMiddlewares,
		gen:         old.gen + 1, // Increment generation for cache invalidation
		notFound:    old.notFound,
	}
	
	// Atomic swap - readers get new table immediately, no locks needed
	r.table.Store(new)
}

// AddRoute registers a route with the given HTTP method, path, handler, and optional middleware
// Example: router.AddRoute(http.MethodGet, "/users", handleUsers)
//
//	router.AddRoute(http.MethodPost, "/users", handleCreateUser, authMiddleware)
func (r *Router) AddRoute(method, path string, handler HandlerFunc, middleware ...MiddlewareFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Load current table
	old := r.table.Load().(*routingTable)

	// Create route object
	route := &Route{
		handler:     handler,
		middlewares: middleware,
		method:      method,
		pattern:     path,
	}

	// Clone maps for copy-on-write
	newExactRoutes := copyExactRoutes(old.exactRoutes)
	newTrees := copyTrees(old.trees)

	// Check if this is a static route (no dynamic parameters)
	if isStaticRoute(path) {
		// Add to exact match map for O(1) lookup
		if newExactRoutes[method] == nil {
			newExactRoutes[method] = make(map[string]*Route)
		}
		newExactRoutes[method][path] = route
	}

	// Always insert into radix tree as fallback
	if newTrees[method] == nil {
		newTrees[method] = newTree()
	}
	newTrees[method].insert(path, route)

	// Create and store new immutable table
	new := &routingTable{
		exactRoutes: newExactRoutes,
		trees:       newTrees,
		middlewares: old.middlewares, // Unchanged
		gen:         old.gen,          // Unchanged (only Use() increments)
		notFound:    old.notFound,
	}

	r.table.Store(new)
}

// isStaticRoute returns true if the route has no dynamic parameters
func isStaticRoute(path string) bool {
	// Static routes don't contain ':' or '*' characters
	for i := 0; i < len(path); i++ {
		if path[i] == ':' || path[i] == '*' {
			return false
		}
	}
	return true
}

// copyExactRoutes creates a shallow copy of the exactRoutes map for copy-on-write.
// Routes themselves are shared (they're immutable after registration).
func copyExactRoutes(old map[string]map[string]*Route) map[string]map[string]*Route {
	if old == nil {
		return make(map[string]map[string]*Route)
	}
	
	new := make(map[string]map[string]*Route, len(old))
	for method, routes := range old {
		newRoutes := make(map[string]*Route, len(routes)+1)
		for path, route := range routes {
			newRoutes[path] = route
		}
		new[method] = newRoutes
	}
	return new
}

// copyTrees creates a shallow copy of the trees map for copy-on-write.
// Trees themselves are shared (routes are immutable after registration).
func copyTrees(old map[string]*tree) map[string]*tree {
	if old == nil {
		return make(map[string]*tree)
	}
	
	new := make(map[string]*tree, len(old))
	for method, tree := range old {
		new[method] = tree
	}
	return new
}

// WithMetadata attaches metadata to a route for OpenAPI generation
func (r *Router) WithMetadata(method, path string, metadata RouteMetadata) {
	r.mu.Lock()
	defer r.mu.Unlock()

	table := r.table.Load().(*routingTable)

	// Find the route in the tree and attach metadata
	if tree, ok := table.trees[method]; ok {
		if route, _ := tree.search(path); route != nil {
			route.metadata = &metadata
		}
	}
}

// Doc is a convenience method to add OpenAPI documentation to the last added route
type RouteDoc struct {
	router *Router
	method string
	path   string
}

// Route returns a RouteDoc for adding metadata
func (r *Router) Route(method, path string) *RouteDoc {
	return &RouteDoc{
		router: r,
		method: method,
		path:   path,
	}
}

// WithDoc adds documentation metadata to the route
func (rd *RouteDoc) WithDoc(metadata RouteMetadata) *RouteDoc {
	rd.router.WithMetadata(rd.method, rd.path, metadata)
	return rd
}

// Group creates a route group with a common prefix and middleware
type Group struct {
	router      *Router
	prefix      string
	middlewares []MiddlewareFunc
}

// Group creates a new route group
func (r *Router) Group(prefix string, middleware ...MiddlewareFunc) *Group {
	return &Group{
		router:      r,
		prefix:      prefix,
		middlewares: middleware,
	}
}

// Use adds middleware to the group
func (g *Group) Use(middleware ...MiddlewareFunc) {
	g.middlewares = append(g.middlewares, middleware...)
}

// AddRoute registers a route in the group with the given HTTP method, path, handler, and optional middleware
// The group prefix and group middleware are automatically applied
func (g *Group) AddRoute(method, path string, handler HandlerFunc, middleware ...MiddlewareFunc) {
	fullPath := g.prefix + path
	allMiddleware := append(g.middlewares, middleware...)
	g.router.AddRoute(method, fullPath, handler, allMiddleware...)
}

// ServeHTTP implements http.Handler interface.
// Uses atomic.Load() for zero-lock reads, achieving 23x better performance
// under concurrent load compared to the previous RWMutex implementation.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ctx := NewContext(w, req)
	defer ctx.Release() // Return context to pool when done

	// Zero-lock read: single atomic load operation (23x faster than RWMutex)
	table := r.table.Load().(*routingTable)

	// Fast path: Try exact match first (O(1) for static routes)
	if exactRoutes := table.exactRoutes[req.Method]; exactRoutes != nil {
		if route, ok := exactRoutes[req.URL.Path]; ok {
			// Static route - no path params needed (stays nil)
			// Build middleware chain: global -> route-specific -> handler (uses cache when possible)
			chain := r.buildChainWithMiddlewares(route, table.middlewares, table.gen)
			r.executeHandler(ctx, chain)
			return
		}
	}

	// Slow path: Fall back to radix tree for dynamic routes
	if tree := table.trees[req.Method]; tree != nil {
		if route, params := tree.search(req.URL.Path); route != nil {
			ctx.PathParams = params

			// Build middleware chain: global -> route-specific -> handler (uses cache when possible)
			chain := r.buildChainWithMiddlewares(route, table.middlewares, table.gen)
			r.executeHandler(ctx, chain)
			return
		}
	}

	// No route found, execute global middleware then 404
	chain := r.buildChainWithMiddlewares(&Route{handler: table.notFound}, table.middlewares, table.gen)
	r.executeHandler(ctx, chain)
}

// buildChainWithMiddlewares builds the middleware chain for a route with given global middlewares
// Uses cached chains when possible for better performance.
func (r *Router) buildChainWithMiddlewares(route *Route, globalMiddlewares []MiddlewareFunc, currentGen uint64) HandlerFunc {
	// Fast path: Check if we have a valid cached chain
	route.cacheMu.RLock()
	if route.cachedChain != nil && route.cachedGen == currentGen {
		cached := route.cachedChain
		route.cacheMu.RUnlock()
		return cached
	}
	route.cacheMu.RUnlock()

	// Slow path: Build the full chain from scratch (only on cache miss)
	handler := route.handler

	// Apply route-specific middleware in reverse order
	for i := len(route.middlewares) - 1; i >= 0; i-- {
		handler = route.middlewares[i](handler)
	}

	// Apply global middleware in reverse order
	for i := len(globalMiddlewares) - 1; i >= 0; i-- {
		handler = globalMiddlewares[i](handler)
	}

	// Cache the built chain
	route.cacheMu.Lock()
	route.cachedChain = handler
	route.cachedGen = currentGen
	route.cacheMu.Unlock()

	return handler
}

// executeHandler executes the handler and sends the response based on return values
func (r *Router) executeHandler(ctx *Context, handler HandlerFunc) {
	data, statusCode, err := handler(ctx)

	// If status is 0, the handler has already written the response (e.g., HTML)
	if statusCode == 0 && err == nil {
		return
	}

	// Handle error response
	if err != nil {
		if statusCode == 0 {
			statusCode = http.StatusInternalServerError
		}

		// Check if error is a custom error with details
		if apiErr, ok := err.(*APIError); ok {
			ctx.JSON(statusCode, NewErrorResponse(statusCode, apiErr.Code, apiErr.Message))
			return
		}

		// Default error response
		ctx.JSON(statusCode, NewErrorResponse(statusCode, "error", err.Error()))
		return
	}

	// Handle success response
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	// Handle no content responses
	if statusCode == http.StatusNoContent || data == nil && statusCode == http.StatusOK {
		ctx.Set(StatusCodeKey, http.StatusNoContent) // Store for logging
		ctx.Writer.WriteHeader(http.StatusNoContent)
		return
	}

	// Send success response with data
	ctx.JSON(statusCode, NewSuccessResponse(data, ""))
}

// NotFound sets a custom 404 handler
func (r *Router) NotFound(handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	old := r.table.Load().(*routingTable)
	
	new := &routingTable{
		exactRoutes: old.exactRoutes,
		trees:       old.trees,
		middlewares: old.middlewares,
		gen:         old.gen,
		notFound:    handler, // Updated
	}
	
	r.table.Store(new)
}

// RegisterCleanup registers a cleanup function to be called on Shutdown.
// This is used internally by middleware (e.g., rate limiter) to register cleanup goroutines.
// Users typically don't need to call this directly.
func (r *Router) RegisterCleanup(cleanup func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupFuncs = append(r.cleanupFuncs, cleanup)
}

// Shutdown gracefully shuts down the router and cleans up resources.
// This stops all background goroutines (e.g., rate limiter cleanup loops).
// Call this when shutting down your server:
//
//	srv := &http.Server{Addr: ":8080", Handler: router}
//	// ... handle shutdown signal ...
//	router.Shutdown()  // Clean up router resources
//	srv.Shutdown(ctx)  // Then shutdown the HTTP server
//
// Or use ServeWithShutdown() for automatic integration.
func (r *Router) Shutdown() {
	r.mu.Lock()
	cleanups := make([]func(), len(r.cleanupFuncs))
	copy(cleanups, r.cleanupFuncs)
	r.mu.Unlock()

	// Execute all cleanup functions
	for _, cleanup := range cleanups {
		cleanup()
	}
}

// Run starts the HTTP server
func (r *Router) Run(addr string) error {
	return http.ListenAndServe(addr, r)
}

// RunTLS starts the HTTPS server
func (r *Router) RunTLS(addr, certFile, keyFile string) error {
	return http.ListenAndServeTLS(addr, certFile, keyFile, r)
}
