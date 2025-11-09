package nimbus

import (
	"net/http"
	"testing"
)

type TestAPIUser struct {
	Name  string `json:"name" validate:"required,minlen=2"`
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age" validate:"min=18"`
}

type TestAPIQuery struct {
	Page  int    `json:"page" validate:"min=1"`
	Limit int    `json:"limit" validate:"min=1,max=100"`
	Query string `json:"query" validate:"minlen=2"`
}

func TestGenerateOpenAPI(t *testing.T) {
	router := NewRouter()
	userSchema := NewSchema(TestAPIUser{})
	querySchema := NewSchema(TestAPIQuery{})

	// Add some routes with metadata
	router.AddRoute(http.MethodGet, "/users", func(ctx *Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})
	router.Route("GET", "/users").WithDoc(RouteMetadata{
		Summary:     "List users",
		Description: "Get a list of all users",
		Tags:        []string{"users"},
		QuerySchema: querySchema,
	})

	router.AddRoute(http.MethodPost, "/users", func(ctx *Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})
	router.Route("POST", "/users").WithDoc(RouteMetadata{
		Summary:       "Create user",
		Description:   "Create a new user",
		Tags:          []string{"users"},
		RequestSchema: userSchema,
	})

	router.AddRoute(http.MethodGet, "/users/:id", func(ctx *Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})
	router.Route("GET", "/users/:id").WithDoc(RouteMetadata{
		Summary: "Get user by ID",
		Tags:    []string{"users"},
	})

	// Generate OpenAPI spec
	config := OpenAPIConfig{
		Title:       "Test API",
		Description: "A test API",
		Version:     "1.0.0",
	}

	spec := router.GenerateOpenAPI(config)

	// Verify basic structure
	if spec.OpenAPI != "3.0.3" {
		t.Errorf("Expected OpenAPI version 3.0.3, got %s", spec.OpenAPI)
	}

	if spec.Info.Title != "Test API" {
		t.Errorf("Expected title 'Test API', got '%s'", spec.Info.Title)
	}

	if spec.Info.Version != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%s'", spec.Info.Version)
	}

	// Verify paths were generated
	if len(spec.Paths) == 0 {
		t.Error("Expected paths to be generated")
	}

	// Verify /users path exists
	usersPath, ok := spec.Paths["/users"]
	if !ok {
		t.Error("Expected /users path to be present")
	}

	// Verify GET /users
	if usersPath.GET == nil {
		t.Error("Expected GET operation for /users")
	} else {
		if usersPath.GET.Summary != "List users" {
			t.Errorf("Expected summary 'List users', got '%s'", usersPath.GET.Summary)
		}
		if len(usersPath.GET.Tags) != 1 || usersPath.GET.Tags[0] != "users" {
			t.Errorf("Expected tags [users], got %v", usersPath.GET.Tags)
		}
		if len(usersPath.GET.Parameters) == 0 {
			t.Error("Expected query parameters to be present")
		}
	}

	// Verify POST /users
	if usersPath.POST == nil {
		t.Error("Expected POST operation for /users")
	} else {
		if usersPath.POST.Summary != "Create user" {
			t.Errorf("Expected summary 'Create user', got '%s'", usersPath.POST.Summary)
		}
		if usersPath.POST.RequestBody == nil {
			t.Error("Expected request body for POST /users")
		}
	}

	// Verify /users/{id} path exists (with path param conversion)
	userIDPath, ok := spec.Paths["/users/{id}"]
	if !ok {
		t.Error("Expected /users/{id} path to be present")
	}

	// Verify path parameter
	if userIDPath.GET != nil {
		foundParam := false
		for _, param := range userIDPath.GET.Parameters {
			if param.Name == "id" && param.In == "path" && param.Required {
				foundParam = true
				break
			}
		}
		if !foundParam {
			t.Error("Expected path parameter 'id' to be present and required")
		}
	}

	// Verify components/schemas were generated
	if len(spec.Components.Schemas) == 0 {
		t.Error("Expected schemas to be generated in components")
	}
}

func TestConvertPathParams(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/users/:id", "/users/{id}"},
		{"/posts/:postId/comments/:commentId", "/posts/{postId}/comments/{commentId}"},
		{"/users", "/users"},
		{"/", "/"},
	}

	for _, tt := range tests {
		result := convertPathParams(tt.input)
		if result != tt.expected {
			t.Errorf("convertPathParams(%s): expected %s, got %s", tt.input, tt.expected, result)
		}
	}
}

func TestExtractPathParams(t *testing.T) {
	tests := []struct {
		pattern  string
		expected []string
	}{
		{"/users/:id", []string{"id"}},
		{"/posts/:postId/comments/:commentId", []string{"postId", "commentId"}},
		{"/users", []string{}},
		{"/api/v1/:resource/:id", []string{"resource", "id"}},
	}

	for _, tt := range tests {
		result := extractPathParams(tt.pattern)
		if len(result) != len(tt.expected) {
			t.Errorf("extractPathParams(%s): expected %v, got %v", tt.pattern, tt.expected, result)
			continue
		}
		for i, param := range result {
			if param != tt.expected[i] {
				t.Errorf("extractPathParams(%s): expected %v, got %v", tt.pattern, tt.expected, result)
				break
			}
		}
	}
}

func TestGenerateOperationID(t *testing.T) {
	tests := []struct {
		method   string
		pattern  string
		expected string
	}{
		{"GET", "/users", "getUsers"},
		{"POST", "/users", "postUsers"},
		{"GET", "/users/:id", "getUsersById"},
		{"DELETE", "/api/posts/:id", "deleteApiPostsById"},
		{"PUT", "/users/:userId/posts/:postId", "putUsersByUserIdPostsByPostId"},
	}

	for _, tt := range tests {
		result := generateOperationID(tt.method, tt.pattern)
		if result != tt.expected {
			t.Errorf("generateOperationID(%s, %s): expected %s, got %s",
				tt.method, tt.pattern, tt.expected, result)
		}
	}
}

func TestSchemaToOpenAPISchema(t *testing.T) {
	userSchema := NewSchema(TestAPIUser{})
	openAPISchema := schemaToOpenAPISchema(userSchema)

	if openAPISchema.Type != "object" {
		t.Errorf("Expected type 'object', got '%s'", openAPISchema.Type)
	}

	// Check name field
	if nameSchema, ok := openAPISchema.Properties["name"]; ok {
		if nameSchema.Type != "string" {
			t.Errorf("Expected name type 'string', got '%s'", nameSchema.Type)
		}
		if nameSchema.MinLength == nil || *nameSchema.MinLength != 2 {
			t.Error("Expected name to have minLength of 2")
		}
	} else {
		t.Error("Expected 'name' property to be present")
	}

	// Check email field
	if emailSchema, ok := openAPISchema.Properties["email"]; ok {
		if emailSchema.Format != "email" {
			t.Errorf("Expected email format 'email', got '%s'", emailSchema.Format)
		}
	} else {
		t.Error("Expected 'email' property to be present")
	}

	// Check age field
	if ageSchema, ok := openAPISchema.Properties["age"]; ok {
		if ageSchema.Type != "integer" {
			t.Errorf("Expected age type 'integer', got '%s'", ageSchema.Type)
		}
		if ageSchema.Minimum == nil || *ageSchema.Minimum != 18 {
			t.Error("Expected age to have minimum of 18")
		}
	} else {
		t.Error("Expected 'age' property to be present")
	}

	// Check required fields
	requiredCount := 0
	for _, field := range openAPISchema.Required {
		if field == "name" || field == "email" {
			requiredCount++
		}
	}
	if requiredCount != 2 {
		t.Errorf("Expected 2 required fields (name, email), found %d", requiredCount)
	}
}

func TestSchemaToQueryParameters(t *testing.T) {
	querySchema := NewSchema(TestAPIQuery{})
	params := schemaToQueryParameters(querySchema)

	if len(params) != 3 {
		t.Errorf("Expected 3 query parameters, got %d", len(params))
	}

	// Check that all params are query params
	for _, param := range params {
		if param.In != "query" {
			t.Errorf("Expected param.In to be 'query', got '%s'", param.In)
		}
	}

	// Find and check page parameter
	foundPage := false
	for _, param := range params {
		if param.Name == "page" {
			foundPage = true
			if param.Schema.Type != "integer" {
				t.Errorf("Expected page type 'integer', got '%s'", param.Schema.Type)
			}
			if param.Schema.Minimum == nil || *param.Schema.Minimum != 1 {
				t.Error("Expected page minimum to be 1")
			}
		}
	}
	if !foundPage {
		t.Error("Expected to find 'page' parameter")
	}
}

// TestStaticAndDynamicRoutes verifies that both static routes and dynamic routes
// are properly collected in OpenAPI spec generation
func TestStaticAndDynamicRoutes(t *testing.T) {
	router := NewRouter()

	// Add static routes (stored in exactRoutes map)
	router.AddRoute(http.MethodGet, "/health", func(ctx *Context) (any, int, error) {
		return map[string]string{"status": "ok"}, http.StatusOK, nil
	})
	router.Route("GET", "/health").WithDoc(RouteMetadata{
		Summary: "Health check",
		Tags:    []string{"system"},
	})

	router.AddRoute(http.MethodGet, "/api/status", func(ctx *Context) (any, int, error) {
		return map[string]string{"status": "running"}, http.StatusOK, nil
	})
	router.Route("GET", "/api/status").WithDoc(RouteMetadata{
		Summary: "API status",
		Tags:    []string{"system"},
	})

	// Add dynamic routes (stored in trees)
	router.AddRoute(http.MethodGet, "/users/:id", func(ctx *Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})
	router.Route("GET", "/users/:id").WithDoc(RouteMetadata{
		Summary: "Get user by ID",
		Tags:    []string{"users"},
	})

	router.AddRoute(http.MethodGet, "/posts/:postId/comments/:commentId", func(ctx *Context) (any, int, error) {
		return nil, http.StatusOK, nil
	})
	router.Route("GET", "/posts/:postId/comments/:commentId").WithDoc(RouteMetadata{
		Summary: "Get comment",
		Tags:    []string{"comments"},
	})

	// Generate OpenAPI spec
	config := OpenAPIConfig{
		Title:       "Test API",
		Description: "Testing static and dynamic routes",
		Version:     "1.0.0",
	}

	spec := router.GenerateOpenAPI(config)

	// Verify all routes are present
	expectedPaths := map[string]bool{
		"/health":                         false,
		"/api/status":                     false,
		"/users/{id}":                     false,
		"/posts/{postId}/comments/{commentId}": false,
	}

	for path := range spec.Paths {
		if _, exists := expectedPaths[path]; exists {
			expectedPaths[path] = true
		}
	}

	// Check that all expected paths were found
	for path, found := range expectedPaths {
		if !found {
			t.Errorf("Expected path '%s' to be present in OpenAPI spec", path)
		}
	}

	// Verify static route has correct metadata
	if healthPath, ok := spec.Paths["/health"]; ok {
		if healthPath.GET == nil {
			t.Error("Expected GET operation for /health")
		} else if healthPath.GET.Summary != "Health check" {
			t.Errorf("Expected summary 'Health check', got '%s'", healthPath.GET.Summary)
		}
	}

	// Verify dynamic route has path parameters
	if userPath, ok := spec.Paths["/users/{id}"]; ok {
		if userPath.GET == nil {
			t.Error("Expected GET operation for /users/{id}")
		} else {
			foundParam := false
			for _, param := range userPath.GET.Parameters {
				if param.Name == "id" && param.In == "path" && param.Required {
					foundParam = true
					break
				}
			}
			if !foundParam {
				t.Error("Expected path parameter 'id' to be present and required")
			}
		}
	}

	// Verify route with multiple path parameters
	if commentPath, ok := spec.Paths["/posts/{postId}/comments/{commentId}"]; ok {
		if commentPath.GET == nil {
			t.Error("Expected GET operation for /posts/{postId}/comments/{commentId}")
		} else {
			paramCount := 0
			for _, param := range commentPath.GET.Parameters {
				if param.In == "path" && param.Required {
					if param.Name == "postId" || param.Name == "commentId" {
						paramCount++
					}
				}
			}
			if paramCount != 2 {
				t.Errorf("Expected 2 path parameters, found %d", paramCount)
			}
		}
	}
}
