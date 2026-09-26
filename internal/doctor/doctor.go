// Package doctor runs diagnostics: agent wiring, index health, Ollama
// reachability and stats writability. It reports ok/warn/fail per check.
package doctor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/script"
	"github.com/JayveerPrajapati/kern/internal/setup"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/version"
)

// Finding is a single diagnostic result.
type Finding struct {
	Check  string `json:"check"`
	Level  string `json:"level"` // ok, warn, fail
	Detail string `json:"detail"`
}

// Run executes all checks against the current project root.
func Run(root string) []Finding {
	var out []Finding
	out = append(out, checkBinary())
	out = append(out, checkVersion())
	out = append(out, checkParity(root))
	out = append(out, checkCapabilities())
	out = append(out, checkPath())
	out = append(out, checkExec(setup.Bin()))
	out = append(out, checkNetworkIsolation())
	out = append(out, checkEnv())
	out = append(out, checkConfig(root))
	out = append(out, checkCache())
	out = append(out, checkSandboxes(root))
	out = append(out, checkWiring(root)...)
	out = append(out, checkPluginSync()...)
	out = append(out, checkGitExclude(root))
	out = append(out, checkIndex(root))
	out = append(out, checkIndexFreshness(root))
	out = append(out, checkIndexShadow(root))
	out = append(out, checkPrecision(root))
	out = append(out, checkRuntime(root))
	out = append(out, checkOllama())
	out = append(out, checkStats())
	return out
}

func checkBinary() Finding {
	exe, err := os.Executable()
	if err != nil {
		return Finding{Check: "binary", Level: "fail", Detail: err.Error()}
	}
	return Finding{Check: "binary", Level: "ok", Detail: exe}
}

// checkCapabilities reports which optional build-time capabilities are
// compiled into this binary (SQLite persistence, tree-sitter extraction).
func checkCapabilities() Finding {
	var parts []string
	if index.SQLiteEnabled() {
		parts = append(parts, "sqlite: on (persistent index + FTS5)")
	} else {
		parts = append(parts, "sqlite: off (in-memory; build with -tags sqlite)")
	}
	if index.TreesitterEnabled() {
		parts = append(parts, "treesitter: on (13 grammars)")
	} else {
		parts = append(parts, "treesitter: off (regex fallback; build with -tags treesitter)")
	}
	return Finding{Check: "capabilities", Level: "ok", Detail: strings.Join(parts, " · ")}
}

func checkPath() Finding {
	bin := setup.Bin()
	if _, err := os.Stat(bin); err == nil {
		return Finding{Check: "kern-mcp", Level: "ok", Detail: bin}
	}
	return Finding{Check: "kern-mcp", Level: "warn", Detail: bin + " missing — agents may not find it"}
}

// mcpInitializeRequest is the JSON-RPC initialize request doctor writes to
// the binary's stdin to prove it speaks MCP — a real handshake, not a usage
// echo. 2024-11-05 is the protocol version most stdio MCP servers answer.
const mcpInitializeRequest = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"kern-doctor","version":"1"}}}` + "\n"

// checkExec actually runs the binary instead of trusting os.Stat and proves
// it speaks MCP: it starts kern-mcp, writes one JSON-RPC initialize request
// line to stdin, closes it, reads the first stdout line, and passes ONLY
// when that line parses as a JSON response carrying a "jsonrpc" field and a
// "result" (the server's capabilities). On macOS an unsigned/ad-hoc-broken
// binary passes os.Stat but is killed by Gatekeeper with SIGKILL; executing
// it surfaces that immediately (killedBySIGKILL decodes both the direct
// signal-death form and the shell-wrapped 137 form).
func checkExec(bin string) Finding {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Stdin = strings.NewReader(mcpInitializeRequest)
	out, err := cmd.Output()
	if err != nil {
		// A deadline expiry kills the child with SIGKILL too, so the timeout
		// must be diagnosed before the Gatekeeper branch: a binary that
		// starts but never answers the handshake is not a Gatekeeper victim.
		if ctx.Err() == context.DeadlineExceeded {
			return Finding{Check: "binary-exec", Level: "fail", Detail: bin + " did not answer the MCP initialize handshake within 5s"}
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && killedBySIGKILL(ee) {
			return Finding{Check: "binary-exec", Level: "fail", Detail: bin + " was killed with SIGKILL — on macOS this is Gatekeeper/codesign; re-sign with `codesign --force --sign -` or reinstall"}
		}
		return Finding{Check: "binary-exec", Level: "fail", Detail: bin + " failed to run: " + err.Error()}
	}
	server, ok := parseMCPInitializeResponse(out)
	if !ok {
		return Finding{Check: "binary-exec", Level: "fail", Detail: bin + " did not answer the MCP initialize handshake (first stdout line is not a JSON-RPC result)"}
	}
	detail := "kern-mcp handshake ok"
	if server != "" {
		detail += " (server: " + server + ")"
	}
	return Finding{Check: "binary-exec", Level: "ok", Detail: bin + " " + detail}
}

// parseMCPInitializeResponse reports whether the first line of out is a
// JSON-RPC response carrying a non-empty "jsonrpc" field and a "result", and
// returns a "name/version" server string when the result names one.
func parseMCPInitializeResponse(out []byte) (server string, ok bool) {
	line := out
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		line = out[:i]
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", false
	}
	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		Result  *struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", false
	}
	if resp.JSONRPC == "" || resp.Result == nil {
		return "", false
	}
	name := resp.Result.ServerInfo.Name
	ver := resp.Result.ServerInfo.Version
	if name == "" {
		return "", true
	}
	if ver == "" {
		return name, true
	}
	return name + "/" + ver, true
}

// killedBySIGKILL reports whether the command died from SIGKILL. A direct
// exec sees a signal death as ExitCode -1 with err "signal: killed" — the
// 137 exit form only appears when a shell wraps the child. Both point at
// Gatekeeper (macOS) or an OOM kill, so check the WaitStatus signal, not
// just the exit code.
func killedBySIGKILL(ee *exec.ExitError) bool {
	if ee.ExitCode() == 137 {
		return true
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL
}

// checkNetworkIsolation reports whether script runs can be network-isolated on
// this host. macOS and Windows lack unprivileged user/network namespaces, so
// script execution fails closed there unless the operator explicitly opts in
// via KERN_ALLOW_UNISOLATED=1 (or the alias KERN_ALLOW_NET=1); this check
// reports that honestly instead of implying isolation is always available.
func checkNetworkIsolation() Finding {
	if script.NetworkIsolationAvailable() {
		backend := "Linux unshare --user --map-root-user --net"
		if goruntime.GOOS == "darwin" {
			backend = "macOS Apple Seatbelt (/usr/bin/sandbox-exec)"
		}
		return Finding{Check: "network-isolation", Level: "ok",
			Detail: fmt.Sprintf("network isolation: available (%s)", backend)}
	}
	return Finding{Check: "network-isolation", Level: "warn",
		Detail: fmt.Sprintf("network isolation: unavailable (%s) — scripts fail closed unless KERN_ALLOW_UNISOLATED=1 (or KERN_ALLOW_NET=1) is set", goruntime.GOOS)}
}

func checkEnv() Finding {
	// D5: echo every KERN_* variable (sorted, redacted values stay intact —
	// these are config toggles, not secrets) and validate the known ones.
	var kernVars []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "KERN_") {
			kernVars = append(kernVars, kv)
		}
	}
	sort.Strings(kernVars)

	var warns []string
	for _, kv := range kernVars {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == "KERN_LLM_PROVIDER":
			if v != "" && v != "ollama" && v != "openai" && v != "anthropic" && v != "google" {
				warns = append(warns, k+"="+v+" (unknown provider)")
			}
		case k == "KERN_MCP_WATCH":
			if v != "0" && v != "1" {
				warns = append(warns, k+"="+v+" (want 0|1)")
			}
		case k == "KERN_MCP_WATCH_INTERVAL":
			if n, err := strconv.Atoi(v); err != nil || n <= 0 {
				warns = append(warns, k+"="+v+" (want a positive integer)")
			}
		case k == "KERN_ALLOW_EXEC" || k == "KERN_ALLOW_DEPLOY" || k == "KERN_ALLOW_UNISOLATED" || k == "KERN_ALLOW_NET" || k == "KERN_REQUIRE_BINARY":
			if v != "1" {
				warns = append(warns, k+"="+v+" (fail-closed toggle: want 1)")
			}
		}
	}

	extra := ""
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		extra = "XDG_CACHE_HOME=" + x
	}
	if o := os.Getenv("OLLAMA_HOST"); o != "" {
		if extra != "" {
			extra += " · "
		}
		extra += "OLLAMA_HOST=" + o
	}

	detail := strings.Join(kernVars, " · ")
	if detail == "" {
		detail = "no KERN_* variables set"
	}
	if extra != "" {
		detail += " · " + extra
	}
	lvl := "ok"
	if len(warns) > 0 {
		lvl = "warn"
		detail += " | " + strings.Join(warns, " | ")
	}
	return Finding{Check: "env", Level: lvl, Detail: detail}
}

// checkVersion reports the binary version stamp and runtime. An unstamped
// ("dev") build is a warn: release workflows stamp via version.Adopt.
func checkVersion() Finding {
	v := version.Version
	lvl := "ok"
	if v == "dev" || v == "" {
		lvl = "warn"
	}
	return Finding{
		Check:  "version",
		Level:  lvl,
		Detail: fmt.Sprintf("kern %s · %s · %s/%s", v, goruntime.Version(), goruntime.GOOS, goruntime.GOARCH),
	}
}

// checkParity detects a stale installed binary: when the binary is stamped
// with a build commit, it must match the repo HEAD it is run against
// (north-star NS-7). An unstamped ("dev") build cannot prove parity and
// reports the stamping incantation instead of failing.
func checkParity(root string) Finding {
	v := version.Version
	if v == "dev" || v == "" {
		return Finding{
			Check:  "parity",
			Level:  "warn",
			Detail: "unstamped binary (version=dev): build with -ldflags \"-X github.com/JayveerPrajapati/kern/internal/version.Version=$(git rev-parse HEAD)\" to enable build-vs-repo parity",
		}
	}
	head := gitHead(root)
	if head == "" {
		return Finding{Check: "parity", Level: "warn", Detail: fmt.Sprintf("binary stamped %s but %s is not a git checkout — cannot compare", v, root)}
	}
	if v == head || strings.HasPrefix(head, v) {
		return Finding{Check: "parity", Level: "ok", Detail: fmt.Sprintf("binary build %s matches repo HEAD %s", v, head)}
	}
	// A release tag build (vX.Y.Z) is a legitimate artifact built from a
	// tagged commit; the parity check cannot map the tag to a HEAD hash, so
	// release-provenance stamps are accepted rather than falsely reported
	// stale. The classification is the canonical kversion.Provenance (the same
	// classifier `kern update` and bpreceipt use) — release-shaped stamps
	// include 4-component tags like v0.9.9.1.
	if version.Provenance(v) == version.ProvenanceRelease {
		return Finding{Check: "parity", Level: "ok", Detail: fmt.Sprintf("binary is a release build (%s); repo HEAD is %s", v, head)}
	}
	return Finding{
		Check:  "parity",
		Level:  "warn",
		Detail: fmt.Sprintf("binary build %s differs from repo HEAD %s — installed binary is stale; rebuild and reinstall", v, head),
	}
}

// gitHead returns the repo HEAD commit short hash, or "" when root is not a
// git checkout.
func gitHead(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// checkConfig validates .kern/config.json: a present-but-malformed file is a
// fail (it silently degrades every config lookup to defaults), and known
// verify.* keys must be strings.
func checkConfig(root string) Finding {
	path := filepath.Join(root, ".kern", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Finding{Check: "config", Level: "ok", Detail: "no .kern/config.json (defaults)"}
		}
		return Finding{Check: "config", Level: "fail", Detail: err.Error()}
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Finding{Check: "config", Level: "fail", Detail: fmt.Sprintf("%s: %v", path, err)}
	}
	var warns []string
	// verify.* keys live under the nested "verify" object (config package
	// resolves dot-separated paths like "verify.build").
	var verify map[string]any
	if v, ok := raw["verify"]; ok {
		if m, ok := v.(map[string]any); ok {
			verify = m
		} else {
			log.Printf("doctor: config field \"verify\" has type %T, want object — ignoring verify.* checks", v)
		}
	}
	for _, k := range []string{"build", "test", "lint"} {
		if v, ok := verify[k]; ok {
			if _, isStr := v.(string); !isStr {
				warns = append(warns, "verify."+k+" must be a string")
			}
		}
	}
	if len(warns) > 0 {
		return Finding{Check: "config", Level: "warn", Detail: strings.Join(warns, "; ")}
	}
	return Finding{Check: "config", Level: "ok", Detail: path + " (valid JSON)"}
}

// checkCache scans the kern cache directory for corruption markers: any
// zero-byte JSON file (a truncated write) is a fail, and the total file count
// is reported so operators can see how much cache has accumulated.
func checkCache() Finding {
	dir := cache.Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Finding{Check: "cache", Level: "ok", Detail: "no cache directory yet"}
	}
	var files, zeroJSON int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		files++
		if strings.HasSuffix(e.Name(), ".json") && e.Type().IsRegular() {
			if info, ierr := e.Info(); ierr == nil && info.Size() == 0 {
				zeroJSON++
			}
		}
	}
	if zeroJSON > 0 {
		return Finding{Check: "cache", Level: "fail", Detail: fmt.Sprintf("%d zero-byte JSON files under %s — truncated writes; consider clearing the cache", zeroJSON, dir)}
	}
	return Finding{Check: "cache", Level: "ok", Detail: fmt.Sprintf("%d files under %s", files, dir)}
}

func checkWiring(root string) []Finding {
	return wiringFindings(setup.Check(root))
}

// wiringFindings renders setup.Status entries as doctor findings with three
// doctor-side cosmetics (F13): same-named statuses are merged into one line
// (setup can report the same logical agent twice — e.g. "opencode plugin
// (global)" for each global plugin location), a "registered" claim is only
// trusted when the agent is actually detected, and a "(detected) not
// present" note is reworded so it does not read as "agent not installed".
func wiringFindings(sts []setup.Status) []Finding {
	// Which agents did the detection pass actually find? A "registered"
	// claim from a config-presence check is only trustworthy when the agent
	// itself is present: pairing "[ok] claude MCP registered" with "[warn]
	// claude (detected) not present" reads as a contradiction, so the
	// registration claim must not be made for an undetected agent.
	detected := map[string]bool{}
	for _, s := range sts {
		if name, ok := strings.CutSuffix(s.Agent, " (detected)"); ok {
			detected[name] = true
		}
	}

	// Merge same-named statuses: collapse them into one finding whose detail
	// carries the distinct notes and paths, instead of printing the same
	// line twice.
	type merged struct {
		level int // 0 ok, 1 warn
		notes []string
		paths []string
	}
	var order []string
	groups := map[string]*merged{}
	for _, s := range sts {
		note, lvl := s.Note, 0
		if !s.Installed {
			lvl = 1
		}
		if strings.Contains(note, "registered") && !detected[s.Agent] {
			// The registration exists on disk but the agent is not detected:
			// do not claim the agent is present and wired.
			lvl = 1
			if note == "" {
				note = "configured but " + s.Agent + " not detected"
			} else {
				note = note + " — " + s.Agent + " not detected"
			}
		}
		if _, ok := strings.CutSuffix(s.Agent, " (detected)"); ok && !s.Installed && note == "not present" {
			// "claude (detected) not present" next to a "registered" line
			// reads as "claude is not installed". Say what is actually
			// missing: the kern-first policy in the instruction file.
			note = "detected, but kern-first policy not present in " + s.Path
		}
		m, ok := groups[s.Agent]
		if !ok {
			m = &merged{level: lvl}
			groups[s.Agent] = m
			order = append(order, s.Agent)
		}
		if lvl > m.level {
			m.level = lvl
		}
		if note != "" {
			m.notes = append(m.notes, note)
		}
		if s.Path != "" {
			m.paths = append(m.paths, s.Path)
		}
	}

	var out []Finding
	for _, agent := range order {
		m := groups[agent]
		seenNote := map[string]bool{}
		uniq := m.notes[:0]
		for _, n := range m.notes {
			if !seenNote[n] {
				seenNote[n] = true
				uniq = append(uniq, n)
			}
		}
		detail := strings.Join(uniq, "; ")
		if len(m.paths) > 1 {
			// Same-named statuses from distinct locations (e.g. the two
			// global opencode plugin paths): name them so the merged line is
			// unambiguous.
			if detail != "" {
				detail += " — "
			}
			detail += strings.Join(m.paths, "; ")
		}
		lvl := "ok"
		if m.level > 0 {
			lvl = "warn"
		}
		out = append(out, Finding{Check: agent, Level: lvl, Detail: detail})
	}
	return out
}

// checkSandboxes reports the .kern/sandboxes worktree footprint: how many
// worktree copies exist, their total size, and whether any are stale
// (older than 24h — abandoned leftovers of interrupted loop/check runs).
// kern loop's worktree manager GCs stale copies automatically on the next
// run; this surfaces the state so operators can clean manually.
func checkSandboxes(root string) Finding {
	dir := filepath.Join(root, ".kern", "sandboxes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Finding{Check: "sandboxes", Level: "ok", Detail: "no sandbox worktrees"}
		}
		return Finding{Check: "sandboxes", Level: "ok", Detail: dir + " unreadable"}
	}
	var dirs, bytes int64
	var stale int
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs++
		info, ierr := e.Info()
		if ierr == nil && info.ModTime().Before(cutoff) {
			stale++
		}
		p := filepath.Join(dir, e.Name())
		_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if !d.IsDir() {
				if fi, eerr := d.Info(); eerr == nil {
					bytes += fi.Size()
				}
			}
			return nil
		})
	}
	detail := fmt.Sprintf("%d worktree(s), %.1f MB under %s", dirs, float64(bytes)/(1<<20), dir)
	if stale > 0 || bytes > 100<<20 {
		level := "warn"
		if stale > 0 {
			detail += fmt.Sprintf("; %d stale (abandoned by an interrupted run — cleaned automatically on the next kern loop)", stale)
		}
		return Finding{Check: "sandboxes", Level: level, Detail: detail}
	}
	return Finding{Check: "sandboxes", Level: "ok", Detail: detail}
} // checkPluginSync (D1): the opencode plugin exists in four places that must
// stay byte-identical (project .opencode/plugins, the embedded asset, and the
// two global copies). A stale user copy silently wins over the fixed one —
// opencode 1.18.x loads from ~/.opencode/plugins — so compare every installed
// global copy against the embedded canonical asset and report drift.
func checkPluginSync() []Finding {
	src, err := setup.PluginAsset()
	if err != nil {
		return nil // asset unreadable: nothing to compare against
	}
	var out []Finding
	compared := 0
	for _, p := range setup.GlobalPluginPaths() {
		if _, serr := os.Stat(p); serr != nil {
			continue // not installed here — installation is the wiring check's job
		}
		compared++
		cur, rerr := os.ReadFile(p)
		if rerr != nil {
			out = append(out, Finding{Check: "opencode-plugin-sync", Level: "warn", Detail: p + ": unreadable: " + rerr.Error()})
			continue
		}
		if !bytes.Equal(cur, src) {
			out = append(out, Finding{Check: "opencode-plugin-sync", Level: "warn",
				Detail: fmt.Sprintf("%s: stale copy (sha256 %x, embedded is %x) — run: kern setup --global, then restart opencode", p, sha256.Sum256(cur), sha256.Sum256(src))})
		}
	}
	if compared > 0 && len(out) == 0 {
		out = append(out, Finding{Check: "opencode-plugin-sync", Level: "ok",
			Detail: fmt.Sprintf("%d installed plugin copy(ies) match the embedded asset", compared)})
	}
	return out
}

func checkIndex(root string) Finding {
	files, err := os.ReadDir(cache.Path("index"))
	n := 0
	if err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".json") {
				n++
			}
		}
	}
	if ix, err := index.Load(root); err == nil && ix != nil {
		// F5a: name the RESOLVED store path the root actually serves, not
		// just the counts — "<root>/.kern/index.json" is where the index
		// lives and what the tools read.
		fresh, verdict := indexVerdict(root, ix)
		_ = fresh
		detail := fmt.Sprintf("%s: %d symbols, %d files, %s (%d cached projects)",
			index.StorePath(root), len(ix.Symbols), len(ix.FileHashes), verdict, n)
		return Finding{Check: "index", Level: "ok", Detail: detail}
	}
	if f, ok := CheckMultiRepoIndex(root); ok {
		return f
	}
	// No cached index: report whether the tree even has indexable sources,
	// without building a throwaway index just to answer that.
	if index.HasIndexableSources(root) {
		return Finding{Check: "index", Level: "warn", Detail: "no cached index for this project — run `kern index .`"}
	}
	return Finding{Check: "index", Level: "fail", Detail: "no source files indexed in this project"}
}

// indexVerdict reports the freshness of a loaded index: "fresh"/"stale"
// (with "unknown" for a nil index). The cheap git tree-OID probe decides
// when it can; the loose content proof is the fallback for non-git roots and
// legacy indexes without a recorded tree OID — the same decision order
// `kern index` uses.
func indexVerdict(root string, ix *index.Index) (fresh bool, verdict string) {
	if ix == nil {
		return false, "unknown"
	}
	if fresh, decided, _ := ix.TreeOIDProbe(root); decided {
		if fresh {
			return true, "fresh"
		}
		return false, "stale"
	}
	if ix.FreshnessProof(root).Verdict == index.FreshnessFresh {
		return true, "fresh"
	}
	return false, "stale"
}

// checkIndexFreshness reports whether the cached project index is out of
// date relative to the source tree (files added/removed/edited since build).
// Uses the index's own Stale() gate, which is hash-based and honors
// .gitignore/.kernignore, so it never needs a rebuild to answer.
func checkIndexFreshness(root string) Finding {
	ix, err := index.Load(root)
	if err != nil || ix == nil {
		if f, ok := CheckMultiRepoFreshness(root); ok {
			return f
		}
		// No cached index: checkIndex already reports this; nothing to be
		// stale about. Report ok so the report does not double-fail.
		return Finding{Check: "freshness", Level: "ok", Detail: "no cached index to check"}
	}
	store := index.StorePath(root)
	if ix.Stale() {
		if f, ok := CheckMultiRepoFreshness(root); ok {
			return f
		}
		return Finding{Check: "freshness", Level: "warn",
			Detail: fmt.Sprintf("%s is STALE (%d symbols) — source changed since build; run `kern index .`", store, len(ix.Symbols))}
	}
	return Finding{Check: "freshness", Level: "ok",
		Detail: fmt.Sprintf("%s is fresh (%d symbols, %d files)", store, len(ix.Symbols), len(ix.FileHashes))}
}

// ParentIndexDir walks up from root (exclusive) and returns the nearest
// ancestor that also holds its own index store (.kern/index.json,
// .kern/index.sqlite or .kern/index.json.snap), or "" when none does. The
// walk stops at the enclosing git worktree root: an index above the repo —
// e.g. under $HOME — is unrelated to the project being served. Index
// resolution is explicit-root (StorePath(root)), so a nested .kern is
// exactly what a subdir-rooted process serves — this exists to make that
// shadowing VISIBLE (F5c), not to change resolution.
func ParentIndexDir(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	// Bound the walk: the nearest enclosing git worktree root ("" when root
	// is not inside a git worktree). When root IS the worktree root, its
	// ancestors are all outside the repo — bound the walk to just above it.
	bound := nearestGitRoot(abs)
	if bound != "" && filepath.Clean(bound) == filepath.Clean(abs) {
		bound = filepath.Dir(bound)
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if dir == "." || filepath.Dir(dir) == dir {
			return ""
		}
		if hasIndexStore(dir) {
			return dir
		}
		if bound != "" && dir == bound {
			return ""
		}
	}
}

// nearestGitRoot returns the nearest ancestor of dir that is a git worktree
// root (contains a .git entry), or "" when no ancestor up to the filesystem
// root is one.
func nearestGitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if d == "." || filepath.Dir(d) == d {
			return ""
		}
	}
}

func hasIndexStore(dir string) bool {
	for _, name := range []string{"index.json", "index.sqlite", "index.json.snap"} {
		if _, err := os.Stat(filepath.Join(dir, ".kern", name)); err == nil {
			return true
		}
	}
	return false
}

// checkIndexShadow warns when the root serves its own nested .kern while a
// parent directory also holds an index (F5c): the subdir index is what gets
// served (explicit-root resolution), and a partial subdir index shadowing a
// full parent index is a common source of confusing 0-symbol answers.
func checkIndexShadow(root string) Finding {
	if !hasIndexStore(root) {
		return Finding{Check: "index-shadow", Level: "ok", Detail: "no nested index at this root"}
	}
	parent := ParentIndexDir(root)
	if parent == "" {
		return Finding{Check: "index-shadow", Level: "ok", Detail: "no parent index shadows this root"}
	}
	return Finding{Check: "index-shadow", Level: "warn",
		Detail: fmt.Sprintf("serving nested index %s while a parent index exists at %s — pass the parent root to serve the full index", index.StorePath(root), filepath.Join(parent, ".kern"))}
}

// checkPrecision reports the per-language edge-precision tier recorded on the
// cached index (resolved / ast / heuristic). In the default dependency-free
// build only Go and Java reach "resolved"; the other indexed languages are
// regex-based and their call edges are skipped under --precision strict. This
// surfaces that honestly instead of letting "17 indexed languages" imply
// uniform precision, and points at the opt-in tree-sitter build for AST.
func checkPrecision(root string) Finding {
	ix, err := index.Load(root)
	if err != nil || ix == nil {
		return Finding{Check: "precision", Level: "warn", Detail: "no index found — run 'kern index' to build"}
	}
	if len(ix.PrecisionByLang) == 0 {
		return Finding{Check: "precision", Level: "warn", Detail: "no precision data recorded — rebuild the index with current kern"}
	}
	resolvedCount, astCount, heuristicCount := 0, 0, 0
	var heuristicLangs []string
	for lang, tier := range ix.PrecisionByLang {
		switch tier {
		case "resolved":
			resolvedCount++
		case "ast":
			astCount++
		default:
			heuristicCount++
			heuristicLangs = append(heuristicLangs, lang)
		}
	}
	if heuristicCount > 0 {
		sort.Strings(heuristicLangs)
		return Finding{Check: "precision", Level: "warn",
			Detail: fmt.Sprintf("%d languages resolved (Go + Java), %d at heuristic precision (skipped under --precision strict): %s. Build with -tags treesitter for AST precision on %d more languages.",
				resolvedCount, heuristicCount, strings.Join(heuristicLangs, ", "), heuristicCount)}
	}
	if index.TreesitterEnabled() {
		return Finding{Check: "precision", Level: "ok",
			Detail: fmt.Sprintf("all %d languages at AST-or-better precision (tree-sitter build; %d resolved)", resolvedCount+astCount, resolvedCount)}
	}
	return Finding{Check: "precision", Level: "ok",
		Detail: fmt.Sprintf("all %d languages at resolved precision (Go + Java)", resolvedCount)}
}

// checkRuntime reports the production-intelligence wiring: the wired adapter
// (env/config live source or .kern/runtime.json snapshot), its poll interval,
// and its telemetry health (event/error counts). No source is a warn with the
// enable hint, mirroring `kern runtime status` — the doctor surfaces the
// runtime dimension where users already look for diagnostics.
func checkRuntime(root string) Finding {
	src := runtime.LoadSource(root)
	if src == nil {
		return Finding{
			Check:  "runtime",
			Level:  "warn",
			Detail: "no runtime source; set KERN_PROMETHEUS_URL / KERN_OTEL_URL / KERN_K8S_API, or provide .kern/runtime.json",
		}
	}
	profiles := runtime.ServiceProfiles(src)
	events, errors := 0, 0
	for _, p := range profiles {
		events += p.Events
		errors += p.Errors
	}
	lvl := "ok"
	if errors > 0 {
		lvl = "warn"
	}
	detail := fmt.Sprintf("%s (poll %s): %d events, %d errors, %d deployments, %d commits, %d service(s)",
		src.Name(), runtime.PollInterval(), events, errors, len(src.Deployments("")), len(src.Commits()), len(profiles))
	if errors > 0 {
		detail += " — production errors present"
	}
	return Finding{Check: "runtime", Level: lvl, Detail: detail}
}

func checkOllama() Finding {
	c := llm.New("")
	if c.Available() {
		return Finding{Check: "ollama", Level: "ok", Detail: c.Base + " reachable, model " + c.Model}
	}
	// Ollama down: report which locally-wired agent CLIs can serve as the
	// LLM provider instead (the auto chain in llm.NewProvider).
	agents := llm.AvailableLocalAgents()
	if len(agents) > 0 {
		return Finding{Check: "ollama", Level: "warn", Detail: c.Base + " not reachable; LLM calls fall back to local agent CLI(s): " + strings.Join(agents, ", ")}
	}
	return Finding{Check: "ollama", Level: "warn", Detail: c.Base + " not reachable and no agent CLI installed (claude/codex/gemini/qwen); deterministic compression still works"}
}

func checkStats() Finding {
	rec, err := stats.NewRecorder()
	if err != nil {
		return Finding{Check: "stats", Level: "fail", Detail: err.Error()}
	}
	s, err := rec.Summarize(7, "")
	if err != nil {
		return Finding{Check: "stats", Level: "warn", Detail: err.Error()}
	}
	return Finding{Check: "stats", Level: "ok", Detail: fmt.Sprintf("%d ops, %d tokens saved (%.1f%%) — per-tool breakdown: kern stats --by-tool", s.Operations, s.SavedTotal, s.SavedPct)}
}

// Render formats the findings as a report.
func Render(root string, findings []Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# kern doctor — %s\n\n", root)
	worst := 0 // 0 ok, 1 warn, 2 fail
	rows := make([]string, 0, len(findings))
	for _, f := range findings {
		level := 0
		switch f.Level {
		case "warn":
			level = 1
		case "fail":
			level = 2
		}
		if level > worst {
			worst = level
		}
		rows = append(rows, fmt.Sprintf("[%s] %-22s %s", f.Level, f.Check, f.Detail))
	}
	sort.Strings(rows)
	for _, r := range rows {
		b.WriteString(r)
		b.WriteString("\n")
	}
	verdict := "all good"
	switch worst {
	case 1:
		verdict = "warnings — mostly optional; run `kern setup` and `kern index .`"
	case 2:
		verdict = "failures — fix the [fail] items above"
	}
	fmt.Fprintf(&b, "\nverdict: %s\n", verdict)
	return b.String()
}

// checkGitExclude verifies the side effect index.Save performs silently:
// <root>/.git/info/exclude must list .kern/ so git never tracks kern's index
// store. Warn (not fail) — the entry is (re)added on the next Save, and a
// missing entry only makes .kern visible in git status.
func checkGitExclude(root string) Finding {
	if root == "" {
		return Finding{Check: "git-exclude", Level: "ok", Detail: "no root"}
	}
	gitDir := filepath.Join(root, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil {
		return Finding{Check: "git-exclude", Level: "ok", Detail: "not a git repository"}
	}
	infoDir := filepath.Join(gitDir, "info")
	if !fi.IsDir() {
		// Worktree/submodule gitdir file: "gitdir: /path/to/.git/worktrees/n".
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return Finding{Check: "git-exclude", Level: "warn", Detail: "cannot read gitdir file"}
		}
		line := strings.TrimSpace(string(b))
		if !strings.HasPrefix(line, "gitdir:") {
			return Finding{Check: "git-exclude", Level: "warn", Detail: "unrecognized .git layout"}
		}
		target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		infoDir = filepath.Join(target, "info")
	}
	b, err := os.ReadFile(filepath.Join(infoDir, "exclude"))
	if err != nil {
		return Finding{Check: "git-exclude", Level: "warn", Detail: "no .git/info/exclude (added on next kern index)"}
	}
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ".kern" || trimmed == ".kern/" {
			return Finding{Check: "git-exclude", Level: "ok", Detail: ".kern/ excluded from git"}
		}
	}
	return Finding{Check: "git-exclude", Level: "warn", Detail: ".kern/ not in .git/info/exclude yet (added on next kern index)"}
}
