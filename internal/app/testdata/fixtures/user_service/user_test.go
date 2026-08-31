package main

import "testing"

// TestUserServiceGetUser verifies the service path resolves through the cache
// and falls back to the repository.
func TestUserServiceGetUser(t *testing.T) {
	svc := NewUserService(&UserRepository{}, &CacheService{}, &TenantContext{Tenant: "acme"})
	u, err := svc.GetUser("u-1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.ID != "u-1" {
		t.Errorf("id = %q, want u-1", u.ID)
	}
	if u.Tenant != "acme" {
		t.Errorf("tenant = %q, want acme", u.Tenant)
	}
}

// TestCacheRoundTrip verifies the cache stores and returns a value.
func TestCacheRoundTrip(t *testing.T) {
	c := &CacheService{}
	c.Set("k", "v")
	if got, ok := c.Get("k"); !ok || got != "v" {
		t.Errorf("cache roundtrip = (%v, %v), want (v, true)", got, ok)
	}
}
