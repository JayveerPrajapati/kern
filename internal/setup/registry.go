package setup

// RegistryCounts returns the distinct adapter names in the effective
// registry (built-in plus custom adapters declared under root) and the
// number of registry entries (config surfaces — an agent with two
// surfaces, copilot repo+global, counts twice), for display layers that
// derive registry-driven counts. Resolution errors are ignored here —
// the check table reports them separately.
func RegistryCounts(root string) (map[string]bool, int) {
	all, _ := effectiveAdapters(root)
	names := make(map[string]bool, len(all))
	for _, a := range all {
		names[a.name] = true
	}
	return names, len(all)
}
