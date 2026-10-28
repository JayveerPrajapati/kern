// Package daemon runs a warm, per-project kern process behind a unix socket.
//
// Why: every CLI invocation pays the full index hydration (~0.5 s on a
// 17k-symbol repo) before it answers, and a read-only query costs ~0.1 s on
// top. The daemon keeps one hydrated index per project and answers the
// read-only commands (`kern search`, `context`, `explore`, `graph`, `impact`,
// `dependencies`, `why`, `compact`) over a local socket, so the second call
// onward skips hydration entirely.
//
// Safety properties, in order of importance:
//
//   - Read-only whitelist. The caller decides which argv may be proxied; the
//     daemon just executes them. Mutating commands never reach it.
//   - Binary identity. A daemon started by an older build must not answer for
//     a rebuilt binary, so every request carries an identity string derived
//     from the version AND the executable's mtime+size; a mismatch is reported
//     as a fatal the client resolves by restarting the daemon.
//   - Freshness is preserved. Index freshness stays the caller's job
//     (index.LoadOrBuild checks staleness on every request); the daemon only
//     memoizes the hydrated index by store fingerprint, so a rebuild
//     invalidates the memo automatically.
//   - Transparent fallback. Every failure path (no socket, refused, timeout,
//     protocol error) returns an error and the CLI runs the command in-process
//     exactly as before. The daemon is an optimization, never a dependency.
//
// Platform: unix domain sockets only; Supported() is false on Windows, where
// the CLI keeps its one-process-per-command behavior.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Protocol operations.
const (
	OpRun  = "run"
	OpPing = "ping"
	OpStop = "stop"
)

// Request is one frame in either direction. One request per connection keeps
// framing trivial (no length prefix, no stream desync).
type Request struct {
	Op       string   `json:"op,omitempty"`
	Identity string   `json:"identity,omitempty"`
	Argv     []string `json:"argv,omitempty"`
}

// Response is the daemon's answer. Fatal is a TRANSPORT/PROTOCOL error (never
// command output): the client falls back to an in-process run on any Fatal.
type Response struct {
	Exit     int    `json:"exit"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Fatal    string `json:"fatal,omitempty"`
	Version  string `json:"version,omitempty"`
	Pid      int    `json:"pid,omitempty"`
	Identity string `json:"identity,omitempty"`
}

// Handler executes one argv inside the daemon process.
type Handler func(argv []string) Response

// ErrUnsupported means this platform has no unix sockets; callers stay
// in-process.
var ErrUnsupported = errors.New("daemon unsupported on this platform")

// maxSocketPath is macOS/BSD's sun_path budget (104 bytes incl. NUL). A
// deeply nested checkout exceeds it, and bind() would fail with EINVAL for
// every call — so such roots get a hashed socket in the temp dir instead.
const maxSocketPath = 100

// SocketPath is the per-project, per-binary socket. It lives beside the
// index so two checkouts of the same repo never share a daemon, and it is
// namespaced by a digest of the running executable (path+mtime+size) so two
// coexisting kern builds — an installed release and a locally built dev
// binary on the same machine, or a binary that was just rebuilt in place —
// each keep their own warm daemon instead of retiring and re-spawning each
// other on every invocation (the measured thrash cost: full re-hydration,
// ~0.5 s, per switch). The client and the daemon compute it identically
// because Ensure spawns the daemon from the client's own executable. When
// that path exceeds the platform limit, a stable hash of the absolute root
// selects a short path in os.TempDir(); both sides derive it identically,
// so the daemon still works.
func SocketPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	abs = filepath.Clean(abs)
	long := filepath.Join(abs, ".kern", "daemon-"+binaryKey()+".sock")
	if len(long) <= maxSocketPath {
		return long
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(os.TempDir(), "kern-daemon-"+hex.EncodeToString(sum[:8])+"-"+binaryKey()+".sock")
}

// binaryKey is the per-executable namespace of the socket name: eight hex
// characters of a digest over the running executable's path, mtime and size
// — the same freshness inputs BinaryIdentity uses, so every rebuild gets a
// fresh socket while two invocations of one build always agree.
func binaryKey() string {
	exe, err := os.Executable()
	if err != nil {
		return "static"
	}
	st, err := os.Stat(exe)
	if err != nil {
		return "static"
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", exe, st.ModTime().UnixNano(), st.Size())))
	return hex.EncodeToString(sum[:4])
}

// LogPath is where the daemon's own stderr goes (a crash must be diagnosable).
func LogPath(root string) string {
	return filepath.Join(root, ".kern", "daemon.log")
}

// IdleTimeout is how long an unused daemon stays alive. Override with
// KERN_DAEMON_IDLE (a Go duration); tests use a short value.
func IdleTimeout() time.Duration {
	if v := os.Getenv("KERN_DAEMON_IDLE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Minute
}

// BinaryIdentity is what a client and a daemon compare before answering. The
// version alone is not enough: `make install` rebuilds the binary in place and
// the version string stays "dev", so a stale daemon would keep serving old
// behavior. mtime+size changes on every rebuild.
func BinaryIdentity(version string) string {
	exe, err := os.Executable()
	if err != nil {
		return version
	}
	st, err := os.Stat(exe)
	if err != nil {
		return version
	}
	return fmt.Sprintf("%s|%d|%d", version, st.ModTime().UnixNano(), st.Size())
}

// Serve runs the accept loop until idle timeout, a stop request, or a signal.
// It is the body of `kern daemon serve`.
func Serve(root, version string, handle Handler) error {
	if !Supported() {
		return ErrUnsupported
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(abs, ".kern"), 0o755); err != nil {
		return err
	}
	sock := SocketPath(abs)
	// A socket file whose listener is gone (crash, kill -9) would make every
	// Dial fail forever; probe and clear it before binding.
	if _, statErr := os.Stat(sock); statErr == nil {
		conn, dialErr := net.DialTimeout("unix", sock, 300*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return fmt.Errorf("daemon already listening on %s", sock)
		}
		_ = os.Remove(sock)
	}
	ln, err := listenUnix(sock)
	if err != nil {
		return err
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(sock)
	}()

	identity := BinaryIdentity(version)
	idle := IdleTimeout()
	activity := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(idle)
		defer t.Stop()
		for {
			select {
			case <-activity:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(idle)
			case <-t.C:
				_ = ln.Close() // unblocks Accept; Serve returns nil
				return
			}
		}
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil // idle timeout or stop closed the listener
		}
		stop := serveConn(conn, identity, handle)
		select {
		case activity <- struct{}{}:
		default:
		}
		if stop {
			return nil
		}
	}
}

// serveConn handles one request and reports whether the daemon should exit.
func serveConn(conn net.Conn, identity string, handle Handler) bool {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Fatal: "protocol: " + err.Error()})
		return false
	}
	enc := json.NewEncoder(conn)
	pid := os.Getpid()
	switch req.Op {
	case OpPing:
		_ = enc.Encode(Response{Version: identityVersion(identity), Pid: pid, Identity: identity})
		return false
	case OpStop:
		_ = enc.Encode(Response{Pid: pid, Identity: identity})
		// Give the client a moment to read the reply before the socket dies.
		time.Sleep(20 * time.Millisecond)
		return true
	}
	if req.Identity != identity {
		_ = enc.Encode(Response{
			Fatal:    fmt.Sprintf("identity-mismatch: daemon=%s client=%s", identity, req.Identity),
			Identity: identity,
			Pid:      pid,
		})
		return false
	}
	resp := handle(req.Argv)
	resp.Identity = identity
	resp.Pid = pid
	_ = enc.Encode(resp)
	return false
}

// identityVersion strips the mtime|size suffix for `daemon status` output.
func identityVersion(identity string) string {
	if i := strings.IndexByte(identity, '|'); i >= 0 {
		return identity[:i]
	}
	return identity
}

// Call sends one request and decodes the reply.
func Call(root string, req Request, timeout time.Duration) (Response, error) {
	if !Supported() {
		return Response{}, ErrUnsupported
	}
	conn, err := net.DialTimeout("unix", SocketPath(root), 2*time.Second)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

// Ensure makes sure a daemon for root is running and speaks this binary's
// identity. It is idempotent and cheap on the happy path (one dial+ping).
func Ensure(root, version string) error {
	if !Supported() {
		return ErrUnsupported
	}
	identity := BinaryIdentity(version)
	if resp, err := Call(root, Request{Op: OpPing, Identity: identity}, 3*time.Second); err == nil {
		if resp.Identity == identity {
			return nil
		}
		// Different build answered: retire it, then start ours.
		_, _ = Call(root, Request{Op: OpStop}, 3*time.Second)
		time.Sleep(50 * time.Millisecond)
	}
	_ = os.Remove(SocketPath(root)) // stale file from a crash
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(LogPath(root), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon", "serve", "--root", root)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Env = append(os.Environ(),
		"KERN_DAEMON_CHILD=1",
		"KERN_INDEX_MEMCACHE=1",
	)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := Call(root, Request{Op: OpPing}, time.Second); err == nil {
			if resp.Identity == identity {
				return nil
			}
			return fmt.Errorf("daemon answered with a different build (%s)", resp.Identity)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within 10s (see %s)", LogPath(root))
}

// Stop asks the daemon to exit and waits for its socket to disappear.
func Stop(root string) error {
	if !Supported() {
		return ErrUnsupported
	}
	if _, err := Call(root, Request{Op: OpStop}, 3*time.Second); err != nil {
		_ = os.Remove(SocketPath(root))
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(SocketPath(root)); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// Status reports the running daemon's version and pid, if any.
func Status(root string) (Response, error) {
	return Call(root, Request{Op: OpPing}, 2*time.Second)
}

// FormatPid renders a pid for human output without importing strconv twice.
func FormatPid(pid int) string { return strconv.Itoa(pid) }
