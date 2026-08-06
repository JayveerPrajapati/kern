package index

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// entryRule detects a framework entry point on a source line. The matched
// line is an annotation, decorator, or route call; the handler is either on
// the same line (handler group) or the next declaration line (resolved via
// the language's declaration rules).
type entryRule struct {
	framework  string         // fw framework id
	files      []string       // if set, only files whose path ends with one of these
	re         *regexp.Regexp // matches the annotation / route line
	route      int            // group of the route path (0 = none)
	name       int            // group of a route name (resources :users -> /users)
	handler    int            // group of the handler name on the same line (0 = next decl)
	defRoute   string         // fallback route when none is captured (e.g. root -> "/")
	pathOnly   bool           // require the route to look like a path ("/x", "*")
	classBase  bool           // when the next decl is a type, remember its route as a base prefix
	classEntry bool           // when the next decl is a type, emit an entry for it
}

// entryRules holds the framework rules per language. It is a package variable
// so foreign.go's init can attach the rules to each langSpec.
var entryRules = map[string][]entryRule{
	"java": {
		{framework: "spring-mvc", re: regexp.MustCompile(`@(?:Get|Post|Put|Delete|Patch|Request)Mapping\s*\(\s*"([^"]*)"`), route: 1, classBase: true},
		{framework: "spring-mvc", re: regexp.MustCompile(`@(?:Get|Post|Put|Delete|Patch)Mapping\b`), classBase: true},
		{framework: "spring-boot", re: regexp.MustCompile(`@SpringBootApplication\b`), classEntry: true},
		{framework: "spring-core", re: regexp.MustCompile(`@(?:Service|Component|Repository|Configuration)\b`), classEntry: true},
		{framework: "jakarta-ee", re: regexp.MustCompile(`@Path\s*\(\s*"([^"]*)"\s*\)`), route: 1, classBase: true},
		{framework: "jakarta-ee", re: regexp.MustCompile(`@(?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\b`)},
	},
	"python": {
		{framework: "python-http", re: regexp.MustCompile(`@(?:app|application|blueprint|bp)\s*\.\s*[a-z_]+\s*\(\s*["']([^"']+)["']`), route: 1},
		{framework: "fastapi", re: regexp.MustCompile(`@(?:router|routers|api|auth|users|items)\s*\.\s*(?:get|post|put|patch|delete|websocket)\s*\(\s*["']([^"']+)["']`), route: 1},
		{framework: "celery", re: regexp.MustCompile(`@(?:app|shared_task)\s*\.?\s*task\b`)},
		{framework: "django", files: []string{"urls.py"}, re: regexp.MustCompile(`\b(?:path|re_path|url)\s*\(\s*["']([^"']+)["']\s*,\s*([A-Za-z_][\w.]*)`), route: 1, handler: 2},
	},
	"javascript": {
		{framework: "js-router", re: regexp.MustCompile(`\.(get|post|put|patch|delete|head|options|all|use)\s*\(\s*["']([^"']*)["'](?:\s*,\s*([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*))?`), route: 2, handler: 3, pathOnly: true},
		{framework: "nestjs", re: regexp.MustCompile(`@(Get|Post|Put|Patch|Delete|Options|Head|All)\s*\(\s*(?:["']([^"')]*)["'])?\s*\)`), route: 2},
		{framework: "nestjs", re: regexp.MustCompile(`@Controller\s*\(\s*(?:["']([^"')]*)["'])?\s*\)`), route: 1, classBase: true},
	},
	"typescript": {
		{framework: "js-router", re: regexp.MustCompile(`\.(get|post|put|patch|delete|head|options|all|use)\s*\(\s*["']([^"']*)["'](?:\s*,\s*([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*))?`), route: 2, handler: 3, pathOnly: true},
		{framework: "nestjs", re: regexp.MustCompile(`@(Get|Post|Put|Patch|Delete|Options|Head|All)\s*\(\s*(?:["']([^"')]*)["'])?\s*\)`), route: 2},
		{framework: "nestjs", re: regexp.MustCompile(`@Controller\s*\(\s*(?:["']([^"')]*)["'])?\s*\)`), route: 1, classBase: true},
	},
	"ruby": {
		{framework: "rails", files: []string{"routes.rb"}, re: regexp.MustCompile(`\b(get|post|put|patch|delete|match)\s+["']([^"']+)["']\s*,\s*to:\s*["']([A-Za-z_]\w*#[A-Za-z_]\w*)["']`), route: 2, handler: 3},
		{framework: "rails", files: []string{"routes.rb"}, re: regexp.MustCompile(`\broot\s+["']([A-Za-z_]\w*#[A-Za-z_]\w*)["']`), handler: 1, defRoute: "/"},
		{framework: "rails", files: []string{"routes.rb"}, re: regexp.MustCompile(`\b(resources|resource)\s+:([a-z_]\w*)`), name: 2},
	},
	"php": {
		{framework: "laravel", re: regexp.MustCompile(`Route::(?:get|post|put|patch|delete|any|match)\s*\(\s*["']([^"']*)["']\s*,\s*([^\)]+)`), route: 1, handler: 2},
	},
}

// entrySym builds a standalone framework entry-point symbol.
func entrySym(rel, lang, framework, name, route string, line int) Symbol {
	return Symbol{Kind: "entry", Name: name, Entry: true, Framework: framework, Route: route, File: rel, Line: line, Lang: lang}
}

func groupAt(m []string, g int) string {
	if g <= 0 || g >= len(m) {
		return ""
	}
	return m[g]
}

func stripQuotes(s string) string {
	return strings.Trim(s, `"' `)
}

// routeOK reports whether a captured string plausibly is a route path rather
// than some other string argument: it starts with "/", is a wildcard, or
// contains a "/" (covers method+path patterns like "GET /users").
func routeOK(r string) bool {
	r = strings.TrimSpace(r)
	if r == "" {
		return false
	}
	if r == "*" || r == "/*" {
		return true
	}
	if strings.HasPrefix(r, "/") {
		return true
	}
	return strings.Contains(r, "/")
}

// trimSlash normalizes a path segment: "" stays "", "/" (any run of slashes)
// becomes "/" (root), and "/api/" becomes "api".
func trimSlash(s string) string {
	if s == "" {
		return ""
	}
	trimmed := strings.Trim(s, "/")
	if trimmed == "" {
		return "/"
	}
	return trimmed
}

// joinRoute joins a class/controller base path and a mapped path, always
// producing a leading "/" ("" only when both are empty).
func joinRoute(base, route string) string {
	b := trimSlash(base)
	r := trimSlash(route)
	switch {
	case b == "" && r == "":
		return ""
	case b == "" && r == "/":
		return "/"
	case b == "/" && r == "":
		return "/"
	case b == "/" && r == "/":
		return "/"
	case b == "":
		return "/" + r
	case r == "" || r == "/":
		return "/" + b
	default:
		return "/" + b + "/" + r
	}
}

// cleanHandler reduces a route-target string ("UserController@index",
// "[Ctrl::class, 'show']", "views.index") to the bare method name used for
// enrichment, e.g. "index".
func cleanHandler(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "'") || strings.Contains(s, `"`) {
		var q rune
		for _, r := range s {
			if r == '\'' || r == '"' {
				q = r
				break
			}
		}
		if q != 0 {
			rest := s[strings.IndexRune(s, q)+1:]
			if i := strings.IndexRune(rest, q); i >= 0 {
				s = rest[:i]
			}
		}
	}
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, "::class"); i >= 0 {
		s = s[:i]
	}
	words := regexp.MustCompile(identRe).FindAllString(s, -1)
	if len(words) > 0 {
		return words[len(words)-1]
	}
	return s
}

func suffixAny(lower string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

// nextDecl finds the next real declaration line after i, skipping blank,
// comment and annotation lines. Returns the declaration rule, its match, and
// the line index, or nil when nothing declares within the lookahead window.
func nextDecl(f *ffile, i int, spec *langSpec) (*declRule, []string, int) {
	for j := i; j < i+10 && j < len(f.lines); j++ {
		trimmed := strings.TrimSpace(f.lines[j])
		if trimmed == "" || f.com[j] {
			continue
		}
		if strings.HasPrefix(trimmed, "@") {
			continue
		}
		rule, m := matchRule(f.lines[j], spec)
		if rule != nil {
			return rule, m, j
		}
		return nil, nil, -1
	}
	return nil, nil, -1
}

// enrichEntry marks a same-file symbol as a framework entry point. It returns
// true when the handler symbol was found and enriched.
func enrichEntry(syms []Symbol, rel, full, framework, route string) bool {
	for i := range syms {
		if syms[i].File == rel && syms[i].FullName() == full {
			syms[i].Entry = true
			syms[i].Framework = framework
			if route != "" {
				syms[i].Route = route
			}
			return true
		}
	}
	return false
}

// extractEntries runs the framework entry rules over a foreign-language file.
// It enriches existing handler symbols (decorated functions, mapped methods,
// controller endpoints) as entry points and returns any standalone entry
// symbols for routes that could not be tied to a same-file handler.
func extractEntries(f *ffile, spec *langSpec, types []typeDecl, syms []Symbol, rel, lang string) []Symbol {
	if len(spec.entries) == 0 {
		return nil
	}
	lower := strings.ToLower(rel)
	classBase := map[string]string{}
	var extra []Symbol
	n := len(f.lines)
	for i := 0; i < n; i++ {
		trimmed := strings.TrimSpace(f.lines[i])
		if trimmed == "" || f.com[i] {
			continue
		}
		for ri := range spec.entries {
			er := &spec.entries[ri]
			if len(er.files) > 0 && !suffixAny(lower, er.files) {
				continue
			}
			m := er.re.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			route := groupAt(m, er.route)
			if route == "" && er.name > 0 {
				route = "/" + groupAt(m, er.name)
			}
			route = stripQuotes(route)
			if route == "" {
				route = er.defRoute
			}
			if er.pathOnly && !routeOK(route) {
				break
			}
			line := i + 1
			recv := ""
			if name := groupAt(m, er.handler); name != "" {
				// The handler is named on the route line itself (Django urls,
				// Laravel Route::, Rails to:). Use it directly.
				name = cleanHandler(name)
				if name == "" || spec.kw[name] {
					name = route
				}
				full := name
				fullRoute := joinRoute("", route)
				if !enrichEntry(syms, rel, full, er.framework, fullRoute) {
					extra = append(extra, entrySym(rel, lang, er.framework, name, fullRoute, line))
				}
				break
			}
			rule, dm, dl := nextDecl(f, i+1, spec)
			if rule == nil {
				// No handler declaration follows (e.g. Rails `resources :posts`
				// or Laravel `Route::resource(...)`). Emit a standalone entry
				// carrying the route so it stays searchable.
				if route != "" {
					extra = append(extra, entrySym(rel, lang, er.framework, route, route, line))
				}
				break
			}
			line = dl + 1
			name := dm[len(dm)-1]
			if typeKinds[rule.kind] {
				if er.classBase && route != "" {
					classBase[name] = route
				}
				if er.classEntry {
					extra = append(extra, entrySym(rel, lang, er.framework, name, route, line))
				}
				break
			}
			if rule.isDef {
				recv = enclosingType(dl, types)
			}
			full := name
			if recv != "" {
				full = recv + "." + name
			}
			fullRoute := joinRoute(classBase[recv], route)
			if !enrichEntry(syms, rel, full, er.framework, fullRoute) {
				extra = append(extra, entrySym(rel, lang, er.framework, name, fullRoute, line))
			}
			break
		}
	}
	return extra
}

var httpVerbs = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
	"HEAD": true, "OPTIONS": true, "USE": true, "ALL": true, "CONNECT": true, "TRACE": true,
}

// extractGoEntries finds HTTP entry points in a Go file: http.HandleFunc /
// http.Handle registrations, ServeMux Handle/HandleFunc calls, and verb-method
// router calls (r.GET, e.POST, app.PUT, ...) covering gin/echo/chi/fiber. It
// enriches the handler function symbols it can resolve in this file and
// returns standalone entry markers for the rest.
func extractGoEntries(fset *token.FileSet, f *ast.File, syms []Symbol, rel string) []Symbol {
	var extra []Symbol
	pathOf := func(arg ast.Expr) string {
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				return s
			}
		}
		return ""
	}
	handlerName := func(arg ast.Expr) string {
		switch t := arg.(type) {
		case *ast.Ident:
			return t.Name
		case *ast.SelectorExpr:
			return t.Sel.Name
		}
		return ""
	}
	visit := func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := fn.Sel.Name
		var fwID string
		switch {
		case method == "Handle" || method == "HandleFunc":
			if _, isVar := fn.X.(*ast.Ident); !isVar {
				return true
			}
			fwID = "net-http"
		case httpVerbs[method]:
			fwID = "http-route"
		default:
			return true
		}
		if len(ce.Args) < 1 {
			return true
		}
		r := pathOf(ce.Args[0])
		if !routeOK(r) {
			return true
		}
		name := ""
		if len(ce.Args) >= 2 {
			name = handlerName(ce.Args[1])
		}
		if name == "" {
			name = method + " " + r
		}
		if !enrichEntry(syms, rel, name, fwID, r) {
			extra = append(extra, entrySym(rel, "go", fwID, name, r, fset.Position(ce.Pos()).Line))
		}
		return true
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				ast.Inspect(d.Body, visit)
			}
		case *ast.GenDecl:
			ast.Inspect(d, visit)
		}
	}
	return extra
}

// resolveEntries links standalone entry markers to the handler symbols they
// refer to. Handlers often live in a different file than the route that
// registers them, so a single-file extraction cannot enrich them directly;
// this pass runs after the whole project has been indexed.
func (ix *Index) resolveEntries() {
	byName := map[string]int{}
	byFull := map[string]int{}
	for i := range ix.Symbols {
		s := &ix.Symbols[i]
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		if _, ok := byFull[s.FullName()]; !ok {
			byFull[s.FullName()] = i
		}
		if _, ok := byName[s.Name]; !ok {
			byName[s.Name] = i
		}
	}
	for i := range ix.Symbols {
		s := &ix.Symbols[i]
		if s.Kind != "entry" || s.Name == "" {
			continue
		}
		ti := -1
		if t, ok := byFull[s.FullName()]; ok {
			ti = t
		} else if t, ok := byName[s.Name]; ok {
			ti = t
		}
		if ti >= 0 {
			if ix.Symbols[ti].Framework == "" {
				ix.Symbols[ti].Framework = s.Framework
			}
			ix.Symbols[ti].Entry = true
		}
	}
}
