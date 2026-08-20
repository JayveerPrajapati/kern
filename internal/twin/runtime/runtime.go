package runtime

import (
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/twin/ids"
)

// Builder converts runtime telemetry into graph nodes.
type Builder struct {
	source runtime.Source
}

// New creates a Builder over the given runtime source.
func New(source runtime.Source) *Builder {
	return &Builder{source: source}
}

// Build produces graph nodes and edges from the runtime source's events
// and deployments. Each service with events becomes a "service-health"
// node; each error event becomes an "error" node linked to its service;
// each deployment becomes a "deployment" node linked to its service.
// Error events with a "file" attribute are also linked to the
// corresponding file node via a "caused_by" edge.
func (b *Builder) Build() ([]domain.Node, []domain.Edge, error) {
	var nodes []domain.Node
	var edges []domain.Edge

	// Deployments: one node per deployment, linked to its service.
	for _, dep := range b.source.Deployments("") {
		depID := fmt.Sprintf("deployment:%s:%s", ids.Escape(dep.Service), ids.Escape(dep.Version))
		nodes = append(nodes, domain.Node{
			ID:    depID,
			Kind:  "deployment",
			Label: dep.Service + " " + dep.Version,
		})
		svcID := "service:" + ids.Escape(dep.Service)
		edges = append(edges, domain.Edge{From: depID, To: svcID, Kind: "deploys"})
	}

	// Discover services from all events and deployments.
	services := map[string]bool{}
	for _, dep := range b.source.Deployments("") {
		services[dep.Service] = true
	}
	for _, ev := range b.source.Events("") {
		services[ev.Service] = true
	}

	// Create a service node for every distinct service so the deploys,
	// occurred_in, and monitors edges never point at a non-existent node.
	for svc := range services {
		nodes = append(nodes, domain.Node{
			ID:      "service:" + ids.Escape(svc),
			Kind:    "service",
			Label:   svc,
			Service: &domain.Service{Name: svc},
		})
	}

	// Per-service events: error nodes, health node.
	for svc := range services {
		svcID := "service:" + ids.Escape(svc)
		errorCount := 0
		for _, ev := range b.source.Events(svc) {
			if ev.Type == runtime.EventError {
				errorCount++
				errID := fmt.Sprintf("error:%s:%d", ids.Escape(svc), ev.Timestamp.UnixNano())
				nodes = append(nodes, domain.Node{
					ID:    errID,
					Kind:  "error",
					Label: string(ev.Type) + " at " + ev.Timestamp.Format(time.RFC3339),
				})
				edges = append(edges, domain.Edge{From: errID, To: svcID, Kind: "occurred_in"})
				// Link to the file where the error occurred, if known.
				if file, ok := ev.Attributes["file"]; ok && file != "" {
					edges = append(edges, domain.Edge{From: errID, To: "file:" + file, Kind: "caused_by"})
				}
			}
		}
		health := "healthy"
		if errorCount > 10 {
			health = "unhealthy"
		} else if errorCount > 0 {
			health = "degraded"
		}
		healthID := "health:" + ids.Escape(svc)
		nodes = append(nodes, domain.Node{
			ID:    healthID,
			Kind:  "service-health",
			Label: svc + " (" + health + ")",
		})
		edges = append(edges, domain.Edge{From: healthID, To: svcID, Kind: "monitors"})
	}

	return nodes, edges, nil
}
