// Package fsutil provides small shared filesystem helpers.
package fsutil

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LooksLikeCredentialFile reports whether p's basename matches a
// credential-shaped name — the classic secret files an agent must never have
// served to it through MCP read tools (kern_compact_file, kern_evidence).
// The set is deliberately conservative: a false positive (a source file
// renamed to id_rsa) costs one rename, while a false negative serves a real
// credential. The .pem extension covers key bundles that carry private
// material; SSH public .pub keys are blocked too, conservatively.
func LooksLikeCredentialFile(p string) bool {
	base := filepath.Base(filepath.Clean(p))
	if base == "" || base == "." || base == "/" {
		return false
	}
	// Dotenv files: .env, .env.local, prod.env, config.env, ...
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env") {
		return true
	}
	switch base {
	case ".netrc", ".pgpass", ".htpasswd", ".npmrc", ".pypirc":
		return true
	}
	// SSH key material (id_rsa, id_ed25519, and suffixed variants — both dot
	// suffixes like id_rsa.old and underscore suffixes like id_rsa_github are
	// common key-naming conventions).
	for _, k := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
		if base == k || strings.HasPrefix(base, k+".") || strings.HasPrefix(base, k+"_") {
			return true
		}
	}
	// Key and certificate material.
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

// WriteFileAtomic writes data to path atomically: write to a temp file in the
// same directory, set permissions, then rename over the target.
func WriteFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(targetPath), filepath.Base(targetPath)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, targetPath); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// NormalizePath converts backslashes to forward slashes and cleans redundant
// path elements, ensuring uniform POSIX representation across all OS platforms.
func NormalizePath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	return path.Clean(p)
}

// NormalizeRelPath converts backslashes to forward slashes, cleans redundant
// path elements, and strips any leading "./" prefix.
func NormalizeRelPath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	n := path.Clean(p)
	if n == "." {
		return "."
	}
	return strings.TrimPrefix(n, "./")
}

// RootedPath resolves p for a file-reading tool. When root is given, the path
// must stay inside it (rejecting "..", absolute paths outside, and symlink
// escapes). A rootless call may only reference a path relative to the current
// working directory: an absolute path is rejected outright, since otherwise a
// caller could pass e.g. path=/etc/shadow and read any file on the system
// outside the confined workspace. This is the path-confinement contract every
// MCP file-reading tool relies on: a returned path is guaranteed to stay
// inside the given root (or the process cwd when root is empty).
func RootedPath(root, p string) (string, error) {
	if root == "" {
		if filepath.IsAbs(p) {
			return "", fmt.Errorf("absolute path requires root argument")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return WithinRoot(cwd, p)
	}
	return WithinRoot(root, p)
}

// WithinRoot resolves file against root (absolute paths are used as-is) and
// requires the result to stay inside root, rejecting `..` escapes, absolute
// paths that point outside the project boundary, and symlink escapes (a
// symlink inside the project that points outside). It returns the resolved
// absolute path. The confinement contract: a caller may read or write the
// returned path knowing it cannot escape root — a candidate that does not
// exist yet is judged by its nearest existing ancestor's real location, so a
// symlinked parent directory (root/link -> /etc) cannot smuggle a write
// outside the project boundary.
func WithinRoot(root, file string) (string, error) {
	var abs string
	if filepath.IsAbs(file) {
		abs = filepath.Clean(file)
	} else {
		abs = filepath.Join(root, file)
	}
	// Resolve symlinks on both the root and the candidate so a symlink inside
	// the project that points outside cannot read/escape the project boundary.
	// A candidate that does not exist yet (e.g. a file about to be written)
	// cannot be resolved directly, so resolve the NEAREST EXISTING ANCESTOR
	// and re-append the remaining components: a symlinked parent directory
	// (root/link -> /etc) is then judged by its real location instead of its
	// lexical text, closing the escape where the old pure-lexical fallback
	// let root/link/newfile land in /etc.
	rRoot, rerr := filepath.EvalSymlinks(root)
	if rerr != nil {
		rRoot = root
	}
	real := abs
	var rem []string
	probe := abs
	for {
		if r, err := filepath.EvalSymlinks(probe); err == nil {
			real = r
			if len(rem) > 0 {
				real = filepath.Join(append([]string{r}, rem...)...)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			// Nothing resolvable up to the filesystem root: fall back to the
			// lexical Clean+Rel check rather than denying an unresolvable path.
			real = abs
			break
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
	rel, err := filepath.Rel(rRoot, real)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %s escapes project root %s", abs, root)
	}
	return abs, nil
}

// EvalSymlinksNearest resolves p to its canonical absolute path, walking up
// to the nearest EXISTING ancestor when EvalSymlinks fails (the target may
// not exist yet) and re-appending the unresolved remainder, so symlinks that
// DO exist still resolve. On total failure the cleaned lexical path returns.
func EvalSymlinksNearest(p string) string {
	cur := filepath.Clean(p)
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, filepath.Join(tail...))
		}
		parent := filepath.Dir(cur)
		if parent == cur { // reached the filesystem root
			return filepath.Join(cur, filepath.Join(tail...))
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

// NearestExisting resolves the real location of the nearest existing ancestor
// of abs (walking up until EvalSymlinks succeeds) and re-appends the remaining
// components. errPrefix is prepended to the failure error.
func NearestExisting(abs string, errPrefix string) (string, error) {
	var rem []string
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			return filepath.Join(append([]string{real}, rem...)...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", fmt.Errorf("%s: cannot resolve %q", errPrefix, abs)
		}
		rem = append([]string{filepath.Base(probe)}, rem...)
		probe = parent
	}
}

// ConfinePath resolves p against root (a relative candidate is joined to root
// first) and rejects any path that escapes root — "..", absolute paths outside
// the root, and symlinked parents (root/link -> /etc). It returns the absolute
// cleaned path on success. errPrefix is prepended to both failure messages.
func ConfinePath(root, p, errPrefix string) (string, error) {
	if root == "" {
		if root, _ = os.Getwd(); root == "" {
			root = "."
		}
	}
	abs := filepath.Clean(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	real, err := NearestExisting(abs, errPrefix)
	if err != nil {
		return "", err
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		rr = root
	}
	rel, err := filepath.Rel(rr, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s: file %q escapes workspace root", errPrefix, p)
	}
	return abs, nil
}

// MinimalExecEnv builds the minimal environment for a spawned child process
// (compile steps, per-mutant test runs). The full operator environment
// (secrets) must never reach the child: pass only PATH (to find the
// toolchain), HOME (go's default GOCACHE/GOPATH/GOENV locations), TMPDIR (go
// temp files) and the Go toolchain vars the child genuinely needs when the
// operator set them (custom GOPATH/GOCACHE/GOMODCACHE/GOPROXY/GOTOOLCHAIN).
// GOENV itself is deliberately excluded: go reads it from
// $HOME/.config/go/env via HOME, so `go env -w` settings survive the
// narrowing.
func MinimalExecEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH")}
	for _, k := range []string{"HOME", "TMPDIR", "GOPATH", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOTOOLCHAIN"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}
