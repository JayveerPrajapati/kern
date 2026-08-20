package api

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/twin/ids"
)

// Extractor scans a repository root for HTTP API endpoints.
type Extractor struct {
	root string
}

// New creates an API extractor for the given root.
func New(root string) *Extractor {
	return &Extractor{root: root}
}

// Extract scans source files for route registrations and returns API
// nodes and edges. Each API node represents one HTTP endpoint. Edges
// link the API to its handler symbol (kind "implements") and to its
// source file (kind "defined_in").
func (e *Extractor) Extract() ([]domain.Node, []domain.Edge, error) {
	var nodes []domain.Node
	var edges []domain.Edge

	err := filepath.WalkDir(e.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			if isIgnoreDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSourceExt(filepath.Ext(path)) {
			return nil
		}
		fileNodes, fileEdges := e.extractFile(path)
		nodes = append(nodes, fileNodes...)
		edges = append(edges, fileEdges...)
		return nil
	})

	return nodes, edges, err
}

// routePattern defines a framework-specific route registration pattern.
type routePattern struct {
	// framework is the framework name (for the API node's Framework field).
	framework string
	// regex matches the route registration line and captures the HTTP
	// method, path, and handler name when the framework encodes them.
	regex *regexp.Regexp
}

// routePatterns are the framework-specific patterns we scan for. Each
// pattern is written so that a given route line matches at most one
// framework, avoiding duplicate API nodes with colliding IDs. Capture
// groups are normalized per framework in extractFile:
//
//	gin:        group 1 = method, group 2 = path,   group 3 = handler
//	express:    group 1 = method, group 2 = path,   group 3 = handler
//	flask:      group 1 = path
//	django:     group 1 = path,   group 2 = handler
//	fastapi:    group 1 = method, group 2 = path
//	spring:     group 1 = method, group 2 = path
//	net/http:   group 1 = path,   group 2 = handler
var routePatterns = []routePattern{
	// Gin/Echo (Go): r.GET("/path", handler) or r.POST("/path", handler).
	{framework: "gin", regex: regexp.MustCompile(`\.(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\(\s*"([^"]+)"\s*,\s*(\w+)`)},
	// Express (JS): app.get("/path", handler) or router.post("/path", handler).
	{framework: "express", regex: regexp.MustCompile(`(?:app|router)\.(get|post|put|delete|patch)\(\s*['"]([^'"]+)['"]\s*,\s*(?:async\s+)?(\w+)`)},
	// Flask (Python): @app.route("/path").
	{framework: "flask", regex: regexp.MustCompile(`@app\.route\(\s*['"]([^'"]+)['"]`)},
	// Django (Python): urlpatterns = [ path("...", view) ].
	{framework: "django", regex: regexp.MustCompile(`path\(\s*['"]([^'"]+)['"]\s*,\s*([\w.]+)`)},
	// FastAPI (Python): @app.get("/path") or @router.post("/path").
	{framework: "fastapi", regex: regexp.MustCompile(`@(?:app|router)\.(get|post|put|delete|patch)\(\s*['"]([^'"]+)['"]`)},
	// Spring (Java): @GetMapping("/path") or @PostMapping("/path").
	{framework: "spring", regex: regexp.MustCompile(`@(Get|Post|Put|Delete|Patch)Mapping\(\s*['"]([^'"]+)['"]`)},
	// net/http (Go): http.HandleFunc("/path", handler).
	{framework: "net/http", regex: regexp.MustCompile(`http\.HandleFunc\(\s*"([^"]+)"\s*,\s*(\w+)`)},
}

// extractFile scans a single file for route patterns.
func (e *Extractor) extractFile(path string) ([]domain.Node, []domain.Edge) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	relPath, _ := filepath.Rel(e.root, path)

	var nodes []domain.Node
	var edges []domain.Edge
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		for _, rp := range routePatterns {
			m := rp.regex.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// Normalize per-framework capture groups to
			// (method, path, handler), defaulting method to GET when the
			// framework does not encode it (Flask @app.route, etc.).
			method, apiPath, handler := "GET", "", ""
			switch rp.framework {
			case "gin":
				method, apiPath, handler = m[1], m[2], m[3]
			case "express":
				method, apiPath, handler = strings.ToUpper(m[1]), m[2], m[3]
			case "net/http":
				apiPath, handler = m[1], m[2]
			case "flask":
				apiPath = m[1]
			case "django":
				apiPath, handler = m[1], m[2]
			case "fastapi":
				method, apiPath = strings.ToUpper(m[1]), m[2]
			case "spring":
				method, apiPath = strings.ToUpper(m[1]), m[2]
			}
			if apiPath == "" {
				continue
			}

			apiID := "api:" + rp.framework + ":" + method + ":" + ids.Escape(apiPath)
			api := domain.API{
				ID:        apiID,
				Name:      method + " " + apiPath,
				Method:    method,
				Path:      apiPath,
				Symbol:    handler,
				Framework: rp.framework,
				File:      relPath,
				Line:      lineNum,
			}
			nodes = append(nodes, domain.Node{
				ID:    apiID,
				Kind:  "api",
				Label: method + " " + apiPath,
				API:   &api,
			})

			// Edge: API -> handler symbol (if a handler name was found).
			if handler != "" {
				edges = append(edges, domain.Edge{
					From: apiID,
					To:   handler,
					Kind: "implements",
					File: relPath,
					Line: lineNum,
				})
			}
			// Edge: API -> source file.
			edges = append(edges, domain.Edge{
				From: apiID,
				To:   "file:" + relPath,
				Kind: "defined_in",
				File: relPath,
				Line: lineNum,
			})
		}
	}
	return nodes, edges
}

func isIgnoreDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "out", "target",
		".venv", "venv", "__pycache__", ".cache", ".idea", ".vscode",
		".kern", "tmp", "coverage":
		return true
	}
	return false
}

func isSourceExt(ext string) bool {
	switch ext {
	case ".go", ".js", ".mjs", ".ts", ".tsx", ".py", ".java", ".rb", ".php", ".rs":
		return true
	}
	return false
}
