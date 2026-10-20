package setup

import "github.com/JayveerPrajapati/kern/internal/agentwire"

// isShippedPluginVersion reports whether the bytes are a kern-deployed
// version of the opencode plugin (see internal/pluginhash). Implemented in
// internal/agentwire (IsShippedPluginVersion).
func isShippedPluginVersion(b []byte) bool { return agentwire.IsShippedPluginVersion(b) }
