package runtime

import (
	"sort"
	"time"
)

// ServiceProfile aggregates a service's telemetry window: event volume,
// error share, and recency — the inputs to runtime-weighted review scoring.
type ServiceProfile struct {
	Name      string    `json:"name"`
	Events    int       `json:"events"`
	Errors    int       `json:"errors"`
	ErrorRate float64   `json:"error_rate"` // percentage 0..100
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
}

// ServiceProfiles computes one profile per service seen by the source,
// sorted by service name. ErrorRate is errors/events*100 (0 when a service
// has no events). A nil source or a source without events yields nil — the
// "no telemetry" state consumers must treat as an absent dimension.
func ServiceProfiles(src Source) []ServiceProfile {
	if src == nil {
		return nil
	}
	bySvc := map[string]*ServiceProfile{}
	var order []string
	for _, ev := range src.Events("") {
		p, ok := bySvc[ev.Service]
		if !ok {
			p = &ServiceProfile{Name: ev.Service}
			bySvc[ev.Service] = p
			order = append(order, ev.Service)
		}
		p.Events++
		if ev.IsError() {
			p.Errors++
		}
		if p.First.IsZero() || ev.Timestamp.Before(p.First) {
			p.First = ev.Timestamp
		}
		if ev.Timestamp.After(p.Last) {
			p.Last = ev.Timestamp
		}
	}
	if len(order) == 0 {
		return nil
	}
	sort.Strings(order)
	out := make([]ServiceProfile, 0, len(order))
	for _, name := range order {
		p := bySvc[name]
		p.ErrorRate = float64(p.Errors) / float64(p.Events) * 100
		out = append(out, *p)
	}
	return out
}

// ProfileFor returns the profile whose service name matches name, or nil.
func ProfileFor(profiles []ServiceProfile, name string) *ServiceProfile {
	for i := range profiles {
		if profiles[i].Name == name {
			return &profiles[i]
		}
	}
	return nil
}
