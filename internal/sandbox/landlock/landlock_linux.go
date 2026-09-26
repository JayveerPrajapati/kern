//go:build linux

package landlock

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	goruntime "runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// This file implements Stage-1 Linux filesystem read confinement (finding M3)
// with Landlock: the sandbox's sensitive-path blocklist (~/.ssh, ~/.aws, ...)
// previously existed only in the macOS Seatbelt profile, so on Linux a
// sandboxed command could read every file-borne secret under $HOME.
//
// Landlock restrictions are per-thread at RESTRICT_SELF time and cannot be
// dropped, so they must be applied in a FRESH child process — a re-exec
// trampoline — which then runs the exact same unshare -> sh -> target chain
// that the sandbox parent built (the argv prefix is passed in the child
// spec; this leaf package cannot re-derive it — netIsolationPrefix lives in
// internal/sandbox). The trampoline is entered via package init() when
// KERN_SANDBOX_CHILD_SPEC is present; every other process pays one
// os.Getenv at startup and nothing else.

// childFatal reports a trampoline failure and exits WITHOUT ever executing
// the target (fail closed after advertising).
func childFatal(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}

// init is the trampoline entry: only fires when this process was re-exec'd by
// the sandbox parent (runGuarded) with the child spec in its environment.
func init() {
	raw := os.Getenv(ChildSpecEnv)
	if raw == "" {
		return // normal process — no side effects
	}
	var spec ChildSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		childFatal(125, "kern sandbox: bad child spec: %v", err)
	}
	rules, err := LandlockAllowPaths(spec.Root, spec.Cmd, spec.Env, writeConfinementEnabled())
	if err != nil {
		childFatal(125, "kern sandbox: allowlist: %v", err)
	}
	// Landlock restrictions apply to the calling thread and are inherited by
	// its children: pin, restrict, and exec on the same thread.
	goruntime.LockOSThread()
	if err := applyLandlock(rules); err != nil {
		childFatal(125, "kern sandbox: landlock apply failed (target NOT executed): %v", err)
	}
	// Rebuild the SAME network chain the parent computed (spec.Prefix — the
	// unshare argv prefix, computed by netIsolationPrefix at spec-build time
	// in internal/sandbox), then exec. argv[0] is the real executable path in
	// both branches (unshare binary, or the target itself when spec.Prefix is
	// empty — Landlock confinement still applies).
	var argv []string
	if len(spec.Prefix) > 0 {
		argv = append(argv, spec.Prefix...)
	}
	argv = append(argv, spec.Cmd)
	argv = append(argv, spec.Args...)
	env := StripEnvVar(spec.Env, ChildSpecEnv)
	if err := syscall.Exec(argv[0], argv, env); err != nil {
		childFatal(125, "kern sandbox: exec %s: %v", argv[0], err)
	}
}

// landlockABI probes the running kernel's Landlock ABI version (0 =
// unavailable).
func landlockABI() int {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(abi)
}

// handledMaskFor returns the access-right mask this kernel ABI can enforce.
// v1 = kernel >= 5.13; v2 = >= 5.19 (+REFER); v3 = >= 6.2 (+TRUNCATE).
// Network/scopes/ioctl rights (v4/v5) are intentionally NOT handled: network
// isolation stays with unshare (unchanged), and unhandled rights are
// unrestricted — the status quo, never a regression.
func handledMaskFor(abi int) uint64 {
	v1 := fsAccessExecute | FsAccessWriteFile | fsAccessReadFile | fsAccessReadDir |
		fsAccessRemoveDir | fsAccessRemoveFile | fsAccessMakeChar | fsAccessMakeDir |
		fsAccessMakeReg | fsAccessMakeSock | fsAccessMakeFifo | fsAccessMakeBlock |
		fsAccessMakeSym
	switch {
	case abi >= 3:
		return v1 | fsAccessRefer | fsAccessTruncate
	case abi == 2:
		return v1 | fsAccessRefer
	default:
		return v1
	}
}

// applyLandlock creates the ruleset, adds every grant rule, then restricts
// the calling thread. Every rule's access is masked to the kernel ABI so a
// newer grant bit never fails on an older kernel.
func applyLandlock(rules []AllowRule) error {
	abi := landlockABI()
	if abi < 1 {
		return fmt.Errorf("landlock unavailable (abi %d)", abi)
	}
	handled := handledMaskFor(abi)
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	rulesetFD, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return errno
	}
	fd := int(rulesetFD)
	defer unix.Close(fd)
	for _, r := range rules {
		pfd, err := unix.Open(r.Path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			if os.IsNotExist(err) {
				continue // path vanished — nothing to grant
			}
			return fmt.Errorf("landlock open %s: %w", r.Path, err)
		}
		pa := unix.LandlockPathBeneathAttr{
			Allowed_access: r.Access & handled,
			Parent_fd:      int32(pfd),
		}
		_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_ADD_RULE, rulesetFD,
			unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&pa)))
		unix.Close(pfd)
		if errno != 0 {
			return fmt.Errorf("landlock add rule %s: %w", r.Path, errno)
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("landlock PR_SET_NO_NEW_PRIVS: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, rulesetFD, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// landlockProbe caches whether the full trampoline chain works on this host.
// The probe re-execs this binary with a trivial spec under a 3s timeout: it
// exercises Landlock apply + the unshare chain end-to-end, so hosts where the
// combination fails (old kernel, seccomp-filtered runner, no_new_privs x
// unshare conflict) silently degrade to the pre-M3 unshare-only chain.
var (
	landlockProbeOnce sync.Once
	landlockOK        bool
)

// LandlockAvailable reports whether the Landlock trampoline can run on this
// host (cached; single probe). prefix is the network-isolation argv prefix
// the sandbox parent computed for THIS host (netIsolationPrefix in
// internal/sandbox) — it is passed into the probe's child spec so the probe
// exercises Landlock apply + the unshare chain end-to-end exactly like a real
// run; nil/empty probes the Landlock+exec path alone. A failed probe is
// logged once so operators know the sensitive-path blocklist is off
// (degraded to unshare-only).
func LandlockAvailable(prefix []string) bool {
	landlockProbeOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			log.Printf("landlock fs confinement unavailable: %v", err)
			return
		}
		spec, err := ChildSpecJSON("/", "true", nil, os.Environ(), prefix)
		if err != nil {
			log.Printf("landlock fs confinement unavailable: %v", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe)
		cmd.Env = append(os.Environ(), ChildSpecEnv+"="+spec)
		if err := cmd.Run(); err != nil {
			log.Printf("landlock fs confinement unavailable: %v", err)
			return
		}
		landlockOK = true
	})
	return landlockOK
}
