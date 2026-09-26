// Package enterprise implements multi-project enterprise mode for kern-server
// Shared org knowledge, centralized policies, org-level audit and
// multi-project state. Each project gets its own lazy-built web.App with
// per-project index/graph/memories/incidents, while org-level audit and
// policies are shared.
// Usage:
// srv, err := enterprise.New()
// if err != nil { /* org mode refused without KERN_RBAC_DEFAULT_DENY=1 */ }
// srv.Register("payments", "/repos/payments")
// srv.Register("orders", "/repos/orders")
// http.ListenAndServe(":8090", srv)
package enterprise
