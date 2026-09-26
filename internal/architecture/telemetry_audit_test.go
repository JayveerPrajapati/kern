package architecture

// TestNoNetworkInCorePackages turns kern's "zero telemetry. It stays local."
// claim into a CI-enforced, machine-checkable gate: no core (non-network-by-
// design) internal package may gain outbound network or telemetry capability
// without an explicit, justified allowlist entry.
//
// Method — static import scan:
//   - walk every non-test .go file under internal/ (recursively),
//   - parse with go/parser and fail when a file imports anything that can
//     open a socket, make an HTTP request, resolve a name, or egress:
//       * "net/http"                                  (client OR server capability)
//       * "net" when the file uses egress-capable symbols
//         (net.Dial*, net.Listen*, net.Conn, net.Lookup*, net.Resolver, ...).
//         Parsing-only uses (net.ParseIP, net.SplitHostPort, net.JoinHostPort,
//         net.ParseCIDR, net.IP, net.IPNet) are allowed, same spirit as the
//         net/url carve-out: pure parsing is not telemetry.
//       * stdlib net/* subprotocols that egress (net/smtp, net/rpc,
//         net/textproto); net/url and net/mail are pure parsing and allowed.
//       * third-party deps whose import path smells like a network client
//         (http/grpc/websocket/mqtt/... tokens). None exist in go.mod today;
//         this is a defensive net for future dependencies.
//       * "os/exec" when an exec.Command/CommandContext/LookPath call passes a
//         string argument naming curl or wget (dynamic binaries are a
//         documented blind spot; the KERN_ALLOW_* fail-closed gates cover
//         them at runtime).
//   - packages whose network use is by design (LLM providers, HTTP server,
//     MCP loopback, webhook delivery, ...) live in networkAllowlist below;
//     each entry is a subtree root, so allowlisting internal/llm exempts
//     internal/llm/** from the walk.
//
// Known limitations (documented in docs/benchmarks/telemetry-audit.md):
//   - build tags are ignored: every .go file on disk is parsed regardless of
//     its build constraints, so a network import hidden behind a tag is
//     still caught (fail-closed, not fail-open);
//   - a local identifier shadowing the "net"/"exec" package name could in
//     theory dodge the symbol checks — accepted for a static audit;
//   - os/exec with a binary name built dynamically (var bin = "curl") cannot
//     be proven statically; runtime gates (KERN_ALLOW_UNISOLATED / fail-closed
//     unshare --net) are the backstop.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// networkAllowlist is the set of internal package ROOTS that are
// network-by-design and therefore exempt from the zero-egress audit. Each
// entry is a subtree root: allowlisting internal/llm exempts internal/llm/**
// from the walk. Keep sorted. Add an entry ONLY when the package's network
// use is its actual job (HTTP I/O in either direction), and justify it with
// one line in docs/benchmarks/telemetry-audit.md.
var networkAllowlist = []string{
	"internal/enterprise",   // enterprise auth + org API (outbound and serving)
	"internal/fetch",        // outbound HTTP fetcher for pages/documents
	"internal/llm",          // outbound LLM provider clients (OpenAI/Anthropic/Google/Ollama)
	"internal/mcp",          // MCP loopback HTTP server (+ transport TLS loopback listener, doc uses fetch)
	"internal/mcpclient",    // outbound MCP client
	"internal/orgapprovals", // org approvals REST surface (HTTP serving, mounted on the enterprise org API)
	"internal/prprovider",   // PR-provider API clients (GitHub)
	"internal/relay",        // peer relay networking
	"internal/resilience",   // chaos/injection scenarios: LOCAL loopback http servers simulate network failures (no outbound egress)
	"internal/runtime",      // live.go: LivePrometheusSource/LiveOtelSource/LiveKubernetesSource poll remote endpoints
	"internal/sdk",          // outbound kern-server REST client
	"internal/web",          // local HTTP server (dashboard)
	"internal/webhook",      // outbound delivery of eventbus events to registered webhook URLs
}

// netEgressSyms are package net symbols that open sockets, accept
// connections, resolve names, or enumerate interfaces — i.e. anything that
// can leave the machine or bind a port. Parsing-only helpers (ParseIP,
// SplitHostPort, JoinHostPort, ParseCIDR) and address TYPES (IP, IPNet,
// TCPAddr, ...) are deliberately NOT in this set.
var netEgressSyms = map[string]bool{
	"Dial": true, "DialTimeout": true, "Dialer": true,
	"DialTCP": true, "DialUDP": true, "DialUnix": true, "DialIP": true, "DialTLS": true,
	"Listen": true, "ListenPacket": true,
	"ListenTCP": true, "ListenUDP": true, "ListenUnix": true, "ListenIP": true,
	"Listener": true, "Conn": true, "PacketConn": true,
	"FileConn": true, "FilePacketConn": true, "FileListener": true,
	"LookupHost": true, "LookupIP": true, "LookupPort": true, "LookupCNAME": true,
	"LookupAddr": true, "LookupMX": true, "LookupNS": true, "LookupTXT": true, "LookupSRV": true,
	"Resolver": true, "DefaultResolver": true,
	"Interface": true, "Interfaces": true, "InterfaceAddrs": true,
	"InterfaceByIndex": true, "InterfaceByName": true,
}

// netSubprotocolsEgress are stdlib net/* subpackages that egress. net/url and
// net/mail are pure parsing and remain allowed.
var netSubprotocolsEgress = []string{
	"net/smtp", "net/rpc", "net/textproto",
}

// execEgressBinaryRe matches curl/wget inside a string literal passed to an
// os/exec call (either as the binary itself or inside a shell -c string).
var execEgressBinaryRe = regexp.MustCompile(`(?i)\b(curl|wget)\b`)

// thirdPartyNetTokenRe flags third-party import paths that look like network
// clients. No current dependency matches; this guards future additions.
var thirdPartyNetTokenRe = regexp.MustCompile(`(?i)(http|grpc|websocket|mqtt|amqp|smtp|imap|ldap|socket|sse|ssh|rpc|tls|dns|client|quic)`)

// auditFinding is one rule violation: a repo-root-relative file, a line, and
// the reason.
type auditFinding struct {
	file string
	line int
	msg  string
}

func (f auditFinding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.file, f.line, f.msg)
}

func TestNoNetworkInCorePackages(t *testing.T) {
	root := repoRoot(t)
	internalDir := filepath.Join(root, "internal")

	allow := map[string]bool{}
	for _, a := range networkAllowlist {
		allow[a] = true
	}

	var findings []auditFinding

	err := filepath.WalkDir(internalDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == internalDir {
				return nil
			}
			rel, rerr := filepath.Rel(internalDir, p)
			if rerr != nil {
				return rerr
			}
			first := strings.Split(rel, string(filepath.Separator))[0]
			if allow["internal/"+first] {
				return filepath.SkipDir // allowlisted root: whole subtree exempt
			}
			// Vendored / foreign / sandbox-module trees are never audited.
			if d.Name() == "vendor" || strings.Contains(rel, ".kern") ||
				strings.Contains(rel, "sandboxes"+string(filepath.Separator)+"loop") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		auditFile(internalDir, p, &findings)
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if len(findings) > 0 {
		var b strings.Builder
		b.WriteString("zero-telemetry audit: network capability found in core (non-network-by-design) packages:\n")
		for _, f := range findings {
			b.WriteString("  " + f.String() + "\n")
		}
		b.WriteString("If a package needs network by design, add it to networkAllowlist in telemetry_audit_test.go\n")
		b.WriteString("and justify it in docs/benchmarks/telemetry-audit.md.\n")
		t.Fatal(b.String())
	}
}

// auditFile parses one non-test .go file under internal/ and appends a
// finding for every rule violation. rel is repo-root-relative for messages.
func auditFile(internalDir, path string, findings *[]auditFinding) {
	rel, err := filepath.Rel(internalDir, path)
	if err != nil {
		*findings = append(*findings, auditFinding{file: path, msg: "rel: " + err.Error()})
		return
	}
	rel = filepath.ToSlash(rel)
	fset := token.NewFileSet()
	b, err := os.ReadFile(path)
	if err != nil {
		*findings = append(*findings, auditFinding{file: rel, msg: "read: " + err.Error()})
		return
	}

	// Fast pass: imports only.
	f, err := parser.ParseFile(fset, path, b, parser.ImportsOnly|parser.SkipObjectResolution)
	if err != nil {
		*findings = append(*findings, auditFinding{file: rel, msg: "parse: " + err.Error()})
		return
	}
	imports := map[string]*ast.ImportSpec{}
	for _, imp := range f.Imports {
		imports[strings.Trim(imp.Path.Value, `"`)] = imp
	}

	// Rule 1: net/http — client or server capability; core packages have no
	// business with either.
	if imp, ok := imports["net/http"]; ok {
		*findings = append(*findings, auditFinding{
			file: rel, line: fset.Position(imp.Pos()).Line,
			msg: `imports "net/http" (HTTP client/server capability)`,
		})
	}

	// Rule 2: stdlib net/* subprotocols that egress.
	for _, sub := range netSubprotocolsEgress {
		if imp, ok := imports[sub]; ok {
			*findings = append(*findings, auditFinding{
				file: rel, line: fset.Position(imp.Pos()).Line,
				msg: fmt.Sprintf("imports %q (network egress subprotocol)", sub),
			})
		}
	}

	// Rule 3: third-party dependencies that smell like network clients.
	for path := range imports {
		if isStdlib(path) || strings.HasPrefix(path, moduleRoot) {
			continue
		}
		if thirdPartyNetTokenRe.MatchString(path) {
			*findings = append(*findings, auditFinding{
				file: rel, line: fset.Position(imports[path].Pos()).Line,
				msg: fmt.Sprintf("imports %q — third-party dependency that looks like a network client; no such dep is allowed in core", path),
			})
		}
	}

	// Rules 4 & 5 need the AST body (net symbol usage, exec call arguments):
	// re-parse fully only when the file imports net or os/exec.
	if imports["net"] == nil && imports["os/exec"] == nil {
		return
	}
	full, err := parser.ParseFile(fset, path, b, parser.SkipObjectResolution)
	if err != nil {
		*findings = append(*findings, auditFinding{file: rel, msg: "full parse: " + err.Error()})
		return
	}

	// Rule 4: raw "net" is only acceptable for parsing-only helpers. Any
	// egress-capable symbol (Dial/Listen/Conn/Lookup/Resolver/...) fails.
	if imports["net"] != nil {
		ast.Inspect(full, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "net" || !netEgressSyms[sel.Sel.Name] {
				return true
			}
			*findings = append(*findings, auditFinding{
				file: rel, line: fset.Position(sel.Pos()).Line,
				msg: fmt.Sprintf("uses net.%s — egress-capable network symbol; only parsing-only net helpers (ParseIP, SplitHostPort, ...) are allowed in core packages", sel.Sel.Name),
			})
			return true
		})
	}

	// Rule 5: os/exec is fine for running local binaries, but not for shelling
	// out to curl/wget.
	if imports["os/exec"] != nil {
		ast.Inspect(full, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "exec" {
				return true
			}
			switch sel.Sel.Name {
			case "Command", "CommandContext", "LookPath":
			default:
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if execEgressBinaryRe.MatchString(lit.Value) {
					*findings = append(*findings, auditFinding{
						file: rel, line: fset.Position(call.Pos()).Line,
						msg: fmt.Sprintf("exec.%s passes %s — shelling out to a network tool from a core package", sel.Sel.Name, lit.Value),
					})
					return false
				}
			}
			return true
		})
	}
}

// isStdlib reports whether an import path is in the Go standard library,
// using the first-path-segment heuristic (a dot in the first segment marks
// a third-party module).
func isStdlib(path string) bool {
	first := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}
	return !strings.Contains(first, ".")
}
