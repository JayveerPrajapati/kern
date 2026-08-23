package main

import "fmt"

// main wires the fixture together so the module builds as an executable. It is
// otherwise unused by the tests.
func main() {
	repo := &UserRepository{}
	cache := &CacheService{}
	ctx := &TenantContext{Tenant: "acme"}
	svc := NewUserService(repo, cache, ctx)
	u, err := svc.GetUser("u-1")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("user:", u.Name, "tenant:", u.Tenant)
}