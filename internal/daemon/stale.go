package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// StaleDaemon is a LIVE daemon answering under a binary identity other than
// this executable's — typically a daemon from a previous build that still
// holds a hydrated index (and its memory) until its idle timeout. The current
// build never talks to it (the socket name embeds the binary key), so it is
// pure residue: auto-start spawns a fresh daemon on the new socket and the old
// one lingers invisibly.
type StaleDaemon struct {
	Socket   string `json:"socket"`
	Pid      int    `json:"pid"`
	Version  string `json:"version"`
	Identity string `json:"identity"`
}

// staleCandidateSockets returns every daemon socket that could belong to root
// under ANY binary key — the long <.kern>/daemon-<key>.sock form and the short
// temp-dir kern-daemon-<roothash>-<key>.sock form SocketPath picks for long
// roots (both sides derive the short name identically).
func staleCandidateSockets(root string) []string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	abs = filepath.Clean(abs)
	var sockets []string
	if long, err := filepath.Glob(filepath.Join(abs, ".kern", "daemon-*.sock")); err == nil {
		sockets = append(sockets, long...)
	}
	sum := sha256.Sum256([]byte(abs))
	short := filepath.Join(os.TempDir(), "kern-daemon-"+hex.EncodeToString(sum[:8])+"-*.sock")
	if matches, err := filepath.Glob(short); err == nil {
		sockets = append(sockets, matches...)
	}
	return sockets
}

// dialCall sends one request over a specific socket path. The deadline covers
// encode + decode, so a wedged listener cannot stall a caller.
func dialCall(sock string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.DialTimeout("unix", sock, timeout)
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

// stopSocket asks a live daemon to exit and confirms that its socket is gone.
// It deliberately never removes the socket itself: if the daemon fails to stop,
// the live endpoint must remain observable and retryable.
func stopSocket(sock string, requestTimeout, shutdownTimeout time.Duration) error {
	if _, err := dialCall(sock, Request{Op: OpStop}, requestTimeout); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stop request: %w", err)
	}
	deadline := time.Now().Add(shutdownTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not remove %s within %s", sock, shutdownTimeout)
}

// StaleDaemons reports the live daemons under root speaking a different binary
// identity than this executable. A socket that does not answer a ping is dead
// debris, not a daemon, and is left to StopAll to remove.
func StaleDaemons(root, version string) []StaleDaemon {
	if !Supported() {
		return nil
	}
	current := BinaryIdentity(version)
	currentSocket := SocketPath(root)
	var out []StaleDaemon
	for _, sock := range staleCandidateSockets(root) {
		if sock == currentSocket {
			continue
		}
		if _, err := os.Stat(sock); err != nil {
			continue // vanished between glob and stat
		}
		resp, err := dialCall(sock, Request{Op: OpPing}, 500*time.Millisecond)
		if err != nil || resp.Identity == "" || resp.Identity == current {
			continue
		}
		out = append(out, StaleDaemon{
			Socket:   sock,
			Pid:      resp.Pid,
			Version:  identityVersion(resp.Identity),
			Identity: resp.Identity,
		})
	}
	return out
}

// StopAll retires every live stale-identity daemon under root and sweeps dead
// socket files (crash/kill residue that would otherwise linger in .kern
// forever), returning how many live daemons were stopped. This is the remedy
// `kern doctor` points at, reached through `kern daemon stop`.
func StopAll(root, version string) (stopped int, err error) {
	if !Supported() {
		return 0, ErrUnsupported
	}
	currentSocket := SocketPath(root)
	var stopErrs []error
	for _, sock := range staleCandidateSockets(root) {
		if sock == currentSocket {
			continue
		}
		if _, pingErr := dialCall(sock, Request{Op: OpPing}, 500*time.Millisecond); pingErr != nil {
			if _, statErr := os.Stat(sock); os.IsNotExist(statErr) {
				continue // vanished between glob and ping
			}
			if removeErr := os.Remove(sock); removeErr != nil && !os.IsNotExist(removeErr) {
				stopErrs = append(stopErrs, fmt.Errorf("remove dead socket %s: %w", sock, removeErr))
			}
			continue
		}
		if stopErr := stopSocket(sock, time.Second, 2*time.Second); stopErr != nil {
			stopErrs = append(stopErrs, fmt.Errorf("stop daemon %s: %w", sock, stopErr))
			continue
		}
		stopped++
	}
	return stopped, errors.Join(stopErrs...)
}
