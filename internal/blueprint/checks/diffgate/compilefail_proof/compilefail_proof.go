//go:build compilefail_proof

// Package compilefail_proof proves the fail-loud diff-gate wiring
// this file calls the pre-fix one-argument constructor
// signature and therefore does NOT compile. It is excluded from normal
// builds by the build tag; TestNewCatalogDriftCheckRequiresExplicitTools
// (internal/mcp/catalog_drift_test.go) compiles it with -tags
// compilefail_proof and asserts the build FAILS — removing the explicit
// injection breaks compilation at the wiring site.
package compilefail_proof

import "github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"

var _ = diffgate.NewCatalogDriftCheck("/tmp")
