package main

import (
	"net/http"

	"github.com/DylanHalstead/nimbus"
)

// RegisterHealthRoutes registers health check routes (no auth required)
func RegisterHealthRoutes(router *nimbus.Router) {
	router.AddRoute(http.MethodGet, "/health", handleHealth)
	router.AddRoute(http.MethodGet, "/ready", handleReady)
}

func handleHealth(ctx *nimbus.Context) (any, int, error) {
	return map[string]any{
		"status": "healthy",
	}, http.StatusOK, nil
}

func handleReady(ctx *nimbus.Context) (any, int, error) {
	// Check database, cache, etc.
	return map[string]any{
		"status": "ready",
		"checks": map[string]any{
			"database": "ok",
			"cache":    "ok",
		},
	}, http.StatusOK, nil
}
