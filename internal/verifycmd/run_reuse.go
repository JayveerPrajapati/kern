package verifycmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// run_reuse.go implements the T-02 verification-anchor reuse layer: when the
// exact same command is verified again in the same root whose repo state
// (git HEAD + tree digest of tracked modifications) is unchanged, the kept
// output of the earlier successful run is served instead of re-executing.
//
// Invariants (plan T-02):
//   - never reuse across roots, workspaces or commands — the manifest key is
//     (command, root, HEAD, tree digest) and the stamp is root-bound;
//   - never reuse failed runs — manifests are saved only for exit 0 runs
//     that did not time out;
//   - GATE/classification and the exec firewall still run BEFORE the reuse
//     lookup, so governance is never skipped, only redundant execution;
//   - the manifest is HMAC-stamped with the machine-bound exec secret
//     (governance.StampRunManifest), so a client who controls the
//     workspace cannot forge a reusable manifest or transplant one from
//     another command/root;
//   - reuse is bounded in age ("recent") because identical repo state does
//     not make time- or network-dependent commands deterministic;
//   - KERN_VERIFY_REUSE=0 disables both saving and lookup (fresh runs only).

const (
	defaultReuseMaxAge = 10 * time.Minute
	manifestSuffix     = ".meta.json"
)

// runManifest is the signed sidecar of a kept .log run. Version gates the
// format so a future change cannot be verified by an older binary.
type runManifest struct {
	Version    int    `json:"version"`
	Command    string `json:"command"`
	Root       string `json:"root"`
	Head       string `json:"head"`
	TreeDigest string `json:"tree"`
	Anchor     string `json:"anchor"`
	Exit       int    `json:"exit"`
	DurMS      int64  `json:"dur_ms"`
	At         int64  `json:"at_unix"`
	Stamp      string `json:"stamp"`
}

const runManifestVersion = 1

// reuseEnabled reports whether the reuse layer is on (default) — the
// KERN_VERIFY_REUSE=0 kill-switch.
func reuseEnabled() bool {
	switch strings.TrimSpace(os.Getenv("KERN_VERIFY_REUSE")) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// reuseMaxAge bounds how old a run may be and still be reused.
func reuseMaxAge() time.Duration {
	if v := strings.TrimSpace(os.Getenv("KERN_VERIFY_REUSE_MAX_AGE")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultReuseMaxAge
}

// repoFingerprint returns the (HEAD, tree digest) pair for root, or ok=false
// when root is not a usable git repository — non-git roots never reuse.
// ONE `git status --porcelain=v2 --branch` call yields both: the
// "# branch.oid" header is HEAD, and every entry line is the tracked/untracked
// state, so ANY tracked-file change (staged, unstaged, renames, conflicts)
// invalidates reuse. Kern's own untracked .kern state dir (runs, manifests,
// audit — written by this very layer) is excluded so reuse never invalidates
// itself; tracked .kern paths, if a repo actually tracks them, still count.
// The fingerprint must be captured BEFORE the command runs.
func repoFingerprint(root string) (head, digest string, ok bool) {
	sb, err := exec.Command("git", "-C", root, "status", "--porcelain=v2", "--branch").Output()
	if err != nil {
		return "", "", false
	}
	var kept []string
	for _, line := range strings.Split(string(sb), "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			head = strings.TrimSpace(strings.TrimPrefix(line, "# branch.oid "))
		case strings.HasPrefix(line, "? "):
			// Untracked entry: path is everything after "? " (may contain
			// spaces). Skip kern's own state dir.
			if p := line[2:]; p == ".kern" || strings.HasPrefix(p, ".kern/") {
				continue
			}
			kept = append(kept, line)
		case line != "":
			// Tracked/renamed/conflicted entries keep their full line —
			// a repo that tracks .kern paths gets correct invalidation.
			kept = append(kept, line)
		}
	}
	if head == "" || head == "(initial)" || strings.HasPrefix(head, "(") {
		return "", "", false // unborn/unusable HEAD: fail closed
	}
	sum := sha256.Sum256([]byte(strings.Join(kept, "\n")))
	return head, hex.EncodeToString(sum[:]), true
}

// manifestPayload is the canonical signed byte form: the stamp covers every
// decision-relevant field except the stamp itself.
func manifestPayload(m runManifest) []byte {
	m.Stamp = ""
	b, _ := json.Marshal(m)
	return b
}

// saveRunManifest writes the signed sidecar next to the kept .log.
func saveRunManifest(root string, m runManifest) error {
	m.Version = runManifestVersion
	stamp, err := governance.StampRunManifest(root, manifestPayload(m))
	if err != nil {
		return err
	}
	m.Stamp = stamp
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runsDir(root), m.Anchor+manifestSuffix), b, 0o600)
}

// reusableRun is a matched, verified manifest plus its kept output.
type reusableRun struct {
	manifest runManifest
	output   string
}

// hasManifests reports whether the root has any reuse manifests at all —
// the microsecond fast path that keeps manifest-free roots from paying the
// git fingerprint subprocess.
func hasManifests(root string) bool {
	entries, err := os.ReadDir(runsDir(root))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), manifestSuffix) {
			return true
		}
	}
	return false
}

// lookupReusableRun scans the root's kept runs for a manifest whose command,
// root, HEAD, tree digest, signature and age all match, and whose output is
// still loadable. Any miss means "run it fresh".
func lookupReusableRun(root, command, head, digest string) (reusableRun, bool) {
	if !reuseEnabled() || command == "" || root == "" {
		return reusableRun{}, false
	}
	entries, err := os.ReadDir(runsDir(root))
	if err != nil {
		return reusableRun{}, false
	}
	var best reusableRun
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), manifestSuffix) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(runsDir(root), e.Name()))
		if err != nil {
			continue
		}
		var m runManifest
		if json.Unmarshal(b, &m) != nil || m.Version != runManifestVersion {
			continue
		}
		if m.Command != command || m.Root != root || m.Exit != 0 {
			continue
		}
		if m.Head != head || m.TreeDigest != digest {
			continue
		}
		if !governance.VerifyRunManifest(root, manifestPayload(m), m.Stamp) {
			continue
		}
		if time.Since(time.Unix(m.At, 0)) > reuseMaxAge() {
			continue
		}
		out, err := loadRun(root, m.Anchor)
		if err != nil {
			continue
		}
		if m.At > best.manifest.At {
			best = reusableRun{manifest: m, output: out}
		}
	}
	return best, best.manifest.Anchor != ""
}

// reuseProvenance is the honest banner prepended to a reused run so agents
// always know the output was not freshly executed.
func reuseProvenance(m runManifest) string {
	age := time.Since(time.Unix(m.At, 0)).Round(time.Second)
	return fmt.Sprintf("[reused] identical command verified %s ago at the same repo state (HEAD + tree digest match); KERN_VERIFY_REUSE=0 forces a fresh run\n", age)
}
