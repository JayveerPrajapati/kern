package fw

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// FlowStep represents one discrete stage in the request pipeline.
type FlowStep struct {
	Stage   string   `json:"stage"` // "route", "middleware", "handler", "service", "model"
	Symbol  string   `json:"symbol"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Details string   `json:"details,omitempty"`
	Calls   []string `json:"calls,omitempty"`
}

// RouteFlow represents the complete end-to-end trace of a framework HTTP endpoint.
type RouteFlow struct {
	Framework        string     `json:"framework"` // e.g. "gin", "express", "fastapi", "spring-boot", "nestjs"
	Method           string     `json:"method"`    // "GET", "POST", "PUT", "DELETE", "USE"
	Path             string     `json:"path"`      // "/api/users/:id"
	Handler          string     `json:"handler"`   // "GetUserHandler"
	File             string     `json:"file"`
	Line             int        `json:"line"`
	Middleware       []string   `json:"middleware,omitempty"`
	InjectedServices []string   `json:"injected_services,omitempty"`
	DBModels         []string   `json:"db_models,omitempty"`
	Steps            []FlowStep `json:"steps"`
}

// TraceResult encapsulates all traced routes and dependency injection chains.
type TraceResult struct {
	Root       string      `json:"root"`
	Frameworks []string    `json:"frameworks"`
	Total      int         `json:"total"`
	Routes     []RouteFlow `json:"routes"`
}

var (
	// Go Gin/Chi/Echo/Fiber route patterns: r.GET("/path", mw1, mw2, handler)
	goRouteRe = regexp.MustCompile(`(?i)(?:r|router|app|group|v\d+|api|e)\.(GET|POST|PUT|DELETE|PATCH|OPTIONS|HEAD|Any|Handle|HandleFunc)\s*\(\s*["'\x60]([^"'\x60]+)["'\x60]\s*,\s*([^)]+)\)`)

	// JS/TS Express/Fastify route patterns: app.get("/path", auth, handler)
	jsRouteRe = regexp.MustCompile(`(?i)(?:app|router|api|v\d+)\.(get|post|put|delete|patch|options|head|use|all)\s*\(\s*["'\x60]([^"'\x60]+)["'\x60]\s*,\s*([^)]+)\)`)

	// Python FastAPI/Flask route patterns: @app.get("/path") or @router.post("/path")
	pyRouteRe = regexp.MustCompile(`(?i)@(?:app|router|bp|api)\.(get|post|put|delete|patch|route)\s*\(\s*["'\x60]([^"'\x60]+)["'\x60](?:[^)]*)\)\s*(?:\n\s*@[^\n]+)*\s*\n\s*(?:async\s+)?def\s+([a-zA-Z0-9_]+)`)

	// Java Spring Boot annotation patterns: @GetMapping("/path") or @PostMapping(value = "/path")
	javaSpringRouteRe = regexp.MustCompile(`(?i)@(Get|Post|Put|Delete|Patch|Request)Mapping\s*(?:\(\s*(?:value\s*=\s*)?["']([^"']+)["']\s*\))?\s*(?:\n\s*@[^\n]+)*\s*\n\s*(?:public|protected|private)?\s*(?:[\w<>\[\],\s]+)\s+([a-zA-Z0-9_]+)\s*\(`)

	// NestJS TS decorator patterns: @Get('/path') or @Post('path')
	nestRouteRe = regexp.MustCompile(`(?i)@(Get|Post|Put|Delete|Patch|All|Options|Head)\s*\(\s*(?:["'\x60]([^"'\x60]*)["'\x60])?\s*\)\s*(?:\n\s*@[^\n]+)*\s*\n\s*(?:public|private|protected|async)?\s*([a-zA-Z0-9_]+)\s*\(`)

	// DI Signals
	goDISignalRe     = regexp.MustCompile(`(?i)(?:Service|Repository|Repo|Client|Store|DB|Dao|UseCase)\b`)
	goModelSignalRe  = regexp.MustCompile(`(?i)(?:Model|Entity|Schema|Table|User|Order|Item|Product|Account|Payment|Token|Session|Record)\b`)
	pyDISignalRe     = regexp.MustCompile(`(?i)Depends\s*\(\s*([a-zA-Z0-9_]+)\s*\)`)
	javaDISignalRe   = regexp.MustCompile(`(?i)@(Autowired|Inject|Resource)\s*(?:\n\s*@[^\n]+)*\s*\n\s*(?:private|protected|public)?\s*([a-zA-Z0-9_<>]+)\s+([a-zA-Z0-9_]+);`)
	nestDISignalRe   = regexp.MustCompile(`(?i)(?:private|protected|public|readonly)\s+([a-zA-Z0-9_]+)\s*:\s*([a-zA-Z0-9_]+Service|[a-zA-Z0-9_]+Repository|[a-zA-Z0-9_]+Client)`)
)

// TraceRoutes scans the project codebase using AST index and regex extractors to construct
// end-to-end route execution pipelines (Route -> Middleware -> Handler -> Service -> DB Model).
func TraceRoutes(ctx context.Context, root string, routeFilter string) (*TraceResult, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}

	detectedFW, _ := Detect(absRoot)
	var fwNames []string
	for _, d := range detectedFW {
		fwNames = append(fwNames, d.ID)
	}

	// Load symbol index if available for call-graph correlation
	ix, _ := index.LoadOrBuild(absRoot)

	routes := make([]RouteFlow, 0)

	// Walk project source files to discover route declarations
	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				name := info.Name()
				if fwBaselineIgnoreDirs[name] {
					return filepath.SkipDir
				}
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !sourceExts[ext] {
			return nil
		}

		rel, _ := filepath.Rel(absRoot, path)
		if strings.Contains(rel, "vendor/") || strings.Contains(rel, "node_modules/") || strings.Contains(rel, "_test.go") || strings.Contains(rel, ".test.") {
			return nil
		}

		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(contentBytes)

		foundRoutes := extractRoutesFromFile(rel, ext, content, ix)
		for _, r := range foundRoutes {
			if routeFilter != "" {
				if !strings.Contains(strings.ToLower(r.Path), strings.ToLower(routeFilter)) &&
					!strings.Contains(strings.ToLower(r.Handler), strings.ToLower(routeFilter)) &&
					!strings.Contains(strings.ToLower(r.Method), strings.ToLower(routeFilter)) {
					continue
				}
			}
			routes = append(routes, r)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort routes deterministically by Method and Path
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})

	return &TraceResult{
		Root:       absRoot,
		Frameworks: fwNames,
		Total:      len(routes),
		Routes:     routes,
	}, nil
}

func extractRoutesFromFile(relPath, ext, content string, ix *index.Index) []RouteFlow {
	var results []RouteFlow

	switch ext {
	case ".go":
		results = append(results, extractGoRoutes(relPath, content, ix)...)
	case ".js", ".ts", ".jsx", ".tsx":
		results = append(results, extractJSTSRoutes(relPath, content, ix)...)
	case ".py":
		results = append(results, extractPythonRoutes(relPath, content, ix)...)
	case ".java":
		results = append(results, extractJavaRoutes(relPath, content, ix)...)
	}

	return results
}

func extractGoRoutes(relPath, content string, ix *index.Index) []RouteFlow {
	var routes []RouteFlow
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		matches := goRouteRe.FindStringSubmatch(line)
		if len(matches) >= 4 {
			method := strings.ToUpper(matches[1])
			path := matches[2]
			rawHandlers := strings.Split(matches[3], ",")

			var middlewares []string
			var handler string

			for idx, h := range rawHandlers {
				h = strings.TrimSpace(h)
				if h == "" {
					continue
				}
				if idx == len(rawHandlers)-1 {
					handler = cleanHandlerName(h)
				} else {
					middlewares = append(middlewares, cleanHandlerName(h))
				}
			}

			if handler == "" {
				handler = "AnonymousHandler"
			}

			flow := buildRouteFlow("gin/echo/chi", method, path, handler, relPath, i+1, middlewares, content, ix)
			routes = append(routes, flow)
		}
	}
	return routes
}

func extractJSTSRoutes(relPath, content string, ix *index.Index) []RouteFlow {
	var routes []RouteFlow
	lines := strings.Split(content, "\n")

	// 1. Express/Fastify: app.get('/path', auth, handler)
	for i, line := range lines {
		matches := jsRouteRe.FindStringSubmatch(line)
		if len(matches) >= 4 {
			method := strings.ToUpper(matches[1])
			path := matches[2]
			rawHandlers := strings.Split(matches[3], ",")

			var middlewares []string
			var handler string

			for idx, h := range rawHandlers {
				h = strings.TrimSpace(h)
				if h == "" {
					continue
				}
				if idx == len(rawHandlers)-1 {
					handler = cleanHandlerName(h)
				} else {
					middlewares = append(middlewares, cleanHandlerName(h))
				}
			}

			flow := buildRouteFlow("express/fastify", method, path, handler, relPath, i+1, middlewares, content, ix)
			routes = append(routes, flow)
		}
	}

	// 2. NestJS Decorators: @Get(':id') methodName()
	nestMatches := nestRouteRe.FindAllStringSubmatchIndex(content, -1)
	for _, m := range nestMatches {
		method := strings.ToUpper(content[m[2]:m[3]])
		path := "/"
		if m[4] != -1 && m[5] != -1 {
			path = content[m[4]:m[5]]
		}
		handler := content[m[6]:m[7]]
		lineNum := getLineNumber(content, m[0])

		flow := buildRouteFlow("nestjs", method, path, handler, relPath, lineNum, nil, content, ix)
		routes = append(routes, flow)
	}

	return routes
}

func extractPythonRoutes(relPath, content string, ix *index.Index) []RouteFlow {
	var routes []RouteFlow
	matches := pyRouteRe.FindAllStringSubmatchIndex(content, -1)

	for _, m := range matches {
		method := strings.ToUpper(content[m[2]:m[3]])
		path := content[m[4]:m[5]]
		handler := content[m[6]:m[7]]
		lineNum := getLineNumber(content, m[0])

		// Look for DI Depends(...) in handler definition
		snippet := content[m[0]:min(len(content), m[1]+500)]
		var injected []string
		depMatches := pyDISignalRe.FindAllStringSubmatch(snippet, -1)
		for _, dm := range depMatches {
			if len(dm) >= 2 {
				injected = append(injected, dm[1])
			}
		}

		flow := buildRouteFlow("fastapi/flask", method, path, handler, relPath, lineNum, nil, content, ix)
		flow.InjectedServices = append(flow.InjectedServices, injected...)
		routes = append(routes, flow)
	}

	return routes
}

func extractJavaRoutes(relPath, content string, ix *index.Index) []RouteFlow {
	var routes []RouteFlow
	matches := javaSpringRouteRe.FindAllStringSubmatchIndex(content, -1)

	for _, m := range matches {
		verb := strings.ToUpper(content[m[2]:m[3]])
		method := verb
		if verb == "REQUEST" {
			method = "ALL"
		}
		path := "/"
		if m[4] != -1 && m[5] != -1 {
			path = content[m[4]:m[5]]
		}
		handler := content[m[6]:m[7]]
		lineNum := getLineNumber(content, m[0])

		// Check for @Autowired in file
		var injected []string
		diMatches := javaDISignalRe.FindAllStringSubmatch(content, -1)
		for _, dm := range diMatches {
			if len(dm) >= 4 {
				injected = append(injected, fmt.Sprintf("%s (%s)", dm[3], dm[2]))
			}
		}

		flow := buildRouteFlow("spring-boot", method, path, handler, relPath, lineNum, nil, content, ix)
		flow.InjectedServices = append(flow.InjectedServices, injected...)
		routes = append(routes, flow)
	}

	return routes
}

func buildRouteFlow(fw, method, path, handler, file string, line int, middlewares []string, content string, ix *index.Index) RouteFlow {
	if !strings.HasPrefix(path, "/") && path != "" {
		path = "/" + path
	}

	flow := RouteFlow{
		Framework:  fw,
		Method:     method,
		Path:       path,
		Handler:    handler,
		File:       file,
		Line:       line,
		Middleware: middlewares,
	}

	// 1. Stage: Route
	flow.Steps = append(flow.Steps, FlowStep{
		Stage:   "route",
		Symbol:  fmt.Sprintf("%s %s", method, path),
		File:    file,
		Line:    line,
		Details: fmt.Sprintf("HTTP %s endpoint routing via %s", method, fw),
	})

	// 2. Stage: Middleware
	for _, mw := range middlewares {
		mwLine := line
		mwFile := file
		if ix != nil {
			if sym, ok := ix.FindSymbol(mw); ok {
				mwLine = sym.Line
				mwFile = sym.File
			}
		}
		flow.Steps = append(flow.Steps, FlowStep{
			Stage:   "middleware",
			Symbol:  mw,
			File:    mwFile,
			Line:    mwLine,
			Details: "Request interception, authentication or policy middleware",
		})
	}

	// 3. Stage: Handler
	hLine := line
	hFile := file
	var handlerCallees []string
	if ix != nil {
		if sym, ok := ix.FindSymbol(handler); ok {
			hLine = sym.Line
			hFile = sym.File
			for _, edge := range ix.Calls[sym.FullName()] {
				handlerCallees = append(handlerCallees, edge.Target)
			}
		}
	}
	flow.Steps = append(flow.Steps, FlowStep{
		Stage:   "handler",
		Symbol:  handler,
		File:    hFile,
		Line:    hLine,
		Details: "Primary controller / route handler logic",
		Calls:   handlerCallees,
	})

	// 4. Stage: Service / Business Logic (DI)
	var injectedServices []string
	var dbModels []string

	// Search handlerCallees and file content for Service/Repository/Model symbols
	for _, callee := range handlerCallees {
		if goDISignalRe.MatchString(callee) {
			injectedServices = append(injectedServices, callee)
			flow.Steps = append(flow.Steps, FlowStep{
				Stage:   "service",
				Symbol:  callee,
				File:    hFile,
				Details: "Injected business logic / service domain component",
			})
		} else if goModelSignalRe.MatchString(callee) {
			dbModels = append(dbModels, callee)
			flow.Steps = append(flow.Steps, FlowStep{
				Stage:   "model",
				Symbol:  callee,
				File:    hFile,
				Details: "Data model / persistence entity schema",
			})
		}
	}

	// Also extract from NestJS / Go field signatures in file if present
	if strings.Contains(content, "Service") || strings.Contains(content, "Repository") {
		matches := nestDISignalRe.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) >= 3 {
				injectedServices = append(injectedServices, fmt.Sprintf("%s (%s)", m[1], m[2]))
			}
		}
	}

	flow.InjectedServices = uniqueStrings(injectedServices)
	flow.DBModels = uniqueStrings(dbModels)

	return flow
}

func cleanHandlerName(h string) string {
	h = strings.TrimSpace(h)
	// Strip func(...) inline closures
	if strings.HasPrefix(h, "func") {
		return "inline anonymous func"
	}
	// Strip method calls like handler.GetUser -> handler.GetUser
	return h
}

func getLineNumber(content string, byteOffset int) int {
	if byteOffset <= 0 {
		return 1
	}
	if byteOffset > len(content) {
		byteOffset = len(content)
	}
	return strings.Count(content[:byteOffset], "\n") + 1
}

func uniqueStrings(s []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, v := range s {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
