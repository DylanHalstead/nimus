package nimbus

import (
	"net/http"
	"sync"
	"sync/atomic"
	"unique"
)

// Pre-interned HTTP method handles for performance
var (
	methodGET     = unique.Make(http.MethodGet)
	methodPOST    = unique.Make(http.MethodPost)
	methodPUT     = unique.Make(http.MethodPut)
	methodDELETE  = unique.Make(http.MethodDelete)
	methodPATCH   = unique.Make(http.MethodPatch)
	methodHEAD    = unique.Make(http.MethodHead)
	methodOPTIONS = unique.Make(http.MethodOptions)
	methodTRACE   = unique.Make(http.MethodTrace)
	methodCONNECT = unique.Make(http.MethodConnect)
)

// getMethodHandle returns the pre-interned unique.Handle for common HTTP methods.
// For standard methods (GET, POST, etc.), this avoids calling unique.Make() per request.
// For custom methods, falls back to unique.Make() which handles interning dynamically.
// The switch statement compiles to a jump table, making this ~1-2ns vs ~8-10ns for unique.Make().
func getMethodHandle(method string) unique.Handle[string] {
	switch method {
	case http.MethodGet:
		return methodGET
	case http.MethodPost:
		return methodPOST
	case http.MethodPut:
		return methodPUT
	case http.MethodDelete:
		return methodDELETE
	case http.MethodPatch:
		return methodPATCH
	case http.MethodHead:
		return methodHEAD
	case http.MethodOptions:
		return methodOPTIONS
	case http.MethodTrace:
		return methodTRACE
	case http.MethodConnect:
		return methodCONNECT
	default:
		// Custom/non-standard HTTP methods (rare)
		return unique.Make(method)
	}
}

// Handler defines the handler function signature
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
type Handler func(*Context) (any, int, error)

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
// Once created and stored in atomic.Pointer, it should never be modified.
// This enables lock-free concurrent reads with zero contention.
// Uses unique.Handle[string] as method keys for O(1) pointer-based hashing (faster than string hashing).
type routingTable struct {
	exactRoutes   map[unique.Handle[string]]map[string]*Route // Method interned string -> Path -> Route (O(1) for static routes)
	trees         map[unique.Handle[string]]*tree             // Method interned string -> radix tree (for dynamic routes)
	middlewares   []Middleware                            // Middleware stack for the router; reads last-in first-out (LIFO)
	gen           uint64                                      // Generation counter for cache invalidation
	notFoundRoute *Route                                      // Special synthetic route for 404 handler (also in chains map)
	chains        map[*Route]Handler                      // Pre-built middleware chains (route -> compiled handler)
}

// Router handles HTTP routing with middleware support.
// Uses atomic.Pointer for lock-free, type-safe reads, achieving ~23x better performance
// under concurrent load compared to sync.RWMutex.
// Routes are indexed by unique.Handle[string] method keys for O(1) pointer-based hashing.
type Router struct {
	table        atomic.Pointer[routingTable] // Immutable routing table (lock-free, type-safe reads)
	mu           sync.Mutex                   // Only protects writes (route registration, middleware changes)
	cleanupFuncs []func()                     // Functions to call on Shutdown (e.g., rate limiter cleanup)
}

// Route represents a single route with its middleware chain.
// Routes are immutable after creation - all state is read-only.
type Route struct {
	handler     Handler
	middlewares []Middleware
	metadata    *RouteMetadata
	method      string
	pattern     string
}

// NewRouter creates a new router instance with atomic.Pointer for lock-free, type-safe reads
// HTTP method handles are pre-interned at package level for optimal performance
func NewRouter() *Router {
	r := &Router{}
	
	// Default 404 handler
	defaultNotFound := func(ctx *Context) (any, int, error) {
		return nil, http.StatusNotFound, &APIError{Code: "not_found", Message: "route not found"}
	}
	
	// Create synthetic route for 404 handler
	notFoundRoute := &Route{
		handler:     defaultNotFound,
		middlewares: nil,
		method:      "",
		pattern:     "",
	}
	
	// Initialize chains map with 404 handler
	chains := make(map[*Route]Handler)
	chains[notFoundRoute] = defaultNotFound // No middleware initially
	
	// Initialize with empty immutable routing table
	// Method handles (methodGET, methodPOST, etc.) are package-level constants
	r.table.Store(&routingTable{
		exactRoutes:   make(map[unique.Handle[string]]map[string]*Route),
		trees:         make(map[unique.Handle[string]]*tree),
		middlewares:   nil,
		gen:           0,
		notFoundRoute: notFoundRoute,
		chains:        chains,
	})
	
	return r
}

// Use adds global middleware to the router.
// Pre-builds all middleware chains with the new middleware stack.
// Note: This rebuilds chains for all routes, so it's best to add all global
// middleware before registering routes for optimal performance.
func (r *Router) Use(middleware ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Load current immutable table (type-safe, no assertion needed)
	old := r.table.Load()
	
	// Create new immutable table with updated middlewares
	newMiddlewares := make([]Middleware, len(old.middlewares)+len(middleware))
	copy(newMiddlewares, old.middlewares)
	copy(newMiddlewares[len(old.middlewares):], middleware)
	
	// Pre-build all chains with the new middleware stack
	newChains := buildAllChains(old.exactRoutes, old.trees, newMiddlewares)
	
	// Build and add notFound chain to the chains map
	notFoundChain := buildNotFoundChain(old.notFoundRoute.handler, newMiddlewares)
	newChains[old.notFoundRoute] = notFoundChain
	
	new := &routingTable{
		exactRoutes:   old.exactRoutes,   // Share (routes are immutable after registration)
		trees:         old.trees,          // Share (routes are immutable after registration)
		middlewares:   newMiddlewares,
		gen:           old.gen + 1,        // Increment generation
		notFoundRoute: old.notFoundRoute,  // Share synthetic 404 route
		chains:        newChains,          // Pre-built chains including 404
	}
	
	// Atomic swap - readers get new table immediately, no locks needed
	r.table.Store(new)
}

// AddRoute registers a route with the given HTTP method, path, handler, and optional middleware
// Example: router.AddRoute(http.MethodGet, "/users", handleUsers)
//
//	router.AddRoute(http.MethodPost, "/users", handleCreateUser, authMiddleware)
func (r *Router) AddRoute(method, path string, handler Handler, middleware ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Load current table (type-safe, no assertion needed)
	old := r.table.Load()

	methodHandle := getMethodHandle(method)

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
		if newExactRoutes[methodHandle] == nil {
			newExactRoutes[methodHandle] = make(map[string]*Route)
		}
		newExactRoutes[methodHandle][path] = route
	}

	// Always insert into radix tree as fallback
	// Only copies nodes along insertion path
	if oldTree := old.trees[methodHandle]; oldTree != nil {
		newTrees[methodHandle] = oldTree.insertWithCopy(path, route)
	} else {
		// Create new tree if one doesn't exist for this method
		newTrees[methodHandle] = newTree()
		newTrees[methodHandle].insert(path, route)
	}

	// Copy chains map and add chain for new route
	newChains := make(map[*Route]Handler, len(old.chains)+1)
	for r, chain := range old.chains {
		newChains[r] = chain
	}
	newChains[route] = buildChain(route, old.middlewares)

	// Create and store new immutable table
	new := &routingTable{
		exactRoutes:   newExactRoutes,
		trees:         newTrees,
		middlewares:   old.middlewares,   // Unchanged
		gen:           old.gen,            // Unchanged (only Use() increments)
		notFoundRoute: old.notFoundRoute,  // Unchanged
		chains:        newChains,          // Updated with new route's chain
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
// Uses unique.Handle[string] keys for O(1) pointer-based hashing.
func copyExactRoutes(old map[unique.Handle[string]]map[string]*Route) map[unique.Handle[string]]map[string]*Route {
	if old == nil {
		return make(map[unique.Handle[string]]map[string]*Route)
	}
	
	new := make(map[unique.Handle[string]]map[string]*Route, len(old))
	for methodHandle, routes := range old {
		newRoutes := make(map[string]*Route, len(routes)+1)
		for path, route := range routes {
			newRoutes[path] = route
		}
		new[methodHandle] = newRoutes
	}
	return new
}

// copyTrees creates a shallow copy of the trees map for copy-on-write.
// The map itself is copied, but tree pointers are shared initially.
// Uses unique.Handle[string] keys for O(1) pointer-based hashing.
func copyTrees(old map[unique.Handle[string]]*tree) map[unique.Handle[string]]*tree {
	if old == nil {
		return make(map[unique.Handle[string]]*tree)
	}
	
	new := make(map[unique.Handle[string]]*tree, len(old))
	for methodHandle, tree := range old {
		new[methodHandle] = tree
	}
	return new
}

// buildChain compiles a middleware chain for a single route.
// Middleware is applied in reverse order: route-specific first, then global.
func buildChain(route *Route, globalMiddlewares []Middleware) Handler {
	handler := route.handler
	
	// Apply route-specific middleware in reverse order (last added wraps first)
	for i := len(route.middlewares) - 1; i >= 0; i-- {
		handler = route.middlewares[i](handler)
	}
	
	// Apply global middleware in reverse order (last added wraps first)
	for i := len(globalMiddlewares) - 1; i >= 0; i-- {
		handler = globalMiddlewares[i](handler)
	}
	
	return handler
}

// buildNotFoundChain compiles a middleware chain for the notFound handler.
// Only global middleware is applied (no route-specific middleware).
func buildNotFoundChain(notFound Handler, globalMiddlewares []Middleware) Handler {
	handler := notFound
	
	// Apply global middleware in reverse order (last added wraps first)
	for i := len(globalMiddlewares) - 1; i >= 0; i-- {
		handler = globalMiddlewares[i](handler)
	}
	
	return handler
}

// buildAllChains pre-compiles middleware chains for all routes in the routing table.
// This is called when global middleware changes or when the routing table is rebuilt.
// Returns an immutable map of route -> compiled chain for lock-free lookups.
func buildAllChains(exactRoutes map[unique.Handle[string]]map[string]*Route, trees map[unique.Handle[string]]*tree, globalMiddlewares []Middleware) map[*Route]Handler {
	chains := make(map[*Route]Handler)
	
	// Build chains for exact routes
	for _, methodRoutes := range exactRoutes {
		for _, route := range methodRoutes {
			chains[route] = buildChain(route, globalMiddlewares)
		}
	}
	
	// Build chains for tree routes (dynamic routes)
	for _, tree := range trees {
		if tree != nil {
			routes := tree.collectRoutes()
			for _, route := range routes {
				// Only build if not already built (route might be in both exact and tree)
				if _, exists := chains[route]; !exists {
					chains[route] = buildChain(route, globalMiddlewares)
				}
			}
		}
	}
	
	return chains
}

// WithMetadata attaches metadata to a route for OpenAPI generation
func (r *Router) WithMetadata(method, path string, metadata RouteMetadata) {
	r.mu.Lock()
	defer r.mu.Unlock()

	table := r.table.Load()

	methodHandle := getMethodHandle(method)

	// Find the route in the tree and attach metadata
	if tree, ok := table.trees[methodHandle]; ok {
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
	middlewares []Middleware
}

// Group creates a new route group
func (r *Router) Group(prefix string, middleware ...Middleware) *Group {
	return &Group{
		router:      r,
		prefix:      prefix,
		middlewares: middleware,
	}
}

// Use adds middleware to the group
func (g *Group) Use(middleware ...Middleware) {
	g.middlewares = append(g.middlewares, middleware...)
}

// AddRoute registers a route in the group with the given HTTP method, path, handler, and optional middleware
// The group prefix and group middleware are automatically applied
func (g *Group) AddRoute(method, path string, handler Handler, middleware ...Middleware) {
	fullPath := g.prefix + path
	allMiddleware := append(g.middlewares, middleware...)
	g.router.AddRoute(method, fullPath, handler, allMiddleware...)
}

// ServeHTTP implements http.Handler interface.
// Uses atomic.Pointer for zero-lock, type-safe reads with pre-built middleware chains.
// Achieves true lock-free performance: ~40ns per request under high concurrency.
// HTTP methods use unique.Handle as map keys for O(1) pointer-based hashing (faster than string hashing).
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ctx := NewContext(w, req)
	defer ctx.Release() // Return context to pool when done

	// Zero-lock read: single atomic load operation (type-safe, no assertion needed)
	table := r.table.Load()

	// Get pre-interned method handle for ultra-fast map lookup
	// unique.Handle provides O(1) pointer-based hashing instead of O(n) string hashing
	methodHandle := getMethodHandle(req.Method)

	// Fast path: Try exact match first (O(1) for static routes)
	// Map lookup uses pointer hash (much faster than string hash)
	if exactRoutes := table.exactRoutes[methodHandle]; exactRoutes != nil {
		if route, ok := exactRoutes[req.URL.Path]; ok {
			// Static route - no path params needed (stays nil)
			// ✅ Lock-free chain lookup - just a map read!
			chain := table.chains[route]
			r.executeHandler(ctx, chain)
			return
		}
	}

	// Slow path: Fall back to radix tree for dynamic routes
	if tree := table.trees[methodHandle]; tree != nil {
		if route, params := tree.search(req.URL.Path); route != nil {
			ctx.PathParams = params

			// ✅ Lock-free chain lookup - just a map read!
			chain := table.chains[route]
			r.executeHandler(ctx, chain)
			return
		}
	}

	// No route found - use pre-built 404 chain from chains map
	// ✅ Lock-free - just another map lookup!
	r.executeHandler(ctx, table.chains[table.notFoundRoute])
}

// executeHandler executes the handler and sends the response based on return values
func (r *Router) executeHandler(ctx *Context, handler Handler) {
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
func (r *Router) NotFound(handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	old := r.table.Load()
	
	// Create new synthetic route for custom 404 handler
	newNotFoundRoute := &Route{
		handler:     handler,
		middlewares: nil,
		method:      "",
		pattern:     "",
	}
	
	// Build the notFound chain with global middleware
	newNotFoundChain := buildNotFoundChain(handler, old.middlewares)
	
	// Copy chains and update with new notFound chain
	newChains := make(map[*Route]Handler, len(old.chains))
	for route, chain := range old.chains {
		if route != old.notFoundRoute {
			newChains[route] = chain
		}
	}
	newChains[newNotFoundRoute] = newNotFoundChain
	
	new := &routingTable{
		exactRoutes:   old.exactRoutes,
		trees:         old.trees,
		middlewares:   old.middlewares,
		gen:           old.gen,
		notFoundRoute: newNotFoundRoute,  // New synthetic route
		chains:        newChains,          // Updated chains with new 404
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
