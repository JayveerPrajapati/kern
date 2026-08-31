// Package main is the safe-change vertical slice fixture: a small service with
// the spec's components (UserService, UserRepository, CacheService,
// TenantContext), tests, and an architecture rule. .
package main

// User is a domain entity.
type User struct {
	ID     string
	Name   string
	Tenant string
}

// TenantContext carries the current tenant for tenant-aware operations.
type TenantContext struct {
	TenantID string
}

// CacheService is a tenant-aware cache. It depends on TenantContext to build
// tenant-scoped cache keys and MUST NOT depend on UserRepository (architecture
// rule enforced by the fixture's .kern/constitution.yaml).
type CacheService struct {
	store map[string]User
}

// NewCacheService creates an empty tenant-aware cache.
func NewCacheService() *CacheService {
	return &CacheService{store: map[string]User{}}
}

// Get returns a user from the cache for the given tenant, reporting whether
// it was present.
func (c *CacheService) Get(ctx TenantContext, id string) (User, bool) {
	u, ok := c.store[c.key(ctx, id)]
	return u, ok
}

// Put stores a user in the cache scoped to the tenant.
func (c *CacheService) Put(ctx TenantContext, u User) {
	c.store[c.key(ctx, u.ID)] = u
}

// key builds a tenant-scoped cache key.
func (c *CacheService) key(ctx TenantContext, id string) string {
	return ctx.TenantID + ":" + id
}

// UserRepository loads users from the (simulated) store.
type UserRepository struct {
	users map[string]User
}

// NewUserRepository seeds a repository with users.
func NewUserRepository(users ...User) *UserRepository {
	r := &UserRepository{users: map[string]User{}}
	for _, u := range users {
		r.users[u.ID] = u
	}
	return r
}

// FindByID loads a user by ID.
func (r *UserRepository) FindByID(id string) (User, bool) {
	u, ok := r.users[id]
	return u, ok
}

// UserService is the application service under change: it reads through the
// tenant-aware cache before hitting the repository.
type UserService struct {
	cache *CacheService
	repo  *UserRepository
}

// NewUserService wires the service to a cache and a repository.
func NewUserService(cache *CacheService, repo *UserRepository) *UserService {
	return &UserService{cache: cache, repo: repo}
}

// GetUser returns a user, populating the tenant-aware cache on a miss.
func (s *UserService) GetUser(ctx TenantContext, id string) User {
	if u, ok := s.cache.Get(ctx, id); ok {
		return u
	}
	u, _ := s.repo.FindByID(id)
	u.Tenant = ctx.TenantID
	s.cache.Put(ctx, u)
	return u
}
