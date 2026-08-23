package main

// TenantContext carries the current tenant for multi-tenancy. It is shared by
// the application service layer but never imported by the cache layer.
type TenantContext struct {
	Tenant string
}

// TenantID returns the current tenant identifier.
func (t *TenantContext) TenantID() string { return t.Tenant }