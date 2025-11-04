package nimbus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
)

// OpenAPISpec represents an OpenAPI 3.0 specification
type OpenAPISpec struct {
	OpenAPI    string                 `json:"openapi"`
	Info       OpenAPIInfo            `json:"info"`
	Servers    []OpenAPIServer        `json:"servers,omitempty"`
	Paths      map[string]OpenAPIPath `json:"paths"`
	Components OpenAPIComponents      `json:"components,omitempty"`
}

// OpenAPIInfo contains API metadata
type OpenAPIInfo struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version"`
	Contact     *Contact `json:"contact,omitempty"`
	License     *License `json:"license,omitempty"`
}

// Contact information
type Contact struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

// License information
type License struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// OpenAPIServer represents a server
type OpenAPIServer struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// OpenAPIPath represents operations for a path
type OpenAPIPath struct {
	GET    *OpenAPIOperation `json:"get,omitempty"`
	POST   *OpenAPIOperation `json:"post,omitempty"`
	PUT    *OpenAPIOperation `json:"put,omitempty"`
	DELETE *OpenAPIOperation `json:"delete,omitempty"`
	PATCH  *OpenAPIOperation `json:"patch,omitempty"`
}

// OpenAPIOperation represents an API operation
type OpenAPIOperation struct {
	Summary     string                     `json:"summary,omitempty"`
	Description string                     `json:"description,omitempty"`
	Tags        []string                   `json:"tags,omitempty"`
	OperationID string                     `json:"operationId,omitempty"`
	Parameters  []OpenAPIParameter         `json:"parameters,omitempty"`
	RequestBody *OpenAPIRequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]OpenAPIResponse `json:"responses"`
	Security    []map[string][]string      `json:"security,omitempty"`
}

// OpenAPIParameter represents a parameter
type OpenAPIParameter struct {
	Name        string         `json:"name"`
	In          string         `json:"in"` // query, header, path, cookie
	Description string         `json:"description,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Schema      *OpenAPISchema `json:"schema,omitempty"`
	Example     any            `json:"example,omitempty"`
}

// OpenAPIRequestBody represents a request body
type OpenAPIRequestBody struct {
	Description string                      `json:"description,omitempty"`
	Required    bool                        `json:"required,omitempty"`
	Content     map[string]OpenAPIMediaType `json:"content"`
}

// OpenAPIMediaType represents a media type
type OpenAPIMediaType struct {
	Schema  *OpenAPISchema `json:"schema,omitempty"`
	Example any            `json:"example,omitempty"`
}

// OpenAPIResponse represents a response
type OpenAPIResponse struct {
	Description string                      `json:"description"`
	Content     map[string]OpenAPIMediaType `json:"content,omitempty"`
}

// OpenAPIComponents contains reusable schemas
type OpenAPIComponents struct {
	Schemas map[string]*OpenAPISchema `json:"schemas,omitempty"`
}

// OpenAPISchema represents a JSON schema
type OpenAPISchema struct {
	Type        string                    `json:"type,omitempty"`
	Format      string                    `json:"format,omitempty"`
	Description string                    `json:"description,omitempty"`
	Properties  map[string]*OpenAPISchema `json:"properties,omitempty"`
	Required    []string                  `json:"required,omitempty"`
	Items       *OpenAPISchema            `json:"items,omitempty"`
	Enum        []any                     `json:"enum,omitempty"`
	Minimum     *float64                  `json:"minimum,omitempty"`
	Maximum     *float64                  `json:"maximum,omitempty"`
	MinLength   *int                      `json:"minLength,omitempty"`
	MaxLength   *int                      `json:"maxLength,omitempty"`
	Pattern     string                    `json:"pattern,omitempty"`
	Example     any                       `json:"example,omitempty"`
	Ref         string                    `json:"$ref,omitempty"`
}

// RouteMetadata contains metadata for generating OpenAPI docs
type RouteMetadata struct {
	Summary        string
	Description    string
	Tags           []string
	RequestSchema  *Schema
	RequestBody    any // Example request body
	QuerySchema    *Schema
	ResponseSchema map[int]any // Status code -> example response
	OperationID    string
}

// OpenAPIConfig configures OpenAPI generation
type OpenAPIConfig struct {
	Title       string
	Description string
	Version     string
	Servers     []OpenAPIServer
	Contact     *Contact
	License     *License
}

// GenerateOpenAPI generates an OpenAPI 3.0 specification from the router
func (r *Router) GenerateOpenAPI(config OpenAPIConfig) *OpenAPISpec {
	spec := &OpenAPISpec{
		OpenAPI: "3.0.3",
		Info: OpenAPIInfo{
			Title:       config.Title,
			Description: config.Description,
			Version:     config.Version,
			Contact:     config.Contact,
			License:     config.License,
		},
		Servers: config.Servers,
		Paths:   make(map[string]OpenAPIPath),
		Components: OpenAPIComponents{
			Schemas: make(map[string]*OpenAPISchema),
		},
	}

	// Process all routes
	r.generatePathsFromRoutes(spec)

	return spec
}

// generatePathsFromRoutes processes routes and generates OpenAPI paths
func (r *Router) generatePathsFromRoutes(spec *OpenAPISpec) {
	table := r.table.Load()

	// Iterate through all methods and their route trees
	for method, tree := range table.trees {
		// Collect all routes from the tree
		routes := tree.collectRoutes()

		for _, route := range routes {
			// Convert path parameters from :param to {param}
			openAPIPath := convertPathParams(route.pattern)

			// Get or create path item
			pathItem, exists := spec.Paths[openAPIPath]
			if !exists {
				pathItem = OpenAPIPath{}
			}

			// Get route metadata
			metadata := r.getRouteMetadata(route)

			// Create operation
			operation := r.createOperation(route, metadata, spec)

			// Add operation to path based on method
			switch method {
			case "GET":
				pathItem.GET = operation
			case "POST":
				pathItem.POST = operation
			case "PUT":
				pathItem.PUT = operation
			case "DELETE":
				pathItem.DELETE = operation
			case "PATCH":
				pathItem.PATCH = operation
			}

			spec.Paths[openAPIPath] = pathItem
		}
	}
}

// createOperation creates an OpenAPI operation from a route
func (r *Router) createOperation(route *Route, metadata *RouteMetadata, spec *OpenAPISpec) *OpenAPIOperation {
	operation := &OpenAPIOperation{
		Summary:     metadata.Summary,
		Description: metadata.Description,
		Tags:        metadata.Tags,
		OperationID: metadata.OperationID,
		Parameters:  []OpenAPIParameter{},
		Responses:   make(map[string]OpenAPIResponse),
	}

	// Generate operation ID if not provided
	if operation.OperationID == "" {
		operation.OperationID = generateOperationID(route.method, route.pattern)
	}

	// Extract path parameters
	pathParams := extractPathParams(route.pattern)
	for _, param := range pathParams {
		operation.Parameters = append(operation.Parameters, OpenAPIParameter{
			Name:        param,
			In:          "path",
			Description: fmt.Sprintf("Path parameter: %s", param),
			Required:    true,
			Schema: &OpenAPISchema{
				Type: "string",
			},
		})
	}

	// Add query parameters from schema
	if metadata.QuerySchema != nil {
		queryParams := schemaToQueryParameters(metadata.QuerySchema)
		operation.Parameters = append(operation.Parameters, queryParams...)
	}

	// Add request body for POST/PUT/PATCH
	if (route.method == "POST" || route.method == "PUT" || route.method == "PATCH") && metadata.RequestSchema != nil {
		schemaName := getSchemaName(metadata.RequestSchema)
		schemaRef := fmt.Sprintf("#/components/schemas/%s", schemaName)

		// Add schema to components if not already present
		if _, exists := spec.Components.Schemas[schemaName]; !exists {
			spec.Components.Schemas[schemaName] = schemaToOpenAPISchema(metadata.RequestSchema)
		}

		operation.RequestBody = &OpenAPIRequestBody{
			Required: true,
			Content: map[string]OpenAPIMediaType{
				"application/json": {
					Schema: &OpenAPISchema{
						Ref: schemaRef,
					},
					Example: metadata.RequestBody,
				},
			},
		}
	}

	// Add responses
	if len(metadata.ResponseSchema) > 0 {
		for statusCode, example := range metadata.ResponseSchema {
			operation.Responses[fmt.Sprintf("%d", statusCode)] = OpenAPIResponse{
				Description: getStatusDescription(statusCode),
				Content: map[string]OpenAPIMediaType{
					"application/json": {
						Schema: &OpenAPISchema{
							Type: "object",
						},
						Example: example,
					},
				},
			}
		}
	} else {
		// Default success response
		operation.Responses["200"] = OpenAPIResponse{
			Description: "Successful response",
			Content: map[string]OpenAPIMediaType{
				"application/json": {
					Schema: &OpenAPISchema{
						Type: "object",
					},
				},
			},
		}
	}

	// Always add error responses
	operation.Responses["400"] = OpenAPIResponse{
		Description: "Bad request",
		Content: map[string]OpenAPIMediaType{
			"application/json": {
				Schema: &OpenAPISchema{
					Type: "object",
					Properties: map[string]*OpenAPISchema{
						"error":   {Type: "string"},
						"message": {Type: "string"},
					},
				},
			},
		},
	}

	return operation
}

// schemaToOpenAPISchema converts a validation Schema to OpenAPI schema
func schemaToOpenAPISchema(schema *Schema) *OpenAPISchema {
	openAPISchema := &OpenAPISchema{
		Type:       "object",
		Properties: make(map[string]*OpenAPISchema),
		Required:   []string{},
	}

	for fieldName, rule := range schema.fields {
		propSchema := &OpenAPISchema{}

		// Get field type from struct
		structField, ok := schema.structType.FieldByName(getStructFieldName(schema.structType, fieldName))
		if !ok {
			continue
		}

		// Determine type
		switch structField.Type.Kind() {
		case reflect.String:
			propSchema.Type = "string"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			propSchema.Type = "integer"
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			propSchema.Type = "integer"
		case reflect.Float32, reflect.Float64:
			propSchema.Type = "number"
		case reflect.Bool:
			propSchema.Type = "boolean"
		default:
			propSchema.Type = "string"
		}

		// Add validation constraints
		if rule.minLength >= 0 {
			minLen := rule.minLength
			propSchema.MinLength = &minLen
		}
		if rule.maxLength >= 0 {
			maxLen := rule.maxLength
			propSchema.MaxLength = &maxLen
		}
		if rule.min != nil {
			minFloat := float64(*rule.min)
			propSchema.Minimum = &minFloat
		}
		if rule.max != nil {
			maxFloat := float64(*rule.max)
			propSchema.Maximum = &maxFloat
		}
		if rule.pattern != nil {
			propSchema.Pattern = rule.pattern.String()
		}
		if len(rule.enum) > 0 {
			propSchema.Enum = make([]any, len(rule.enum))
			for i, v := range rule.enum {
				propSchema.Enum[i] = v
			}
		}
		if rule.email {
			propSchema.Format = "email"
		}

		openAPISchema.Properties[fieldName] = propSchema

		if rule.required {
			openAPISchema.Required = append(openAPISchema.Required, fieldName)
		}
	}

	return openAPISchema
}

// schemaToQueryParameters converts a Schema to query parameters
func schemaToQueryParameters(schema *Schema) []OpenAPIParameter {
	params := []OpenAPIParameter{}

	for fieldName, rule := range schema.fields {
		structField, ok := schema.structType.FieldByName(getStructFieldName(schema.structType, fieldName))
		if !ok {
			continue
		}

		param := OpenAPIParameter{
			Name:     fieldName,
			In:       "query",
			Required: rule.required,
			Schema:   &OpenAPISchema{},
		}

		// Determine type
		switch structField.Type.Kind() {
		case reflect.String:
			param.Schema.Type = "string"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			param.Schema.Type = "integer"
		case reflect.Float32, reflect.Float64:
			param.Schema.Type = "number"
		case reflect.Bool:
			param.Schema.Type = "boolean"
		}

		// Add constraints
		if rule.minLength >= 0 {
			minLen := rule.minLength
			param.Schema.MinLength = &minLen
		}
		if rule.maxLength >= 0 {
			maxLen := rule.maxLength
			param.Schema.MaxLength = &maxLen
		}
		if rule.min != nil {
			minFloat := float64(*rule.min)
			param.Schema.Minimum = &minFloat
		}
		if rule.max != nil {
			maxFloat := float64(*rule.max)
			param.Schema.Maximum = &maxFloat
		}
		if len(rule.enum) > 0 {
			param.Schema.Enum = make([]any, len(rule.enum))
			for i, v := range rule.enum {
				param.Schema.Enum[i] = v
			}
		}
		if rule.pattern != nil {
			param.Schema.Pattern = rule.pattern.String()
		}
		if rule.email {
			param.Schema.Format = "email"
		}

		params = append(params, param)
	}

	return params
}

// Helper functions

func convertPathParams(path string) string {
	// Convert :param to {param}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = "{" + part[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

func extractPathParams(pattern string) []string {
	params := []string{}
	parts := strings.Split(pattern, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ":") {
			params = append(params, part[1:])
		}
	}
	return params
}

func generateOperationID(method, pattern string) string {
	// Convert pattern to camelCase operation ID
	// e.g., POST /api/users/:id -> postApiUsersById
	parts := strings.Split(pattern, "/")
	var words []string
	words = append(words, strings.ToLower(method))

	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, ":") {
			words = append(words, "By")
			words = append(words, capitalize(part[1:]))
		} else {
			words = append(words, capitalize(part))
		}
	}

	result := words[0]
	for i := 1; i < len(words); i++ {
		result += words[i]
	}
	return result
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func getSchemaName(schema *Schema) string {
	// Get the name of the struct type
	typeName := schema.structType.Name()
	if typeName == "" {
		typeName = "Request"
	}
	return typeName
}

func getStatusDescription(code int) string {
	descriptions := map[int]string{
		200: "Successful response",
		201: "Resource created successfully",
		204: "No content",
		400: "Bad request",
		401: "Unauthorized",
		403: "Forbidden",
		404: "Not found",
		500: "Internal server error",
	}
	if desc, ok := descriptions[code]; ok {
		return desc
	}
	return "Response"
}

// getRouteMetadata retrieves metadata attached to a route
func (r *Router) getRouteMetadata(route *Route) *RouteMetadata {
	if route.metadata != nil {
		return route.metadata
	}
	return &RouteMetadata{}
}

// ServeSwaggerJSON serves the OpenAPI specification as JSON
func (r *Router) ServeSwaggerJSON(path string, config OpenAPIConfig) {
	// Cache the OpenAPI spec (generated once, reused for all requests)
	var specCache *OpenAPISpec
	var specOnce sync.Once

	r.AddRoute(http.MethodGet, path, func(ctx *Context) (any, int, error) {
		specOnce.Do(func() {
			specCache = r.GenerateOpenAPI(config)
		})
		ctx.Header("Content-Type", "application/json")
		return specCache, 200, nil
	})
}

// ServeSwaggerUI serves a Swagger UI HTML page
func (r *Router) ServeSwaggerUI(path, specURL string) {
	// Cache the HTML template (generated once, reused for all requests)
	var htmlCache string
	var htmlOnce sync.Once

	r.AddRoute(http.MethodGet, path, func(ctx *Context) (any, int, error) {
		htmlOnce.Do(func() {
			htmlCache = generateSwaggerUiHtml(specURL)
		})
		return ctx.HTML(200, htmlCache)
	})
}

// GenerateOpenAPIFile generates and saves the OpenAPI spec to a JSON file
func (r *Router) GenerateOpenAPIFile(filename string, config OpenAPIConfig) error {
	spec := r.GenerateOpenAPI(config)

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OpenAPI spec: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// EnableSwagger sets up both Swagger UI and JSON spec endpoints
// IMPORTANT: Call this AFTER all routes are registered, as the OpenAPI spec
// is cached on first request and will not reflect routes added later
func (r *Router) EnableSwagger(uiPath, jsonPath string, config OpenAPIConfig) {
	r.ServeSwaggerUI(uiPath, jsonPath)
	r.ServeSwaggerJSON(jsonPath, config)
}

func generateSwaggerUiHtml(specURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>API Documentation</title>
    <link rel="stylesheet" type="text/css" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.10.5/swagger-ui.css">
    <style>
        body { 
            margin: 0; 
            padding: 0; 
        }
        #swagger-ui {
            max-width: 100%%;
        }
        .loading {
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            font-family: sans-serif;
            color: #3b4151;
        }
    </style>
</head>
<body>
    <div id="loading" class="loading">
        <div>Loading API Documentation...</div>
    </div>
    <div id="swagger-ui"></div>
    <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.10.5/swagger-ui-bundle.js" charset="UTF-8"></script>
    <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.10.5/swagger-ui-standalone-preset.js" charset="UTF-8"></script>
    <script>
        window.onload = function() {
            // Hide loading message
            document.getElementById('loading').style.display = 'none';
            
            // Initialize Swagger UI
            const ui = SwaggerUIBundle({
                url: "%s",
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: "StandaloneLayout",
                onComplete: function() {
                    console.log('Swagger UI loaded successfully');
                },
                onFailure: function(err) {
                    console.error('Failed to load Swagger UI:', err);
                    document.getElementById('swagger-ui').innerHTML = 
                        '<div style="padding: 20px; color: red;">Failed to load API documentation. Check console for details.</div>';
                }
            });

            window.ui = ui;
        };
        
        // Add error handling for script loading
        window.onerror = function(msg, url, lineNo, columnNo, error) {
            console.error('Script error:', msg, 'at', url, lineNo + ':' + columnNo);
            document.getElementById('loading').innerHTML = 
                '<div style="color: red;">Error loading API documentation. Please check your internet connection.</div>';
            return false;
        };
    </script>
</body>
</html>`, specURL)
}
