package guardpause

// guardpause implements the audited, repo-scoped pause for the agent hook's
// read/grep/glob steering. It deliberately does NOT pause raw bash: allowing
// raw shell would bypass the hook's command gate and weaken exec governance.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/flock"
)

const (
	// Schema identifies the pause JSONL record format.
	Schema = "kern-guard-pause"
	// DefaultDuration is used when --duration is omitted.
	DefaultDuration = 30 * time.Minute
	// MaxDuration bounds every pause.
	MaxDuration = 2 * time.Hour
)

// Pause is one active, duration-bounded repo pause.
type Pause struct {
	Schema   string `json:"schema_version"`
	Scope    string `json:"scope"`
	Root     string `json:"root"`
	Reason   string `json:"reason"`
	Expires  string `json:"expires"`
	Recorded string `json:"recorded"`
}

// registry holds parsed pause records plus foreign JSONL lines. Foreign lines
// are preserved verbatim so a shared registry can carry other record types.
type registry struct {
	pauses  []Pause
	foreign []string
}

// Path returns the outside-workspace pause registry location. The path is
// deliberately derived from the operator home and is not environment-overridable.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kern", "guard", "pauses.jsonl"), nil
}

func auditPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kern", "audit", "bypass.jsonl"), nil
}

// CanonicalRoot resolves a repository root for pause matching. Symlinks are
// resolved when possible so a worktree and its canonical path agree.
func CanonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

// ParseDuration validates the operator-supplied duration. Empty means the
// default; values above MaxDuration are rejected.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return DefaultDuration, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q: use a positive Go duration such as 30m", s)
	}
	if d > MaxDuration {
		return 0, fmt.Errorf("duration %s exceeds the 2h maximum", d)
	}
	return d, nil
}

func readLocked(path string, now time.Time) (registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return registry{}, nil
		}
		return registry{}, err
	}
	var reg registry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var p Pause
		if json.Unmarshal([]byte(line), &p) != nil || p.Schema != Schema {
			reg.foreign = append(reg.foreign, line)
			continue
		}
		exp, perr := time.Parse(time.RFC3339, p.Expires)
		if perr != nil {
			reg.foreign = append(reg.foreign, line)
			continue
		}
		if exp.After(now) {
			reg.pauses = append(reg.pauses, p)
		}
	}
	return reg, nil
}

func writeLocked(path string, reg registry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, p := range reg.pauses {
		p.Schema = Schema
		j, err := json.Marshal(p)
		if err != nil {
			return err
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	for _, line := range reg.foreign {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendAudit(event string, p Pause, detail string) error {
	path, err := auditPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	e := map[string]any{
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"event":  event,
		"scope":  p.Scope,
		"root":   p.Root,
		"reason": p.Reason,
		"detail": detail,
		"pwd":    p.Root,
	}
	j, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(j, '\n'))
	return err
}

// Active returns the active pause for root, if any.
func Active(root string) (Pause, bool, error) {
	return ActiveScoped(root, ScopeRepo)
}

// ActiveScoped reports the active pause for root under the given scope
// (ScopeRepo for read/grep/glob steering, ScopeBashSteering for the bash
// arm's serve redirection).
func ActiveScoped(root, scope string) (Pause, bool, error) {
	root, err := CanonicalRoot(root)
	if err != nil {
		return Pause{}, false, err
	}
	path, err := Path()
	if err != nil {
		return Pause{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Pause{}, false, err
	}
	f, err := flock.Lock(path + ".lock")
	if err != nil {
		return Pause{}, false, err
	}
	defer flock.Release(f)
	reg, err := readLocked(path, time.Now().UTC())
	if err != nil {
		return Pause{}, false, err
	}
	for _, p := range reg.pauses {
		if p.Scope == scope && p.Root == root {
			return p, true, nil
		}
	}
	return Pause{}, false, nil
}

// token returns a 32-byte random hex confirmation challenge.
func token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateRecord creates and persists a pause without interactive confirmation.
// It is exported for narrow tests; production callers use Create.
// Pause scopes. "repo" pauses the read/grep/glob steering (the classic
// relief valve). "bash-steering" pauses ONLY the bash arm's serve
// redirection — grep/cat-class bash commands run RAW instead of being
// served from the index — for operators whose local serve misbehaves
// (stale index, tier bugs). GATE verdicts, exec governance and the
// ask-protocol are never paused by either scope.
const (
	ScopeRepo         = "repo"
	ScopeBashSteering = "bash-steering"
)

// ValidScope reports whether s is a known pause scope.
func ValidScope(s string) bool {
	return s == ScopeRepo || s == ScopeBashSteering
}

func CreateRecord(root, duration, reason string, now time.Time) (Pause, error) {
	return CreateRecordScoped(root, duration, reason, ScopeRepo, now)
}

func CreateRecordScoped(root, duration, reason, scope string, now time.Time) (Pause, error) {
	if !ValidScope(scope) {
		return Pause{}, fmt.Errorf("unknown pause scope %q (known: %s, %s)", scope, ScopeRepo, ScopeBashSteering)
	}
	root, err := CanonicalRoot(root)
	if err != nil {
		return Pause{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return Pause{}, errors.New("reason is required")
	}
	d, err := ParseDuration(duration)
	if err != nil {
		return Pause{}, err
	}
	p := Pause{
		Schema:   Schema,
		Scope:    scope,
		Root:     root,
		Reason:   strings.TrimSpace(reason),
		Expires:  now.Add(d).UTC().Format(time.RFC3339),
		Recorded: now.UTC().Format(time.RFC3339),
	}
	path, err := Path()
	if err != nil {
		return Pause{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Pause{}, err
	}
	f, err := flock.Lock(path + ".lock")
	if err != nil {
		return Pause{}, err
	}
	defer flock.Release(f)
	reg, err := readLocked(path, now)
	if err != nil {
		return Pause{}, err
	}
	for _, existing := range reg.pauses {
		if existing.Scope == scope && existing.Root == root {
			return Pause{}, errors.New("this repo already has an active " + scope + " pause")
		}
	}
	// Audit the request before activation. If the store write fails, the trail
	// shows an attempted pause but no pause is active — the safe failure
	// direction for an audited bypass-like state change.
	if err := appendAudit("kern-guard-pause-request", p, "scope="+scope+"; duration="+d.String()+"; expires="+p.Expires); err != nil {
		return Pause{}, fmt.Errorf("pause audit failed: %w", err)
	}
	reg.pauses = append(reg.pauses, p)
	if err := writeLocked(path, reg); err != nil {
		return Pause{}, err
	}
	if err := appendAudit("kern-guard-pause-active", p, "pause written; scope="+scope+"; duration="+d.String()+"; expires="+p.Expires); err != nil {
		// Best-effort rollback: never leave an active pause whose completion
		// audit could not be recorded.
		_ = removeLockedScoped(path, root, scope, now)
		return Pause{}, fmt.Errorf("pause activation audit failed: %w", err)
	}
	return p, nil
}

func removeLocked(path, root string, now time.Time) error {
	return removeLockedScoped(path, root, ScopeRepo, now)
}

func removeLockedScoped(path, root, scope string, now time.Time) error {
	reg, err := readLocked(path, now)
	if err != nil {
		return err
	}
	kept := registry{foreign: reg.foreign}
	for _, p := range reg.pauses {
		if p.Scope == scope && p.Root == root {
			continue
		}
		kept.pauses = append(kept.pauses, p)
	}
	return writeLocked(path, kept)
}

// Create performs the typed tty confirmation and then creates the pause.
func Create(root, duration, reason, scope string) (Pause, error) {
	if !ValidScope(scope) {
		return Pause{}, fmt.Errorf("unknown pause scope %q (known: %s, %s)", scope, ScopeRepo, ScopeBashSteering)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return Pause{}, errors.New("no interactive terminal: a pause can only be created by an operator at /dev/tty")
	}
	defer tty.Close()
	challenge, err := token()
	if err != nil {
		return Pause{}, err
	}
	fmt.Fprintf(tty, "kern guard pause\n  root:     %s\n  scope:    %s\n  duration: %s\n  reason:   %s\nType this FULL token to pause %s guard steering: %s\n", root, scope, duration, reason, scope, challenge)
	answerCh := make(chan string, 1)
	go func() {
		var answer string
		fmt.Fscanln(tty, &answer)
		answerCh <- answer
	}()
	select {
	case answer := <-answerCh:
		if strings.TrimSpace(answer) != challenge {
			return Pause{}, errors.New("confirmation did not match the full token")
		}
	case <-time.After(15 * time.Second):
		return Pause{}, errors.New("timed out after 15s")
	}
	return CreateRecordScoped(root, duration, reason, scope, time.Now())
}

// Resume removes the active pauses for root (every scope — an operator
// saying "resume" means stop pausing) and audits each removal.
func Resume(root string) (bool, error) {
	root, err := CanonicalRoot(root)
	if err != nil {
		return false, err
	}
	path, err := Path()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := flock.Lock(path + ".lock")
	if err != nil {
		return false, err
	}
	defer flock.Release(f)
	now := time.Now().UTC()
	reg, err := readLocked(path, now)
	if err != nil {
		return false, err
	}
	removed := false
	for _, p := range reg.pauses {
		if p.Root != root {
			continue
		}
		if err := appendAudit("kern-guard-resume-request", p, "remove active "+p.Scope+" pause; expires="+p.Expires); err != nil {
			return false, fmt.Errorf("resume audit failed: %w", err)
		}
		if err := removeLockedScoped(path, root, p.Scope, now); err != nil {
			return false, err
		}
		if err := appendAudit("kern-guard-resume-complete", p, p.Scope+" pause removed; expires="+p.Expires); err != nil {
			// Restore the exact pre-removal registry so a pause is never lost
			// merely because its completion audit could not be written.
			if rerr := writeLocked(path, reg); rerr != nil {
				return false, fmt.Errorf("resume completion audit failed: %v; rollback also failed: %w", err, rerr)
			}
			return false, fmt.Errorf("resume completion audit failed: %w", err)
		}
		removed = true
	}
	return removed, nil
}

// Diagnostic is the doctor-shaped result for the guard state checks. It is
// deliberately independent of internal/doctor: guardpause is the owning
// package, and doctor renders these values without importing back.
type Diagnostic struct {
	Check  string `json:"check"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
}

// DoctorFindings returns the guard state checks (disable switches, active
// pauses) as diagnostics — doctor renders them without owning the logic.
func DoctorFindings(root string) []Diagnostic {
	return []Diagnostic{CheckSwitches(root), CheckPause(root)}
}

// CheckSwitches reports the un-audited agent-hook disable switches. It keeps
// the R2 wording exact: these switches do not affect the exec firewall.
func CheckSwitches(root string) Diagnostic {
	var warns []string
	if os.Getenv("KERN_DISABLED") == "1" {
		warns = append(warns, "KERN_DISABLED=1 (agent hook guard off; un-audited switch — exec firewall still enforces)")
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "off")); err == nil {
		warns = append(warns, filepath.Join(root, ".kern", "off")+" (agent hook guard off; un-audited switch — exec firewall still enforces)")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Diagnostic{Check: "guard-switches", Level: "warn", Detail: "could not inspect .kern/off: " + err.Error()}
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "indexing")); err == nil {
		warns = append(warns, filepath.Join(root, ".kern", "indexing")+" (first index in progress; hook guard intentionally fails open)")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Diagnostic{Check: "guard-switches", Level: "warn", Detail: "could not inspect .kern/indexing: " + err.Error()}
	}
	if len(warns) == 0 {
		return Diagnostic{Check: "guard-switches", Level: "ok", Detail: "no agent hook disable switches active"}
	}
	return Diagnostic{Check: "guard-switches", Level: "warn", Detail: strings.Join(warns, " | ")}
}

// CheckPause reports an active pause without implying that exec governance
// changed: the pause affects only read/grep/glob steering.
func CheckPause(root string) Diagnostic {
	p, ok, err := Active(root)
	if err != nil {
		return Diagnostic{Check: "guard-pause", Level: "warn", Detail: "could not inspect repo pause: " + err.Error()}
	}
	if !ok {
		return Diagnostic{Check: "guard-pause", Level: "ok", Detail: "no active repo guard pause"}
	}
	exp, perr := time.Parse(time.RFC3339, p.Expires)
	if perr != nil {
		return Diagnostic{Check: "guard-pause", Level: "warn", Detail: "active pause has invalid expiry " + p.Expires + " (root " + p.Root + ")"}
	}
	return Diagnostic{Check: "guard-pause", Level: "warn", Detail: "repo pause active — root " + p.Root + "; reason " + p.Reason + "; expires " + p.Expires + " (" + time.Until(exp).Round(time.Second).String() + " remaining); exec governance remains enforced"}
}
