package main

import "fmt"

// User is a minimal multi-tenant user record used by the fixture repository.
type User struct {
	ID     string
	Name   string
	Tenant string
}

// UserRepository is the persistence layer. It deliberately has no dependency on
// the cache or presentation layers so the architecture boundary rules hold.
type UserRepository struct{}

// ByID returns a user for the given ID, or a not-found error.
func (r *UserRepository) ByID(id string) (*User, error) {
	if id == "" {
		return nil, fmt.Errorf("user %q not found", id)
	}
	return &User{ID: id, Name: "Test User", Tenant: "acme"}, nil
}