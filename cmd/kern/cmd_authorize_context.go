// cmd_authorize_context.go owns the thin `kern authorize-context` delegate;
// the implementation moved to internal/contextcli (workstream 3.6-A —
// context/guard family extraction).
package main

import (
	"github.com/JayveerPrajapati/kern/internal/contextcli"
)

// runAuthorizeContext delegates `kern authorize-context` to the
// internal/contextcli family.
func runAuthorizeContext(rest []string) int { return contextcli.RunAuthorizeContext(rest) }
