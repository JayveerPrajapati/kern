package agentwire

import "github.com/JayveerPrajapati/kern/internal/pluginhash"

// IsShippedPluginVersion reports whether the bytes are a kern-deployed
// version of the opencode plugin (see internal/pluginhash).
func IsShippedPluginVersion(b []byte) bool { return pluginhash.IsShipped(b) }
