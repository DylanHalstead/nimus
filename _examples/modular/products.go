package main

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/DylanHalstead/nimbus"
	"github.com/DylanHalstead/nimbus/middleware"
)

// Product represents a product entity
type Product struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

// CreateProductRequest represents the request body for creating a product
type CreateProductRequest struct {
	Name  string  `json:"name" validate:"required,minlen=3,maxlen=100"`
	Price float64 `json:"price" validate:"required,min=0"`
}

// ProductFilters represents query parameters for filtering products
type ProductFilters struct {
	MinPrice float64 `json:"min_price" query:"min_price" validate:"min=0"`
	MaxPrice float64 `json:"max_price" query:"max_price" validate:"min=0"`
}

// ProductParams holds path parameters for product routes
type ProductParams struct {
	ID string `path:"id"`
}

// ProductStore provides thread-safe access to product data
type ProductStore struct {
	mu       sync.RWMutex
	products map[int]Product
	nextID   int
}

// NewProductStore creates a new product store with initial data
func NewProductStore() *ProductStore {
	return &ProductStore{
		products: map[int]Product{
			1: {ID: 1, Name: "Laptop", Price: 999.99},
			2: {ID: 2, Name: "Mouse", Price: 29.99},
			3: {ID: 3, Name: "Keyboard", Price: 79.99},
		},
		nextID: 4,
	}
}

// List returns all products (returns a copy to prevent external mutations)
func (s *ProductStore) List() []Product {
	s.mu.RLock()
	defer s.mu.RUnlock()

	products := make([]Product, 0, len(s.products))
	for _, product := range s.products {
		products = append(products, product)
	}
	return products
}

// Get retrieves a product by ID
func (s *ProductStore) Get(id int) (Product, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	product, exists := s.products[id]
	return product, exists
}

// Create adds a new product
func (s *ProductStore) Create(name string, price float64) Product {
	s.mu.Lock()
	defer s.mu.Unlock()

	product := Product{
		ID:    s.nextID,
		Name:  name,
		Price: price,
	}
	s.products[s.nextID] = product
	s.nextID++
	return product
}

var (
	productParamsValidator = nimbus.NewValidator(&ProductParams{})
)

// RegisterProductRoutes registers product-related routes with rate limiting
func RegisterProductRoutes(router *nimbus.Router, store *ProductStore) {
	// Products group with rate limiting middleware
	group := router.Group("/api/v1/products", middleware.RateLimit(10, 20))

	// GET /api/v1/products - list products with optional query filters
	group.AddRoute(http.MethodGet, "",
		nimbus.WithTyped(makeListProducts(store), nil, nil, nimbus.NewValidator(&ProductFilters{})))

	// GET /api/v1/products/:id - get a single product
	group.AddRoute(http.MethodGet, "/:id",
		nimbus.WithTyped(makeGetProduct(store), productParamsValidator, nil, nil))

	// POST /api/v1/products - create a new product
	group.AddRoute(http.MethodPost, "",
		nimbus.WithTyped(makeCreateProduct(store), nil, nimbus.NewValidator(&CreateProductRequest{}), nil))
}

// makeListProducts returns a handler that lists products with optional filters
func makeListProducts(store *ProductStore) nimbus.HandlerFuncTyped[struct{}, struct{}, ProductFilters] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[struct{}, struct{}, ProductFilters]) (any, int, error) {
		// Get all products from store
		products := store.List()

		// Apply filters if provided
		minPrice := 0.0
		maxPrice := 0.0
		if req.Query != nil {
			minPrice = req.Query.MinPrice
			maxPrice = req.Query.MaxPrice

			// Filter products if filters are set
			if minPrice > 0 || maxPrice > 0 {
				filtered := make([]Product, 0, len(products))
				for _, p := range products {
					if minPrice > 0 && p.Price < minPrice {
						continue
					}
					if maxPrice > 0 && p.Price > maxPrice {
						continue
					}
					filtered = append(filtered, p)
				}
				products = filtered
			}
		}

		return map[string]any{
			"products": products,
			"count":    len(products),
			"filters": map[string]any{
				"min_price": minPrice,
				"max_price": maxPrice,
			},
		}, http.StatusOK, nil
	}
}

// makeGetProduct returns a handler that retrieves a single product by ID
func makeGetProduct(store *ProductStore) nimbus.HandlerFuncTyped[ProductParams, struct{}, struct{}] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[ProductParams, struct{}, struct{}]) (any, int, error) {
		id, err := parseProductID(req.Params.ID)
		if err != nil {
			return nil, http.StatusBadRequest, nimbus.NewAPIError("invalid_id", err.Error())
		}

		product, exists := store.Get(id)
		if !exists {
			return nil, http.StatusNotFound, nimbus.NewAPIError("not_found", "Product not found")
		}

		return product, http.StatusOK, nil
	}
}

// makeCreateProduct returns a handler that creates a new product
func makeCreateProduct(store *ProductStore) nimbus.HandlerFuncTyped[struct{}, CreateProductRequest, struct{}] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[struct{}, CreateProductRequest, struct{}]) (any, int, error) {
		// Create product in store
		product := store.Create(req.Body.Name, req.Body.Price)

		return map[string]any{
			"message": "Product created successfully",
			"product": product,
		}, http.StatusCreated, nil
	}
}

// parseProductID parses a string ID and returns an error if invalid
func parseProductID(id string) (int, error) {
	val, err := strconv.Atoi(id)
	if err != nil {
		return 0, fmt.Errorf("invalid ID format: %s", id)
	}
	if val <= 0 {
		return 0, fmt.Errorf("ID must be positive, got: %d", val)
	}
	return val, nil
}
