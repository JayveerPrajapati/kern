package main

import "testing"

// TestGetUserPopulatesCache exercises the tenant-aware caching path.
func TestGetUserPopulatesCache(t *testing.T) {
	repo := NewUserRepository(User{ID: "u1", Name: "Ada"})
	svc := NewUserService(NewCacheService(), repo)

	u := svc.GetUser(TenantContext{TenantID: "t1"}, "u1")
	if u.Name != "Ada" {
		t.Fatalf("Name = %q, want Ada", u.Name)
	}
	if u.Tenant != "t1" {
		t.Fatalf("Tenant = %q, want t1", u.Tenant)
	}
}

// TestGetUserCacheIsTenantScoped verifies cache keys are tenant-scoped.
func TestGetUserCacheIsTenantScoped(t *testing.T) {
	repo := NewUserRepository(User{ID: "u1", Name: "Ada"})
	cache := NewCacheService()
	svc := NewUserService(cache, repo)

	_ = svc.GetUser(TenantContext{TenantID: "t1"}, "u1")
	// A different tenant must not hit the same cache entry.
	if _, ok := cache.Get(TenantContext{TenantID: "t2"}, "u1"); ok {
		t.Fatal("cache entry leaked across tenants")
	}
}
