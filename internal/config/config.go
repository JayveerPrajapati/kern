// Package config is kern's single configuration path: a JSON file at
// <root>/.kern/config.json with per-key environment-variable overrides.
//
// Every typed getter resolves the same three tiers, in order:
//
//  1. environment variable (if set and valid)
//  2. .kern/config.json (per project root)
//  3. built-in default
//
// The file is optional and parsed at most once per root (cached; see Reset).
// A missing file is silent; a malformed file prints a one-line warning to
// stderr and falls back to defaults — never a hard error.
//
// Secrets and fail-closed/safety toggles are intentionally NOT part of the
// file schema and remain environment-only (see Registry for what IS
// file-configurable).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// filePath is the config file location relative to a project root. It mirrors
// the .kern/boundaries.json precedent and does not collide with anything else
// under .kern/ today.
const filePath = ".kern/config.json"

// fileCache holds the parsed config per absolute project root so a process
// reads the file at most once per root. Reset clears it (tests, long-lived
// processes that want to re-read after an edit).
var fileCache sync.Map // absRoot -> *fileEntry

// fileEntry is the cached parse result for one root.
type fileEntry struct {
	vals map[string]any // nil when the file is missing or malformed
}

// Reset clears the per-root config cache. Tests use it to pick up file
// changes (or to isolate fixtures) between calls.
func Reset() {
	fileCache = sync.Map{}
}

// load returns the parsed config for root ("" means the process working
// directory). A missing file yields an empty config; a malformed file prints a
// one-line warning to stderr and yields an empty config — never a hard error.
func load(root string) *fileEntry {
	root = resolveRoot(root)
	if e, ok := fileCache.Load(root); ok {
		return e.(*fileEntry)
	}
	e := readFile(filepath.Join(root, filePath))
	fileCache.Store(root, e)
	return e
}

// resolveRoot normalizes root to an absolute path; "" resolves to the process
// working directory.
func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return root
	}
	return abs
}

// readFile parses one config file. Missing/unreadable files yield an empty
// config silently; malformed JSON yields an empty config plus a one-line
// stderr warning.
func readFile(path string) *fileEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return &fileEntry{}
	}
	var vals map[string]any
	if err := json.Unmarshal(data, &vals); err != nil {
		fmt.Fprintf(os.Stderr, "kern: ignoring malformed %s: %v\n", path, err)
		return &fileEntry{}
	}
	return &fileEntry{vals: vals}
}

// lookup walks vals following a dot-path key ("llm.model_roles.planner").
// It reports whether the key resolves to a non-nil value.
func lookup(vals map[string]any, key string) (any, bool) {
	if vals == nil {
		return nil, false
	}
	var cur any = vals
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// parseFunc converts a raw value (an env string, or a decoded JSON value)
// into the getter's type; ok=false means "not valid, fall through".
type parseFunc func(raw any) (val any, ok bool)

// resolve walks the precedence tiers and returns (value, source) where source
// is "env", "file", or "default". An env var that is unset, empty, or fails
// its parse falls through to the file tier; a file key that is absent or of
// the wrong type falls through to the default.
func resolve(root, envName, key string, def any, parseEnv, parseFile parseFunc) (any, string) {
	if envName != "" {
		if raw, ok := os.LookupEnv(envName); ok && raw != "" {
			if v, valid := parseEnv(raw); valid {
				return v, "env"
			}
		}
	}
	if e := load(root); e.vals != nil {
		if raw, ok := lookup(e.vals, key); ok {
			if v, valid := parseFile(raw); valid {
				return v, "file"
			}
		}
	}
	return def, "default"
}

// ---- typed getters ---------------------------------------------------------

// String resolves key with env > file > default. The env value is used when
// set to a non-empty string; the file value must be a JSON string.
func String(root, envName, key, def string) string {
	v, _ := stringResolved(root, envName, key, def)
	return v
}

func stringResolved(root, envName, key, def string) (string, string) {
	v, src := resolve(root, envName, key, def,
		func(raw any) (any, bool) { s, ok := raw.(string); return s, ok && s != "" },
		func(raw any) (any, bool) { s, ok := raw.(string); return s, ok })
	return v.(string), src
}

// Int resolves key with env > file > default. The env value must parse as an
// integer; the file value may be a JSON number (truncated) or a numeric
// string. Garbage at either tier falls through to the next.
func Int(root, envName, key string, def int) int {
	v, _ := intResolved(root, envName, key, def)
	return v
}

func intResolved(root, envName, key string, def int) (int, string) {
	parseEnv := func(raw any) (any, bool) {
		n, err := strconv.Atoi(raw.(string))
		return n, err == nil
	}
	parseFile := func(raw any) (any, bool) {
		switch v := raw.(type) {
		case float64:
			return int(v), true // JSON numbers decode as float64
		case string:
			n, err := strconv.Atoi(v)
			return n, err == nil
		}
		return nil, false
	}
	v, src := resolve(root, envName, key, def, parseEnv, parseFile)
	return v.(int), src
}

// Float64 resolves key with env > file > default. The env value must parse as
// a float; the file value may be a JSON number or a numeric string.
func Float64(root, envName, key string, def float64) float64 {
	v, _ := float64Resolved(root, envName, key, def)
	return v
}

func float64Resolved(root, envName, key string, def float64) (float64, string) {
	parseEnv := func(raw any) (any, bool) {
		f, err := strconv.ParseFloat(raw.(string), 64)
		return f, err == nil
	}
	parseFile := func(raw any) (any, bool) {
		switch v := raw.(type) {
		case float64:
			return v, true
		case string:
			f, err := strconv.ParseFloat(v, 64)
			return f, err == nil
		}
		return nil, false
	}
	v, src := resolve(root, envName, key, def, parseEnv, parseFile)
	return v.(float64), src
}

// Bool resolves key with env > file > default. Values use strconv.ParseBool
// semantics ("1", "t", "true", "TRUE", "0", "f", "false", "FALSE", ...) in
// both the env string and the file (where a JSON boolean is accepted too).
func Bool(root, envName, key string, def bool) bool {
	v, _ := boolResolved(root, envName, key, def)
	return v
}

func boolResolved(root, envName, key string, def bool) (bool, string) {
	parseEnv := func(raw any) (any, bool) {
		b, err := strconv.ParseBool(raw.(string))
		return b, err == nil
	}
	parseFile := func(raw any) (any, bool) {
		switch v := raw.(type) {
		case bool:
			return v, true
		case string:
			b, err := strconv.ParseBool(v)
			return b, err == nil
		}
		return nil, false
	}
	v, src := resolve(root, envName, key, def, parseEnv, parseFile)
	return v.(bool), src
}

// Duration resolves key with env > file > default. Values are Go duration
// strings ("30s", "5m", "1h30m") parsed with time.ParseDuration in both the
// env var and the file; a bare integer string is accepted as seconds,
// preserving the historical KERN_DEPLOY_TIMEOUT format. A JSON number in the
// file is treated as seconds too.
func Duration(root, envName, key string, def time.Duration) time.Duration {
	v, _ := durationResolved(root, envName, key, def)
	return v
}

func durationResolved(root, envName, key string, def time.Duration) (time.Duration, string) {
	parseEnv := func(raw any) (any, bool) { return parseDuration(raw.(string)) }
	parseFile := func(raw any) (any, bool) {
		switch v := raw.(type) {
		case string:
			return parseDuration(v)
		case float64:
			return time.Duration(v * float64(time.Second)), true
		}
		return nil, false
	}
	v, src := resolve(root, envName, key, def, parseEnv, parseFile)
	return v.(time.Duration), src
}

// parseDuration parses a Go duration string, falling back to bare seconds
// ("30" == 30 * time.Second) so the historical KERN_DEPLOY_TIMEOUT env format
// keeps working after the move to the shared getter.
func parseDuration(s string) (time.Duration, bool) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, true
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Duration(n) * time.Second, true
	}
	return 0, false
}

// StringMap resolves map[string]string with env > file > default. The env
// value keeps the historical "k=v,k=v" comma-separated form; an entry without
// "=" is keyed by itself ({"raw":"raw"}), preserving the bare-URL webhook and
// bare-path project conventions at the call sites. The file value is a JSON
// object; non-string values are skipped.
func StringMap(root, envName, key string, def map[string]string) map[string]string {
	v, _ := stringMapResolved(root, envName, key, def)
	return v
}

func stringMapResolved(root, envName, key string, def map[string]string) (map[string]string, string) {
	parseEnv := func(raw any) (any, bool) {
		m := map[string]string{}
		for _, e := range strings.Split(raw.(string), ",") {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			if i := strings.Index(e, "="); i > 0 {
				m[strings.TrimSpace(e[:i])] = strings.TrimSpace(e[i+1:])
			} else {
				m[e] = e
			}
		}
		return m, true
	}
	parseFile := func(raw any) (any, bool) {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, false
		}
		m := make(map[string]string, len(obj))
		for k, v := range obj {
			if s, ok := v.(string); ok {
				m[k] = s
			}
		}
		return m, true
	}
	v, src := resolve(root, envName, key, def, parseEnv, parseFile)
	return v.(map[string]string), src
}

// Strings resolves []string with env > file > default. The env value is a
// comma-separated list (entries trimmed, empty entries dropped); the file
// value is a JSON array of strings (same trimming). KERN_ROOTS' historical
// ':'-or-',' splitter is available via StringsSplit.
func Strings(root, envName, key string, def []string) []string {
	v, _ := stringsResolved(root, envName, key, def, splitComma)
	return v
}

// StringsSplit is Strings with a custom env-list splitter, for legacy formats
// such as KERN_ROOTS (':' or ',' separators). File values are JSON arrays
// regardless of the splitter.
func StringsSplit(root, envName, key string, def []string, split func(string) []string) []string {
	v, _ := stringsResolved(root, envName, key, def, split)
	return v
}

func stringsResolved(root, envName, key string, def []string, split func(string) []string) ([]string, string) {
	if split == nil {
		split = splitComma
	}
	parseEnv := func(raw any) (any, bool) {
		out := make([]string, 0, len(raw.(string)))
		for _, p := range split(raw.(string)) {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out, true
	}
	parseFile := func(raw any) (any, bool) {
		arr, ok := raw.([]any)
		if !ok {
			return nil, false
		}
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out, true
	}
	v, src := resolve(root, envName, key, def, parseEnv, parseFile)
	return v.([]string), src
}

// splitComma splits on commas, trimming entries and dropping empties.
func splitComma(s string) []string {
	var out []string
	for _, e := range strings.Split(s, ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// splitColonOrComma is the historical KERN_ROOTS splitter (':' or ',').
func splitColonOrComma(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == ',' })
}

// ---- registry (drives `kern config`) --------------------------------------

// Key describes one file-configurable key for the `kern config` command.
// Env-only knobs (secrets and safety toggles) are deliberately absent.
type Key struct {
	Key     string // dot-path config key, e.g. "llm.model"
	Env     string // primary env var, e.g. "KERN_MODEL"; "" when none maps 1:1
	Type    string // "string" | "int" | "float" | "bool" | "duration" | "stringmap" | "strings"
	Default any    // built-in default (for display)
	// Resolve overrides the default type switch for keys with non-trivial
	// env mapping (model roles, mcp roots). It returns (value, source).
	Resolve func(root string) (any, string)
}

// modelRoleEnvKeys pairs each llm.model_roles.* file key with its historical
// KERN_MODEL_<ROLE> env var.
var modelRoleEnvKeys = []struct{ env, key string }{
	{"KERN_MODEL_PLANNER", "llm.model_roles.planner"},
	{"KERN_MODEL_ARCHITECT", "llm.model_roles.architect"},
	{"KERN_MODEL_CODER", "llm.model_roles.coder"},
	{"KERN_MODEL_REVIEWER", "llm.model_roles.reviewer"},
	{"KERN_MODEL_SECURITY", "llm.model_roles.security"},
	{"KERN_MODEL_TESTER", "llm.model_roles.tester"},
	{"KERN_MODEL_SRE", "llm.model_roles.sre"},
	{"KERN_MODEL_DEFAULT", "llm.model_roles.default"},
}

// resolveModelRoles builds the effective per-role model map: each role is
// resolved independently (env > file > default) and the summary source is the
// highest tier any role hit.
func resolveModelRoles(root string) (any, string) {
	m := map[string]string{}
	src := "default"
	for _, e := range modelRoleEnvKeys {
		v, s := stringResolved(root, e.env, e.key, "")
		if v == "" {
			continue
		}
		role := e.key[strings.LastIndex(e.key, ".")+1:]
		m[role] = v
		switch s {
		case "env":
			src = "env"
		case "file":
			if src == "default" {
				src = "file"
			}
		}
	}
	return m, src
}

// resolveMCPRoots resolves mcp.roots from either historical env name
// (KERN_ROOTS wins over KERN_MCP_ROOTS) or the file.
func resolveMCPRoots(root string) (any, string) {
	if os.Getenv("KERN_ROOTS") != "" {
		v, s := stringsResolved(root, "KERN_ROOTS", "mcp.roots", []string{}, splitColonOrComma)
		return v, s
	}
	if os.Getenv("KERN_MCP_ROOTS") != "" {
		v, s := stringsResolved(root, "KERN_MCP_ROOTS", "mcp.roots", []string{}, splitComma)
		return v, s
	}
	v, s := stringsResolved(root, "", "mcp.roots", []string{}, splitComma)
	return v, s
}

// Registry is every file-configurable key, in display order. It powers
// `kern config`; the getters above are used directly by the migrated sites.
var Registry = []Key{
	{Key: "llm.provider", Env: "KERN_LLM_PROVIDER", Type: "string", Default: "ollama"},
	{Key: "llm.model", Env: "KERN_MODEL", Type: "string", Default: "llama3.2"},
	{Key: "llm.embed_model", Env: "KERN_EMBED_MODEL", Type: "string", Default: "nomic-embed-text"},
	{Key: "llm.model_roles", Env: "", Type: "stringmap", Default: map[string]string{}, Resolve: resolveModelRoles},
	{Key: "tokenizer", Env: "KERN_TOKENIZER", Type: "string", Default: "estimator"},
	{Key: "cost_per_token", Env: "KERN_COST_PER_TOKEN", Type: "float", Default: 0.00001},
	{Key: "cache.archive_days", Env: "KERN_CACHE_ARCHIVE_DAYS", Type: "float", Default: 7.0},
	{Key: "cache.ttl_days", Env: "KERN_CACHE_TTL_DAYS", Type: "float", Default: 30.0},
	{Key: "runtime.poll_interval", Env: "KERN_POLL_INTERVAL", Type: "duration", Default: 30 * time.Second},
	{Key: "runtime.prometheus_url", Env: "KERN_PROMETHEUS_URL", Type: "string", Default: ""},
	{Key: "runtime.otel_url", Env: "KERN_OTEL_URL", Type: "string", Default: ""},
	{Key: "runtime.k8s.api", Env: "KERN_K8S_API", Type: "string", Default: ""},
	{Key: "runtime.k8s.namespace", Env: "KERN_K8S_NAMESPACE", Type: "string", Default: ""},
	{Key: "deploy.command", Env: "KERN_DEPLOY_COMMAND", Type: "string", Default: ""},
	{Key: "deploy.timeout", Env: "KERN_DEPLOY_TIMEOUT", Type: "duration", Default: 5 * time.Minute},
	{Key: "exec.risk", Env: "KERN_EXEC_RISK", Type: "string", Default: "MEDIUM"},
	{Key: "mcp.roots", Env: "KERN_ROOTS", Type: "strings", Default: []string{}, Resolve: resolveMCPRoots},
	{Key: "webhooks", Env: "KERN_WEBHOOKS", Type: "stringmap", Default: map[string]string{}},
	{Key: "enterprise.projects", Env: "KERN_ENTERPRISE_PROJECTS", Type: "stringmap", Default: map[string]string{}},
}

// Effective returns the effective value and its source ("env", "file", or
// "default") for a registry key. It backs the `kern config` command.
func Effective(root string, k Key) (any, string) {
	if k.Resolve != nil {
		return k.Resolve(root)
	}
	switch k.Type {
	case "string":
		return stringResolved(root, k.Env, k.Key, k.Default.(string))
	case "int":
		return intResolved(root, k.Env, k.Key, k.Default.(int))
	case "float":
		return float64Resolved(root, k.Env, k.Key, k.Default.(float64))
	case "bool":
		return boolResolved(root, k.Env, k.Key, k.Default.(bool))
	case "duration":
		return durationResolved(root, k.Env, k.Key, k.Default.(time.Duration))
	case "stringmap":
		return stringMapResolved(root, k.Env, k.Key, k.Default.(map[string]string))
	case "strings":
		return stringsResolved(root, k.Env, k.Key, k.Default.([]string), splitComma)
	}
	return k.Default, "default"
}
