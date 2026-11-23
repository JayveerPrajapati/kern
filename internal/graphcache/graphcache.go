// Package graphcache persists derived read-path structures as gob sidecars
// under <root>/.kern/, keyed by the identity of the index they were derived
// from. It exists to keep one-shot CLI reads fast: building the export
// catalog and the domain graph from a loaded index costs hundreds of ms per
// process, while decoding a persisted sidecar costs a fraction of that.
//
// The package is deliberately dependency-free (stdlib only) so both
// internal/index and internal/app can use it without import cycles.
//
// Semantics:
//   - Save is atomic (temp file + rename) and best-effort: read-only
//     sandboxes or full disks make it fail silently — the caller falls back
//     to building from the index, which is always correct.
//   - Load verifies the key before decoding; any mismatch, missing file, or
//     decode error is a miss (false), never an error.
//   - A successful Save prunes same-name sidecars with other keys, so at
//     most one generation of each sidecar exists per name.
//   - Keys must include everything the persisted shape depends on: callers
//     pass the index identity parts plus a schema/version constant, so a
//     rebuilt index or a changed format invalidates the sidecar by key
//     change rather than by content sniffing.
package graphcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SidecarVersion is the graphcache on-disk format generation. Bump it when
// the persisted shapes change incompatibly (a field is removed or re-typed);
// it belongs in every key so old sidecars stop loading after an upgrade.
const SidecarVersion = 1

// Key derives the sidecar key from the given parts. Parts are joined with a
// NUL separator (no part may contain one) and SHA-256 hashed, so the key is
// a stable, filename-safe 64-hex string.
func Key(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", h)
}

// Load reads the sidecar named name for key into obj (a pointer). It reports
// whether a matching, decodable sidecar was found; every failure mode is a
// miss.
func Load(root, name, key string, obj any) bool {
	if root == "" || name == "" || key == "" || obj == nil {
		return false
	}
	data, err := os.ReadFile(sidecarPath(root, name, key))
	if err != nil {
		return false
	}
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(obj); err != nil {
		return false
	}
	return true
}

// Save writes obj to the sidecar named name for key, atomically, and prunes
// other generations of the same name. Errors are returned but callers treat
// them as best-effort.
func Save(root, name, key string, obj any) error {
	if root == "" || name == "" || key == "" || obj == nil {
		return fmt.Errorf("graphcache: empty root, name, key, or object")
	}
	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(obj); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	target := sidecarPath(root, name, key)
	if err := writeAll(tmp, buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	prune(root, name, key)
	return nil
}

func writeAll(f *os.File, data []byte) error {
	_, err := f.Write(data)
	return err
}

func sidecarPath(root, name, key string) string {
	return filepath.Join(root, ".kern", name+"-"+key+".gob")
}

// prune removes same-name sidecars whose key differs, so a stale generation
// never lingers after its index was rebuilt. Best-effort.
func prune(root, name, keepKey string) {
	dir := filepath.Join(root, ".kern")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := name + "-"
	suffix := ".gob"
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, suffix) {
			continue
		}
		if n == name+"-"+keepKey+suffix {
			continue
		}
		os.Remove(filepath.Join(dir, n))
	}
}
