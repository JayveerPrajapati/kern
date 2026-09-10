// Package runtime snapshot helpers: one canonical machine-readable shape for
// runtime status, shared by the CLI (`kern runtime status --json`), the MCP
// kern_runtime tool, and the web /api/runtime endpoint, so the three surfaces
// cannot drift.
package runtime

// StatusSnapshot renders the canonical status JSON for a source. A nil source
// yields the "not wired" shape with the enable hint.
func StatusSnapshot(src Source) map[string]any {
	if src == nil {
		return map[string]any{
			"wired": false,
			"hint":  "set KERN_PROMETHEUS_URL, KERN_OTEL_URL, KERN_K8S_API, or provide .kern/runtime.json",
		}
	}
	profiles := ServiceProfiles(src)
	events, errors := 0, 0
	for _, p := range profiles {
		events += p.Events
		errors += p.Errors
	}
	svcs := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		svcs = append(svcs, map[string]any{
			"name":       p.Name,
			"events":     p.Events,
			"errors":     p.Errors,
			"error_rate": p.ErrorRate,
		})
	}
	return map[string]any{
		"wired":        true,
		"source":       src.Name(),
		"poll_seconds": int(PollInterval().Seconds()),
		"events":       events,
		"errors":       errors,
		"deployments":  len(src.Deployments("")),
		"commits":      len(src.Commits()),
		"services":     svcs,
	}
}

// DriftSnapshot renders the canonical drift JSON: runtime routes vs the
// code-declared routes from an index.
func DriftSnapshot(src Source, codeRoutes []string) map[string]any {
	rep := Drift(Routes(src), codeRoutes)
	out := map[string]any{
		"matched":   rep.Matched,
		"code_only": rep.CodeOnly,
	}
	prod := make([]map[string]any, 0, len(rep.ProdOnly))
	for _, r := range rep.ProdOnly {
		prod = append(prod, map[string]any{
			"route":      r.Route,
			"service":    r.Service,
			"events":     r.Events,
			"error_rate": r.ErrorRate,
		})
	}
	out["prod_only"] = prod
	return out
}
