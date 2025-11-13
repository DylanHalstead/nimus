package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/DylanHalstead/nimbus"
	"github.com/DylanHalstead/nimbus/middleware"
)

// User represents a user entity
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// UserParams holds path parameters for user routes
type UserParams struct {
	ID string `path:"id"`
}

// CreateUserRequest represents the request body for creating a user
type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// UserStore provides thread-safe access to user data
type UserStore struct {
	mu     sync.RWMutex
	users  map[int]User
	nextID int
}

// NewUserStore creates a new user store with initial data
func NewUserStore() *UserStore {
	return &UserStore{
		users: map[int]User{
			1: {ID: 1, Name: "Alice", Email: "alice@example.com"},
			2: {ID: 2, Name: "Bob", Email: "bob@example.com"},
		},
		nextID: 3,
	}
}

// List returns all users (returns a copy to prevent external mutations)
func (s *UserStore) List() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]User, 0, len(s.users))
	for _, user := range s.users {
		users = append(users, user)
	}
	return users
}

// Get retrieves a user by ID
func (s *UserStore) Get(id int) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[id]
	return user, exists
}

// Create adds a new user
func (s *UserStore) Create(name, email string) User {
	s.mu.Lock()
	defer s.mu.Unlock()

	user := User{
		ID:    s.nextID,
		Name:  name,
		Email: email,
	}
	s.users[s.nextID] = user
	s.nextID++
	return user
}

// Update updates an existing user
func (s *UserStore) Update(id int, name, email string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[id]; !exists {
		return User{}, false
	}

	user := User{
		ID:    id,
		Name:  name,
		Email: email,
	}
	s.users[id] = user
	return user, true
}

// Delete removes a user by ID
func (s *UserStore) Delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[id]; !exists {
		return false
	}

	delete(s.users, id)
	return true
}

// Validators - these are defined once and reused
var (
	userParamsValidator        = nimbus.NewValidator(&UserParams{})
	createUserRequestValidator = nimbus.NewValidator(&CreateUserRequest{})
	userValidator              = nimbus.NewValidator(&User{})
)

// RegisterUserRoutes registers user-related routes with authentication middleware
func RegisterUserRoutes(router *nimbus.Router, store *UserStore) {
	// Users group with auth middleware (using nimbus key for demo)
	group := router.Group("/api/v1/users", middleware.Auth(validateToken))

	// GET /api/v1/users - list all users (no params, body, or query)
	group.AddRoute(http.MethodGet, "",
		nimbus.WithTyped(makeListUsers(store), nil, nil, nil))

	// GET /api/v1/users/:id - get a single user (only path params)
	group.AddRoute(http.MethodGet, "/:id",
		nimbus.WithTyped(makeGetUser(store), userParamsValidator, nil, nil))

	// POST /api/v1/users - create a user (only body)
	group.AddRoute(http.MethodPost, "",
		nimbus.WithTyped(makeCreateUser(store), nil, createUserRequestValidator, nil))

	// PUT /api/v1/users/:id - update a user (path params and body)
	group.AddRoute(http.MethodPut, "/:id",
		nimbus.WithTyped(makeUpdateUser(store), userParamsValidator, createUserRequestValidator, nil))

	// DELETE /api/v1/users/:id - delete a user (only path params)
	group.AddRoute(http.MethodDelete, "/:id",
		nimbus.WithTyped(makeDeleteUser(store), userParamsValidator, nil, nil))
}

// makeListUsers returns a handler that lists all users
func makeListUsers(store *UserStore) nimbus.HandlerFuncTyped[struct{}, struct{}, struct{}] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[struct{}, struct{}, struct{}]) (any, int, error) {
		users := store.List()
		return users, http.StatusOK, nil
	}
}

// makeGetUser returns a handler that retrieves a single user by ID
func makeGetUser(store *UserStore) nimbus.HandlerFuncTyped[UserParams, struct{}, struct{}] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[UserParams, struct{}, struct{}]) (any, int, error) {
		id, err := parseID(req.Params.ID)
		if err != nil {
			return nil, http.StatusBadRequest, nimbus.NewAPIError("invalid_id", err.Error())
		}

		user, exists := store.Get(id)
		if !exists {
			return nil, http.StatusNotFound, nimbus.NewAPIError("not_found", "User not found")
		}

		return user, http.StatusOK, nil
	}
}

// makeCreateUser returns a handler that creates a new user
func makeCreateUser(store *UserStore) nimbus.HandlerFuncTyped[struct{}, CreateUserRequest, struct{}] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[struct{}, CreateUserRequest, struct{}]) (any, int, error) {
		// Validate request
		if req.Body.Name == "" || req.Body.Email == "" {
			return nil, http.StatusBadRequest, nimbus.NewAPIError("validation_error", "Name and email are required")
		}

		// Create user in store
		user := store.Create(req.Body.Name, req.Body.Email)

		return user, http.StatusCreated, nil
	}
}

// makeUpdateUser returns a handler that updates an existing user
func makeUpdateUser(store *UserStore) nimbus.HandlerFuncTyped[UserParams, CreateUserRequest, struct{}] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[UserParams, CreateUserRequest, struct{}]) (any, int, error) {
		id, err := parseID(req.Params.ID)
		if err != nil {
			return nil, http.StatusBadRequest, nimbus.NewAPIError("invalid_id", err.Error())
		}

		// Validate request
		if req.Body.Name == "" || req.Body.Email == "" {
			return nil, http.StatusBadRequest, nimbus.NewAPIError("validation_error", "Name and email are required")
		}

		// Update user in store
		user, exists := store.Update(id, req.Body.Name, req.Body.Email)
		if !exists {
			return nil, http.StatusNotFound, nimbus.NewAPIError("not_found", "User not found")
		}

		return user, http.StatusOK, nil
	}
}

// makeDeleteUser returns a handler that deletes a user
func makeDeleteUser(store *UserStore) nimbus.HandlerFuncTyped[UserParams, struct{}, struct{}] {
	return func(ctx *nimbus.Context, req *nimbus.TypedRequest[UserParams, struct{}, struct{}]) (any, int, error) {
		id, err := parseID(req.Params.ID)
		if err != nil {
			return nil, http.StatusBadRequest, nimbus.NewAPIError("invalid_id", err.Error())
		}

		// Delete user from store
		if !store.Delete(id) {
			return nil, http.StatusNotFound, nimbus.NewAPIError("not_found", "User not found")
		}

		return nil, http.StatusNoContent, nil
	}
}

// parseID parses a string ID and returns an error if invalid
func parseID(id string) (int, error) {
	val, err := strconv.Atoi(id)
	if err != nil {
		return 0, fmt.Errorf("invalid ID format: %s", id)
	}
	if val <= 0 {
		return 0, fmt.Errorf("ID must be positive, got: %d", val)
	}
	return val, nil
}

// validateToken is a dummy token validator for auth middleware example
func validateToken(token string) (any, error) {
	if token == "valid-token-123" {
		return map[string]any{"user_id": 1, "username": "demo"}, nil
	}
	return nil, errors.New("invalid token")
}
