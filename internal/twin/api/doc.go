// Package api extracts HTTP API contracts from source code for the Digital
// Twin's API category. It scans for route registration patterns across
// frameworks (Gin, Express, Django, Flask, FastAPI, Spring, net/http) and
// produces domain.API nodes linked to their handlers via "implements" edges.
// The extraction is deterministic — no LLM.
package api
