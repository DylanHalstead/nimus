package main

import (
	"log"

	"github.com/DylanHalstead/nimbus"
	"github.com/DylanHalstead/nimbus/middleware"
)

func main() {
	router := nimbus.NewRouter()

	// Initialize data stores (thread-safe)
	userStore := NewUserStore()
	productStore := NewProductStore()

	// Global middleware applied to all routes
	router.Use(
		middleware.Recovery(),
		middleware.RequestID(),
		middleware.Logger(middleware.DevelopmentLoggerConfig()),
		middleware.CORS(),
	)

	// Register route modules with group-specific middleware
	// Health routes: no auth required
	RegisterHealthRoutes(router)

	// User routes: require authentication
	// See users.go for auth middleware
	RegisterUserRoutes(router, userStore)

	// Product routes: rate limited to 10 req/sec with burst of 20
	// See products.go for rate limit middleware
	RegisterProductRoutes(router, productStore)

	log.Println("==============================================")
	log.Println("Server running on http://localhost:8080")
	log.Println("==============================================")
	log.Println("Available endpoints:")
	log.Println("  GET  /health               - Health check (no auth)")
	log.Println("  GET  /ready                - Readiness check (no auth)")
	log.Println("  GET  /api/v1/users         - List users (requires Authorization: Bearer valid-token-123)")
	log.Println("  GET  /api/v1/users/:id     - Get user (requires auth)")
	log.Println("  POST /api/v1/users         - Create user (requires auth)")
	log.Println("  PUT  /api/v1/users/:id     - Update user (requires auth)")
	log.Println("  DEL  /api/v1/users/:id     - Delete user (requires auth)")
	log.Println("  GET  /api/v1/products      - List products (rate limited)")
	log.Println("  GET  /api/v1/products/:id  - Get product (rate limited)")
	log.Println("  POST /api/v1/products      - Create product (rate limited)")
	log.Println("==============================================")
	log.Println("Try:")
	log.Println("  curl http://localhost:8080/health")
	log.Println("  curl http://localhost:8080/api/v1/products")
	log.Println("  curl -H 'Authorization: Bearer valid-token-123' http://localhost:8080/api/v1/users")
	log.Println("==============================================")

	if err := router.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
