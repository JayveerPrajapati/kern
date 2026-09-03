//go:build !windows

package relay

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

func mustStart(t *testing.T, root string) *Server {
	t.Helper()
	s, err := Start(root)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s
}

func TestDefaultPath(t *testing.T) {
	if got := DefaultPath("/w"); got != "/w/.kern/events.sock" {
		t.Errorf("DefaultPath = %q", got)
	}
}

// broadcastUntil repeatedly broadcasts e until the client receives any
// event or the deadline passes. Dial returns as soon as the connection
// is queued, but the server registers the client asynchronously in its
// accept loop — an immediate single Broadcast races that registration.
func broadcastUntil(t *testing.T, s *Server, c *Client, e eventbus.Event) eventbus.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.Broadcast(e)
		_ = c.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if got, err := c.Next(); err == nil {
			return got
		}
	}
	t.Fatalf("client never received the broadcast")
	return eventbus.Event{}
}

func TestFanOutToClients(t *testing.T) {
	root := t.TempDir()
	s := mustStart(t, root)
	defer s.Close()

	const n = 3
	clients := make([]*Client, n)
	for i := range clients {
		c, err := Dial(root)
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		defer c.Close()
		clients[i] = c
	}

	want := eventbus.Event{ID: "e-1", Kind: eventbus.LockAcquired, Source: "test", Subject: "scope-a"}
	for i, c := range clients {
		got := broadcastUntil(t, s, c, want)
		if got.Kind != eventbus.LockAcquired || got.Subject != "scope-a" {
			t.Errorf("client %d got %+v", i, got)
		}
	}
}

func TestSlowClientDropped(t *testing.T) {
	root := t.TempDir()
	s := mustStart(t, root)
	defer s.Close()

	slow, err := Dial(root) // never reads
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	fast, err := Dial(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()

	// Registration is asynchronous: wait until both clients are actually
	// wired to the server before flooding, so the overflow is attributable
	// to the slow client's buffer alone and never drops the fast client.
	deadline := time.Now().Add(3 * time.Second)
	for len(allClients(s)) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("clients never registered")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Overflow the slow client's buffer (> perClientBuffer, never read):
	// it must be dropped while the fast client, drained as we go, keeps
	// receiving and Broadcast never blocks.
	for i := 0; i < perClientBuffer*3; i++ {
		s.Broadcast(eventbus.Event{ID: "e", Kind: eventbus.LockContended, Source: "test"})
		// Drain anything fast may have received so it never fills its own
		// buffer and is never dropped by the storm.
		_ = fast.conn.SetReadDeadline(time.Now().Add(2 * time.Millisecond))
		for {
			if _, err := fast.Next(); err != nil {
				break
			}
		}
	}

	// The fast client must still be receiving after the slow one is
	// dropped.
	got := 0
	s.Broadcast(eventbus.Event{ID: "e", Kind: eventbus.LockContended, Source: "test"})
	_ = fast.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err := fast.Next(); err != nil {
			break
		}
		got++
	}
	if got == 0 {
		t.Errorf("fast client received nothing after slow client dropped")
	}
}

// allClients snapshots the server's registered clients under its lock.
func allClients(s *Server) []*clientConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*clientConn, 0, len(s.clients))
	for c := range s.clients {
		out = append(out, c)
	}
	return out
}

func mkStale(root string) error {
	p := SocketPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte("stale"), 0o600)
}

// TestSocketPathFallback verifies that roots whose canonical socket path
// exceeds the OS limit (macOS sun_path is 104 bytes) transparently bind
// under a hashed temp path, and Start/Dial agree on it.
func TestSocketPathFallback(t *testing.T) {
	// Build a root deep enough that its canonical socket path exceeds the
	// OS bind limit regardless of t.TempDir() length (e.g. short /tmp on
	// Linux vs long /var/folders on macOS). Padding until 12 levels exceed
	// it keeps the test deterministic on every host.
	deep := filepath.Join(t.TempDir(), strings.Repeat("d/", 12)+"workspace")
	for len(DefaultPath(deep)) <= maxUnixSocketPath {
		deep = filepath.Join(deep, "dd")
	}
	p := SocketPath(deep)
	if p == DefaultPath(deep) {
		t.Fatalf("expected hashed fallback path, got %q", p)
	}
	if !strings.HasPrefix(p, os.TempDir()) {
		t.Errorf("fallback path %q not under temp dir", p)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	s := mustStart(t, deep)
	defer s.Close()
	c, err := Dial(deep)
	if err != nil {
		t.Fatalf("Dial on fallback path: %v", err)
	}
	defer c.Close()
	s.Broadcast(eventbus.Event{Kind: eventbus.LockAcquired, Subject: "deep"})
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := broadcastUntil(t, s, c, eventbus.Event{Kind: eventbus.LockAcquired, Subject: "deep"})
	if got.Subject != "deep" {
		t.Errorf("Next on fallback path got %+v", got)
	}
}

func TestStaleSocketRecovery(t *testing.T) {
	root := t.TempDir()
	if err := mkStale(root); err != nil {
		t.Fatal(err)
	}
	s := mustStart(t, root) // must recover from the stale file
	defer s.Close()
	c, err := Dial(root)
	if err != nil {
		t.Fatalf("Dial after stale recovery: %v", err)
	}
	defer c.Close()
}

func TestSocketInUse(t *testing.T) {
	root := t.TempDir()
	s := mustStart(t, root)
	defer s.Close()

	second, err := Start(root)
	if err == nil {
		second.Close()
		t.Fatalf("second Start should fail")
	}
	if !errors.Is(err, ErrSocketInUse) {
		t.Errorf("err = %v, want ErrSocketInUse", err)
	}
}

func TestEmitThroughServer(t *testing.T) {
	root := t.TempDir()
	s := mustStart(t, root)
	defer s.Close()

	published := make(chan eventbus.Event, 1)
	s.SetPublisher(func(e eventbus.Event) { published <- e })

	c, err := Dial(root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Emit(eventbus.Event{
		Kind:    eventbus.LockReleased,
		Source:  "cli",
		Subject: "scope-b",
		Payload: map[string]any{"pid": 123},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case e := <-published:
		if e.Kind != eventbus.LockReleased || e.Subject != "scope-b" {
			t.Errorf("emitted event = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("emit was not forwarded to the publisher")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	root := t.TempDir()
	s := mustStart(t, root)
	s.Close()
	s.Close() // must not panic
}
