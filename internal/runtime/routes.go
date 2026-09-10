package runtime

import (
	"sort"
	"strings"
)

// RouteInfo aggregates telemetry for one runtime route (from metric labels
// like route=, path=, endpoint=, uri=): event volume, error share, and the
// service that reports it.
type RouteInfo struct {
	Route     string  `json:"route"`
	Service   string  `json:"service"`
	Events    int     `json:"events"`
	Errors    int     `json:"errors"`
	ErrorRate float64 `json:"error_rate"` // percentage 0..100
}

// routeAttrs lists the metric label names that identify a route, in
// precedence order. Vendor metric conventions differ; the first present
// label wins.
var routeAttrs = []string{"route", "path", "endpoint", "uri", "http_route", "http_target"}

// Routes aggregates the source's metric events by route, sorted by route
// then service. Events without any route label contribute nothing. A nil
// source or no routed events yields nil.
func Routes(src Source) []RouteInfo {
	if src == nil {
		return nil
	}
	byRoute := map[string]*RouteInfo{}
	var order []string
	for _, ev := range src.Events("") {
		route := routeOf(ev)
		if route == "" {
			continue
		}
		key := route + "\x00" + ev.Service
		r, ok := byRoute[key]
		if !ok {
			r = &RouteInfo{Route: route, Service: ev.Service}
			byRoute[key] = r
			order = append(order, key)
		}
		r.Events++
		if ev.IsError() {
			r.Errors++
		}
	}
	if len(order) == 0 {
		return nil
	}
	sort.Strings(order)
	out := make([]RouteInfo, 0, len(order))
	for _, key := range order {
		r := byRoute[key]
		r.ErrorRate = float64(r.Errors) / float64(r.Events) * 100
		out = append(out, *r)
	}
	return out
}

// routeOf extracts the route from an event's attributes using the first
// recognized label in precedence order.
func routeOf(ev Event) string {
	for _, k := range routeAttrs {
		if v := ev.Attributes[k]; v != "" {
			return v
		}
	}
	return ""
}

// DriftReport compares the routes observed at runtime against the routes
// declared in code.
type DriftReport struct {
	// ProdOnly lists runtime routes with no matching code route: traffic is
	// hitting something the code no longer declares (renamed, removed, or
	// dynamically registered handler).
	ProdOnly []RouteInfo `json:"prod_only"`
	// CodeOnly lists code routes with no runtime traffic.
	CodeOnly []string `json:"code_only"`
	// Matched counts the code routes that do have traffic.
	Matched int `json:"matched"`
}

// Drift compares runtime routes against the code-declared routes. Matching
// is template-aware: a code route "/users/:id" matches a runtime route
// "/users/42" segment-wise, so parameterized handlers are not reported as
// missing. Both inputs are normalized (trailing slash stripped, query and
// leading "*" removed). Nil or empty inputs are valid ("no telemetry" /
// "no declarative routes" states).
func Drift(runtimeRoutes []RouteInfo, codeRoutes []string) DriftReport {
	var rep DriftReport
	norm := make([]string, 0, len(codeRoutes))
	for _, c := range codeRoutes {
		if n := normalizeRoute(c); n != "" {
			norm = append(norm, n)
		}
	}
	for _, r := range runtimeRoutes {
		n := normalizeRoute(r.Route)
		if n == "" {
			continue
		}
		if !routeMatchesAny(n, norm) {
			rep.ProdOnly = append(rep.ProdOnly, r)
		}
	}
	matched := map[string]bool{}
	for _, n := range norm {
		found := false
		for _, r := range runtimeRoutes {
			if routeMatches(normalizeRoute(r.Route), n) {
				found = true
				matched[n] = true
				break
			}
		}
		if !found {
			rep.CodeOnly = append(rep.CodeOnly, n)
		}
	}
	rep.Matched = len(matched)
	sort.Slice(rep.ProdOnly, func(i, j int) bool {
		if rep.ProdOnly[i].Route != rep.ProdOnly[j].Route {
			return rep.ProdOnly[i].Route < rep.ProdOnly[j].Route
		}
		return rep.ProdOnly[i].Service < rep.ProdOnly[j].Service
	})
	sort.Strings(rep.CodeOnly)
	return rep
}

// normalizeRoute canonicalizes a route for comparison: strips a leading "*",
// a trailing "/" (beyond the root), and any query string.
func normalizeRoute(r string) string {
	r = strings.TrimSpace(r)
	if i := strings.IndexAny(r, "?"); i >= 0 {
		r = r[:i]
	}
	r = strings.TrimPrefix(r, "*")
	if r != "/" {
		r = strings.TrimSuffix(r, "/")
	}
	return r
}

// routeMatches reports whether a runtime route matches a code route
// template: equal after normalization, or segment-wise equal with the code
// route's parameter segments (":id", "*path", "{uid}") matching any runtime
// segment. The brace form covers FastAPI/Spring-style templates.
func routeMatches(runtime, code string) bool {
	if runtime == code {
		return true
	}
	rs := strings.Split(runtime, "/")
	cs := strings.Split(code, "/")
	if len(rs) != len(cs) {
		return false
	}
	for i := range cs {
		if cs[i] == "" {
			continue
		}
		if isParamSegment(cs[i]) {
			continue
		}
		if rs[i] != cs[i] {
			return false
		}
	}
	return true
}

// isParamSegment reports whether a code-route segment is a parameter
// placeholder (":id", "*path", "{uid}", "{uid:int}").
func isParamSegment(seg string) bool {
	if strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "*") {
		return true
	}
	return len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}'
}

func routeMatchesAny(runtime string, codes []string) bool {
	for _, c := range codes {
		if routeMatches(runtime, c) {
			return true
		}
	}
	return false
}
