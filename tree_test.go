package nimbus

import (
	"fmt"
	"testing"
)

func TestTree_InsertAndSearch_StaticRoutes(t *testing.T) {
	tree := newTree()

	route1 := &Route{pattern: "/users"}
	route2 := &Route{pattern: "/products"}
	route3 := &Route{pattern: "/api/v1/health"}

	tree.insert("/users", route1)
	tree.insert("/products", route2)
	tree.insert("/api/v1/health", route3)

	tests := []struct {
		path     string
		expected *Route
	}{
		{"/users", route1},
		{"/products", route2},
		{"/api/v1/health", route3},
		{"/notfound", nil},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			found, _ := tree.search(tt.path)
			if found != tt.expected {
				t.Errorf("Expected route %v, got %v", tt.expected, found)
			}
		})
	}
}

func TestTree_InsertAndSearch_DynamicRoutes(t *testing.T) {
	tree := newTree()

	route1 := &Route{pattern: "/users/:id"}
	route2 := &Route{pattern: "/users/:id/posts"}
	route3 := &Route{pattern: "/users/:id/posts/:postId"}

	tree.insert("/users/:id", route1)
	tree.insert("/users/:id/posts", route2)
	tree.insert("/users/:id/posts/:postId", route3)

	tests := []struct {
		path           string
		expectedRoute  *Route
		expectedParams map[string]string
	}{
		{
			path:           "/users/123",
			expectedRoute:  route1,
			expectedParams: map[string]string{"id": "123"},
		},
		{
			path:           "/users/456/posts",
			expectedRoute:  route2,
			expectedParams: map[string]string{"id": "456"},
		},
		{
			path:           "/users/789/posts/999",
			expectedRoute:  route3,
			expectedParams: map[string]string{"id": "789", "postId": "999"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			found, params := tree.search(tt.path)

			if found != tt.expectedRoute {
				t.Errorf("Expected route %v, got %v", tt.expectedRoute, found)
			}

			if len(params) != len(tt.expectedParams) {
				t.Errorf("Expected %d params, got %d", len(tt.expectedParams), len(params))
			}

			for key, expectedValue := range tt.expectedParams {
				if actualValue, ok := params[key]; !ok || actualValue != expectedValue {
					t.Errorf("Expected param %s=%s, got %s", key, expectedValue, actualValue)
				}
			}
		})
	}
}

func TestTree_InsertAndSearch_MixedRoutes(t *testing.T) {
	tree := newTree()

	staticRoute := &Route{pattern: "/users/new"}
	dynamicRoute := &Route{pattern: "/users/:id"}

	// Static route should take precedence over dynamic
	tree.insert("/users/:id", dynamicRoute)
	tree.insert("/users/new", staticRoute)

	tests := []struct {
		path          string
		expectedRoute *Route
	}{
		{"/users/new", staticRoute},  // Should match static, not dynamic
		{"/users/123", dynamicRoute}, // Should match dynamic
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			found, _ := tree.search(tt.path)
			if found != tt.expectedRoute {
				t.Errorf("Expected route %v, got %v for path %s", tt.expectedRoute, found, tt.path)
			}
		})
	}
}

func TestTree_RootPath(t *testing.T) {
	tree := newTree()
	rootRoute := &Route{pattern: "/"}

	tree.insert("/", rootRoute)

	found, _ := tree.search("/")
	if found != rootRoute {
		t.Errorf("Expected root route, got %v", found)
	}
}

func TestTree_TrailingSlash(t *testing.T) {
	tree := newTree()
	route := &Route{pattern: "/users"}

	tree.insert("/users", route)

	// Should match without trailing slash
	found, _ := tree.search("/users")
	if found != route {
		t.Error("Expected to find route for /users")
	}
}

func TestTree_ComplexPaths(t *testing.T) {
	tree := newTree()

	routes := map[string]*Route{
		"/api/v1/users":                         {pattern: "/api/v1/users"},
		"/api/v1/users/:id":                     {pattern: "/api/v1/users/:id"},
		"/api/v1/users/:id/posts":               {pattern: "/api/v1/users/:id/posts"},
		"/api/v1/users/:id/posts/:postId":       {pattern: "/api/v1/users/:id/posts/:postId"},
		"/api/v1/users/:id/posts/:postId/likes": {pattern: "/api/v1/users/:id/posts/:postId/likes"},
		"/api/v2/products":                      {pattern: "/api/v2/products"},
		"/api/v2/products/:id":                  {pattern: "/api/v2/products/:id"},
	}

	// Insert all routes
	for path, route := range routes {
		tree.insert(path, route)
	}

	// Test all routes can be found
	for path, expectedRoute := range routes {
		found, _ := tree.search(path)
		if found != expectedRoute {
			t.Errorf("Failed to find route for %s", path)
		}
	}

	// Test dynamic paths
	found, params := tree.search("/api/v1/users/123/posts/456/likes")
	if found == nil {
		t.Error("Expected to find route")
	}
	if params["id"] != "123" || params["postId"] != "456" {
		t.Errorf("Incorrect params: %v", params)
	}
}

func TestTree_CommonPrefixes(t *testing.T) {
	tree := newTree()

	route1 := &Route{pattern: "/user"}
	route2 := &Route{pattern: "/users"}
	route3 := &Route{pattern: "/users/admin"}

	tree.insert("/user", route1)
	tree.insert("/users", route2)
	tree.insert("/users/admin", route3)

	tests := []struct {
		path     string
		expected *Route
	}{
		{"/user", route1},
		{"/users", route2},
		{"/users/admin", route3},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			found, _ := tree.search(tt.path)
			if found != tt.expected {
				t.Errorf("Expected route %v for path %s, got %v", tt.expected, tt.path, found)
			}
		})
	}
}

func TestTree_NoMatch(t *testing.T) {
	tree := newTree()

	tree.insert("/users", &Route{pattern: "/users"})
	tree.insert("/products", &Route{pattern: "/products"})

	found, _ := tree.search("/orders")
	if found != nil {
		t.Error("Expected no match for /orders")
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"abc", "abcd", 3},
		{"abcd", "abc", 3},
		{"test", "test", 4},
		{"test", "different", 0},
		{"", "test", 0},
		{"test", "", 0},
		{"a", "a", 1},
	}

	for _, tt := range tests {
		result := longestCommonPrefix(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("longestCommonPrefix(%q, %q) = %d, expected %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

// Benchmark radix tree performance
func BenchmarkTree_Insert(b *testing.B) {
	paths := []string{
		"/",
		"/users",
		"/users/:id",
		"/users/:id/posts",
		"/users/:id/posts/:postId",
		"/products",
		"/products/:id",
		"/api/v1/health",
		"/api/v1/users",
		"/api/v1/users/:id",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree := newTree()
		for _, path := range paths {
			tree.insert(path, &Route{pattern: path})
		}
	}
}

func BenchmarkTree_Search_Static(b *testing.B) {
	tree := newTree()
	tree.insert("/users", &Route{pattern: "/users"})
	tree.insert("/products", &Route{pattern: "/products"})
	tree.insert("/api/v1/health", &Route{pattern: "/api/v1/health"})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tree.search("/users")
	}
}

func BenchmarkTree_Search_Dynamic(b *testing.B) {
	tree := newTree()
	tree.insert("/users/:id", &Route{pattern: "/users/:id"})
	tree.insert("/users/:id/posts", &Route{pattern: "/users/:id/posts"})
	tree.insert("/users/:id/posts/:postId", &Route{pattern: "/users/:id/posts/:postId"})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tree.search("/users/123/posts/456")
	}
}

func BenchmarkTree_Search_ManyRoutes(b *testing.B) {
	tree := newTree()

	// Insert 100 routes
	for i := 0; i < 100; i++ {
		path := "/route" + string(rune(i))
		tree.insert(path, &Route{pattern: path})
	}

	// Also add some dynamic routes
	tree.insert("/users/:id", &Route{pattern: "/users/:id"})
	tree.insert("/products/:id", &Route{pattern: "/products/:id"})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tree.search("/users/123")
	}
}

// BenchmarkTree_Clone benchmarks full tree cloning (used for baseline comparison)
func BenchmarkTree_Clone(b *testing.B) {
	tree := newTree()
	
	// Build a realistic tree with 100 routes
	for i := 0; i < 100; i++ {
		path := fmt.Sprintf("/api/v1/resource%d/:id/action/:action", i)
		route := &Route{
			handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
			method:  "GET",
			pattern: path,
		}
		tree.insert(path, route)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = tree.clone() // Full deep copy
	}
}

// BenchmarkTree_InsertWithCopy benchmarks path copying optimization
func BenchmarkTree_InsertWithCopy(b *testing.B) {
	tree := newTree()
	
	// Build a realistic tree with 100 routes
	for i := 0; i < 100; i++ {
		path := fmt.Sprintf("/api/v1/resource%d/:id/action/:action", i)
		route := &Route{
			handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
			method:  "GET",
			pattern: path,
		}
		tree.insert(path, route)
	}
	
	// New route to insert
	newRoute := &Route{
		handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
		method:  "POST",
		pattern: "/api/v1/newresource/:id/action/:action",
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = tree.insertWithCopy("/api/v1/newresource/:id/action/:action", newRoute)
	}
}

// BenchmarkTree_Clone_SmallTree benchmarks cloning with 10 routes
func BenchmarkTree_Clone_SmallTree(b *testing.B) {
	tree := newTree()
	
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/api/resource%d/:id", i)
		route := &Route{
			handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
			method:  "GET",
			pattern: path,
		}
		tree.insert(path, route)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = tree.clone()
	}
}

// BenchmarkTree_InsertWithCopy_SmallTree benchmarks path copy with 10 routes
func BenchmarkTree_InsertWithCopy_SmallTree(b *testing.B) {
	tree := newTree()
	
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/api/resource%d/:id", i)
		route := &Route{
			handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
			method:  "GET",
			pattern: path,
		}
		tree.insert(path, route)
	}
	
	newRoute := &Route{
		handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
		method:  "POST",
		pattern: "/api/newresource/:id",
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = tree.insertWithCopy("/api/newresource/:id", newRoute)
	}
}

// BenchmarkTree_Clone_LargeTree benchmarks cloning with 500 routes
func BenchmarkTree_Clone_LargeTree(b *testing.B) {
	tree := newTree()
	
	for i := 0; i < 500; i++ {
		path := fmt.Sprintf("/api/v1/resource%d/:id/action/:action/detail/:detail", i)
		route := &Route{
			handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
			method:  "GET",
			pattern: path,
		}
		tree.insert(path, route)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = tree.clone()
	}
}

// BenchmarkTree_InsertWithCopy_LargeTree benchmarks path copy with 500 routes
func BenchmarkTree_InsertWithCopy_LargeTree(b *testing.B) {
	tree := newTree()
	
	for i := 0; i < 500; i++ {
		path := fmt.Sprintf("/api/v1/resource%d/:id/action/:action/detail/:detail", i)
		route := &Route{
			handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
			method:  "GET",
			pattern: path,
		}
		tree.insert(path, route)
	}
	
	newRoute := &Route{
		handler: func(ctx *Context) (any, int, error) { return nil, 200, nil },
		method:  "POST",
		pattern: "/api/v1/newresource/:id/action/:action/detail/:detail",
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = tree.insertWithCopy("/api/v1/newresource/:id/action/:action/detail/:detail", newRoute)
	}
}
