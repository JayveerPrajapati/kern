// Package version holds the build-stamped version string shared by all
// kern binaries. Release builds override it via
// -ldflags "-X github.com/JayveerPrajapati/kern/internal/version.Version=..."
// (or the legacy "-X main.version=..." which still works because each
// main package's version var is initialized from this one).
package version

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// Version is the kern release version. Defaults to "dev" for source
// checkouts; stamped at build time for releases.
var Version = "dev"

// Adopt resolves the effective version a main package should report. Each
// binary keeps a `var version = "dev"` in package main so the legacy
// `-ldflags "-X main.version=..."` (which only rewrites a compile-time
// constant initializer) keeps working; Adopt then fingerprints whether that
// var is still the unwrapped "dev" and, if so, falls back to the shared
// internal/version.Version (either "dev" or the newer
// `-X .../internal/version.Version=...` stamp). This centralizes the
// boilerplate that used to be repeated (verbatim) in every cmd/*/main.go
// init(), while preserving exact behavior for both ldflags forms — DRY.
func Adopt(compiledIn string) string {
	if compiledIn != "dev" {
		return compiledIn
	}
	return Version
}

// Describe decorates a still-"dev" version with the build's provenance so a
// binary built with a plain `go build` from a checkout reports its commit
// instead of a bare "dev" (dogfood F-B1, 2026-11-22: an unstamped kern-mcp
// answered `--version` and the MCP initialize serverInfo with "dev" while
// the sibling kern CLI reported its commit). The toolchain-embedded
// vcs.revision build setting is used first, then the checkout's .git/HEAD
// for -buildvcs=false builds. Stamped versions pass through unchanged, so a
// release binary reports exactly its tag.
func Describe(v string) string {
	if v != "dev" {
		return v
	}
	if rev := buildRevision(); rev != "" {
		return shortRevision(rev) + " (dev)"
	}
	if h := headShortHash(); h != "" {
		return h + " (dev)"
	}
	return v
}

// shortRevision keeps the conventional 7-char short form the Makefile
// stamps; other lengths (already-short or non-hash values) pass through.
func shortRevision(rev string) string {
	if len(rev) == 40 {
		return rev[:7]
	}
	return rev
}

// buildRevision returns the git revision the Go toolchain embedded at build
// time (the vcs.revision build setting), or "" when absent (a release
// tarball build, `go install pkg@version` from the module cache, or
// -buildvcs=false).
func buildRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// headShortHash returns the short HEAD hash of the git checkout containing
// the working directory (walking up to the repository root), or "" when no
// checkout is found. It is the file-reading equivalent of `git rev-parse
// --short HEAD`: .git/HEAD holds the hash directly (detached HEAD) or names
// a ref whose loose file (or packed-refs entry) holds it.
func headShortHash() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if h := readHeadHash(filepath.Join(dir, ".git")); h != "" {
			return h
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// readHeadHash resolves the HEAD commit of the git directory gitDir, or ""
// when gitDir is not a readable git metadata directory.
func readHeadHash(gitDir string) string {
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(head))
	if h == "" {
		return ""
	}
	if !strings.HasPrefix(h, "ref: ") {
		return shortRevision(h) // detached HEAD: the hash itself
	}
	ref := strings.TrimSpace(strings.TrimPrefix(h, "ref: "))
	if b, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref))); err == nil {
		return shortRevision(strings.TrimSpace(string(b)))
	}
	// Packed refs: "<hash> <refname>" lines.
	packed, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(packed), "\n") {
		fields := strings.Fields(ln)
		if len(fields) == 2 && fields[1] == ref {
			return shortRevision(fields[0])
		}
	}
	return ""
}

// BuildID identifies the running build for tool-response cache keying.
// Release builds (Version stamped) return Version unchanged. Dev builds
// ("dev") fold in the executable's size and mtime so that a rebuild after a
// code change mints fresh cache keys instead of serving responses cached by
// the pre-change binary — a fixed handler otherwise appears still broken
// until the 24h TTL lapses or the cache is cleared by hand.
func BuildID() string {
	if Version != "dev" {
		return Version
	}
	self, err := os.Executable()
	if err != nil {
		return Version
	}
	st, err := os.Stat(self)
	if err != nil {
		return Version
	}
	return fmt.Sprintf("dev+%d@%d", st.Size(), st.ModTime().Unix())
}
