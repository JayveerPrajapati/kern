package verification

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// manifestCheck is the outcome of verifying a project's declared dependencies
// across supported ecosystems (Go, Node, Python, Maven, Rust). It is
// deterministic, stdlib-only and runs fully in-process (no package manager
// subprocess and no network).
type manifestCheck struct {
	// ecosystem names the manifests that were checked ("go", "node+python",
	// ...) — empty when nothing was found.
	ecosystem string
	// ok is false when a manifest was found but could not be parsed (or a Go
	// module check failed to run). The caller treats that as fail-closed: it
	// must not fabricate a PASS.
	ok bool
	// skipped is true when no supported manifest exists at all. The caller
	// reports an honest skip (nothing to verify) — not a fabricated PASS.
	skipped     bool
	skippedNote string
	findings    []string
}

// supportedManifests is the ordered probe list used by checkManifestDeps.
// Order matters only for the ecosystem label when several manifests exist.
var supportedManifests = []string{
	"go.mod", "package.json", "requirements.txt", "pom.xml", "Cargo.toml",
}

// checkManifestDeps verifies a project's declared dependencies by parsing
// every supported manifest that is present:
//   - go.mod (Go): existing import-coverage + duplicate-require semantics
//     (see checkModuleDeps).
//   - package.json (Node): direct dependencies + devDependencies must carry a
//     pinned version ("*" / "latest" / missing is unpinned).
//   - requirements.txt (Python): a bare package name without a version
//     specifier is unpinned.
//   - pom.xml (Maven): each <dependency> must declare a <version>.
//   - Cargo.toml (Rust): each dependency must carry a version (path/git deps
//     are location-pinned and exempt).
//
// When a manifest exists but cannot be parsed the check is fail-closed (ok
// false). When NO supported manifest exists the result is an honest skip, so
// a non-Go project is never reported as a FAIL simply because it lacks
// go.mod.
func checkManifestDeps(root string) *manifestCheck {
	mc := &manifestCheck{ok: true}
	any := false
	for _, name := range supportedManifests {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		any = true
		switch name {
		case "go.mod":
			md := checkModuleDeps(root)
			mc.findings = append(mc.findings, md.findings...)
			if !md.ok {
				mc.ok = false
			}
			mc.ecosystem = joinEcosystem(mc.ecosystem, "go")
		case "package.json":
			nd := checkNodeDeps(path)
			mc.findings = append(mc.findings, nd.findings...)
			if !nd.ok {
				mc.ok = false
			}
			mc.ecosystem = joinEcosystem(mc.ecosystem, "node")
		case "requirements.txt":
			pd := checkPythonDeps(path)
			mc.findings = append(mc.findings, pd.findings...)
			if !pd.ok {
				mc.ok = false
			}
			mc.ecosystem = joinEcosystem(mc.ecosystem, "python")
		case "pom.xml":
			vd := checkMavenDeps(path)
			mc.findings = append(mc.findings, vd.findings...)
			if !vd.ok {
				mc.ok = false
			}
			mc.ecosystem = joinEcosystem(mc.ecosystem, "maven")
		case "Cargo.toml":
			rd := checkRustDeps(path)
			mc.findings = append(mc.findings, rd.findings...)
			if !rd.ok {
				mc.ok = false
			}
			mc.ecosystem = joinEcosystem(mc.ecosystem, "rust")
		}
	}
	if !any {
		return &manifestCheck{skipped: true, skippedNote: "no supported dependency manifest (" + strings.Join(supportedManifests, ", ") + ")"}
	}
	sort.Strings(mc.findings)
	return mc
}

// joinEcosystem appends name to a comma-separated ecosystem label.
func joinEcosystem(cur, name string) string {
	if cur == "" {
		return name
	}
	return cur + "+" + name
}

// depFindings is a parse outcome for a non-Go manifest.
type depFindings struct {
	ok       bool
	findings []string
}

// ---------------------------------------------------------------------------
// Node (package.json)

type nodeManifest struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func checkNodeDeps(path string) *depFindings {
	data, err := os.ReadFile(path)
	if err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (package.json): " + err.Error()}}
	}
	var m nodeManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (package.json): " + err.Error()}}
	}
	var findings []string
	seen := map[string]string{} // name -> where it appeared
	check := func(name, ver, where string) {
		v := strings.TrimSpace(ver)
		if v == "" || v == "*" || v == "latest" {
			findings = append(findings, "unpinned dependency "+name+" ("+where+")")
		}
		if prev, dup := seen[name]; dup && prev != where {
			findings = append(findings, "duplicate dependency "+name+" ("+prev+" and "+where+")")
		} else {
			seen[name] = where
		}
	}
	for name, ver := range m.Dependencies {
		check(name, ver, "dependencies")
	}
	for name, ver := range m.DevDependencies {
		check(name, ver, "devDependencies")
	}
	sort.Strings(findings)
	return &depFindings{ok: true, findings: findings}
}

// ---------------------------------------------------------------------------
// Python (requirements.txt)

func checkPythonDeps(path string) *depFindings {
	f, err := os.Open(path)
	if err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (requirements.txt): " + err.Error()}}
	}
	defer func() { _ = f.Close() }()

	var findings []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "--") {
			continue // comments, options (-r/-e/-i/--hash etc.)
		}
		name, pinned := splitRequirement(line)
		if name == "" {
			continue
		}
		if !pinned {
			findings = append(findings, "unpinned dependency "+name)
		}
		if seen[name] {
			findings = append(findings, "duplicate dependency "+name)
		}
		seen[name] = true
	}
	if err := sc.Err(); err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (requirements.txt): " + err.Error()}}
	}
	sort.Strings(findings)
	return &depFindings{ok: true, findings: findings}
}

// splitRequirement splits a requirements.txt line into (normalized name,
// pinned). A specifier of any form (==, >=, <=, ~=, !=, <, >) counts as
// pinned; a bare name is unpinned. Extras ([x]) are stripped from the name.
func splitRequirement(line string) (string, bool) {
	// Drop inline comments.
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	name := line
	pinned := false
	for _, op := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", "="} {
		if i := strings.Index(line, op); i >= 0 {
			name = line[:i]
			pinned = true
			break
		}
	}
	name = strings.TrimSpace(name)
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	name = strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	return name, pinned
}

// ---------------------------------------------------------------------------
// Maven (pom.xml)

type pomProject struct {
	Dependencies []pomDependency `xml:"dependencies>dependency"`
}

type pomDependency struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

func checkMavenDeps(path string) *depFindings {
	data, err := os.ReadFile(path)
	if err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (pom.xml): " + err.Error()}}
	}
	var p pomProject
	if err := xml.Unmarshal(data, &p); err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (pom.xml): " + err.Error()}}
	}
	var findings []string
	seen := map[string]bool{}
	for _, d := range p.Dependencies {
		id := strings.TrimSpace(d.GroupID) + ":" + strings.TrimSpace(d.ArtifactID)
		if id == ":" {
			continue
		}
		if strings.TrimSpace(d.Version) == "" {
			findings = append(findings, "unpinned dependency "+id)
		}
		if seen[id] {
			findings = append(findings, "duplicate dependency "+id)
		}
		seen[id] = true
	}
	sort.Strings(findings)
	return &depFindings{ok: true, findings: findings}
}

// ---------------------------------------------------------------------------
// Rust (Cargo.toml) — heuristic line parser, no TOML dependency.

func checkRustDeps(path string) *depFindings {
	f, err := os.Open(path)
	if err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (Cargo.toml): " + err.Error()}}
	}
	defer func() { _ = f.Close() }()

	var findings []string
	seen := map[string]bool{}
	section := ""
	subDep := "" // dependency name when inside a [dependencies.X] sub-table
	subHasVersion := false
	noteSubTable := func() {
		if subDep != "" && !subHasVersion {
			findings = append(findings, "unpinned dependency "+subDep)
		}
		subDep = ""
		subHasVersion = false
	}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "["):
			noteSubTable()
			section = strings.Trim(line, "[] ")
			// A [dependencies.foo] sub-table names a single dependency.
			if strings.HasPrefix(section, "dependencies.") {
				subDep = strings.TrimPrefix(section, "dependencies.")
			}
			continue
		}
		if !isDepsSection(section) && subDep == "" {
			continue
		}
		if subDep != "" {
			if rest, ok := cutKey(line, "version"); ok && strings.TrimSpace(rest) != "" {
				subHasVersion = true
			}
			continue
		}
		name, kind, ok := splitCargoDep(line)
		if !ok {
			continue
		}
		if kind == "" { // bare inline table without version/path/git
			findings = append(findings, "unpinned dependency "+name)
		}
		if seen[name] {
			findings = append(findings, "duplicate dependency "+name)
		}
		seen[name] = true
	}
	noteSubTable()
	if err := sc.Err(); err != nil {
		return &depFindings{ok: false, findings: []string{"dependency check could not run (Cargo.toml): " + err.Error()}}
	}
	sort.Strings(findings)
	return &depFindings{ok: true, findings: findings}
}

// isDepsSection reports whether a TOML section holds direct dependencies.
// Workspace dependency tables are shared declarations, not direct deps.
func isDepsSection(section string) bool {
	switch section {
	case "dependencies", "dev-dependencies", "build-dependencies":
		return true
	}
	if strings.HasPrefix(section, "workspace.") {
		return false
	}
	return strings.HasSuffix(section, ".dependencies") // e.g. [target.'cfg(unix)'.dependencies]
}

// splitCargoDep parses one dependency line in a deps section into
// (name, kind). kind is "" (unpinned), "version", "path" or "git"; the latter
// two are location-pinned and exempt from the unpinned finding.
func splitCargoDep(line string) (string, string, bool) {
	name, rest, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	name = strings.TrimSpace(name)
	rest = strings.TrimSpace(rest)
	if name == "" || name == "version" {
		return "", "", false
	}
	val := strings.Trim(rest, "\"")
	if strings.HasPrefix(val, "{") {
		inner := strings.Trim(val, "{} ")
		if strings.Contains(inner, "version") {
			return name, "version", true
		}
		if strings.Contains(inner, "path") {
			return name, "path", true
		}
		if strings.Contains(inner, "git") {
			return name, "git", true
		}
		return name, "", true // bare table: no version/path/git
	}
	if val == "" {
		return name, "", true
	}
	return name, "version", true
}

// cutKey returns the trimmed value of a `key = value` line, or ok=false.
func cutKey(line, key string) (string, bool) {
	k, v, ok := strings.Cut(line, "=")
	if !ok || strings.TrimSpace(k) != key {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(v), "\""), true
}
