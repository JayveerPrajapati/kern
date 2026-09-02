// Package relay broadcasts kern system events over a local Unix-domain
// socket so concurrently running agents, scripts, and shells can observe
// policy blocks, verification failures, and lock contention across
// process boundaries — the in-process eventbus is invisible to other
// processes; the relay is the cross-process half.
//
// The socket lives at <root>/.kern/events.sock, is restricted to the
// local user, and speaks line-delimited JSON:
//
//	server -> client: one eventbus.Event per line
//	client -> server: {"emit": {event fields}} to publish an event into
//	                  the owner's bus (used by `kern events --emit`)
//
// Fan-out never blocks the bus: each client has a bounded buffer and a
// client that falls behind is dropped. A crashed owner leaves a stale
// socket file; the next Start detects it (connect probe) and rebinds.
// When the socket is already owned by a live process, Start returns
// ErrSocketInUse and the caller simply runs without a relay.
//
// On platforms without usable AF_UNIX sockets, Start fails and callers
// degrade gracefully (the relay is an observability add-on, never a
// dependency).
package relay

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// ErrSocketInUse is returned when a live relay server already owns the
// socket path. The caller should continue without starting its own.
var ErrSocketInUse = errors.New("relay socket is owned by another live process")

// DefaultPath returns the canonical relay socket path for a workspace
// root.
func DefaultPath(root string) string {
	return filepath.Join(root, ".kern", "events.sock")
}

// maxUnixSocketPath is the portable bind limit: macOS's sun_path is 104
// bytes; stay under it with headroom for the temp-dir fallback.
const maxUnixSocketPath = 100

// SocketPath returns the path the relay actually binds for root: the
// canonical .kern/events.sock when it fits the OS socket-path limit,
// else a hashed short path under the system temp dir (deep workspace
// paths would otherwise fail to bind on macOS). Start and Dial both
// resolve through this, so the fallback is transparent.
func SocketPath(root string) string {
	p := DefaultPath(root)
	if len(p) <= maxUnixSocketPath {
		return p
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(os.TempDir(), fmt.Sprintf("kern-relay-%x.sock", sum[:8]))
}

// perClientBuffer bounds the events buffered per client before the
// client is dropped. Slow consumers must never stall the publisher.
const perClientBuffer = 64

// Server owns the relay socket and fans events out to clients.
type Server struct {
	path string

	mu      sync.Mutex
	clients map[*clientConn]struct{}
	closed  bool

	ln net.Listener

	// publish is invoked for client "emit" messages; wired to the bus
	// that owns this server. Nil disables remote emit.
	publishMu sync.RWMutex
	publish   func(eventbus.Event)
}

type clientConn struct {
	c   net.Conn
	buf chan []byte
}

// Start binds the relay socket for root and begins accepting clients.
// It returns ErrSocketInUse when a live server already owns the path.
func Start(root string) (*Server, error) {
	path := SocketPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s := &Server{path: path, clients: make(map[*clientConn]struct{})}
	ln, err := net.Listen("unix", path)
	if err != nil {
		// Either a live server owns the socket (probe it) or a stale
		// file from a crashed owner is in the way (remove and retry).
		if conn, perr := net.DialTimeout("unix", path, dialProbeTimeout); perr == nil {
			conn.Close()
			return nil, fmt.Errorf("%w: %s", ErrSocketInUse, path)
		}
		if rerr := os.Remove(path); rerr != nil {
			return nil, err
		}
		ln, err = net.Listen("unix", path)
		if err != nil {
			return nil, err
		}
	}
	// Local-only, same-user by default: the socket inherits the
	// directory's ownership; tighten the file mode explicitly.
	_ = os.Chmod(path, 0o600)
	s.ln = ln
	go s.acceptLoop()
	return s, nil
}

// dialProbeTimeout is how long Start waits when probing whether an
// existing socket file has a live owner.
const dialProbeTimeout = 500 * time.Millisecond

// SetPublisher wires the emit path: client "emit" messages are forwarded
// to fn (typically the owning bus's Publish).
func (s *Server) SetPublisher(fn func(eventbus.Event)) {
	s.publishMu.Lock()
	s.publish = fn
	s.publishMu.Unlock()
}

// Path returns the socket path.
func (s *Server) Path() string { return s.path }

// Broadcast fans one event out to every connected client. It never
// blocks: clients whose buffer is full are dropped.
func (s *Server) Broadcast(e eventbus.Event) {
	data, err := json.Marshal(e)
	if err != nil {
		return // events with unmarshalable payloads are skipped, not fatal
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for c := range s.clients {
		select {
		case c.buf <- data:
		default:
			// Slow client: drop it rather than stall the bus.
			delete(s.clients, c)
			close(c.buf)
			go c.c.Close()
		}
	}
}

// Close stops the server, drops all clients, and removes the socket
// file. Calling Close twice is safe.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for c := range s.clients {
		delete(s.clients, c)
		close(c.buf)
		c.c.Close()
	}
	s.mu.Unlock()
	if s.ln != nil {
		_ = s.ln.Close()
	}
	_ = os.Remove(s.path)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed
		}
		c := &clientConn{c: conn, buf: make(chan []byte, perClientBuffer)}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		go s.writeLoop(c)
		go s.readLoop(c)
	}
}

// writeLoop drains a client's buffer to the wire.
func (s *Server) writeLoop(c *clientConn) {
	for data := range c.buf {
		if _, err := c.c.Write(data); err != nil {
			s.drop(c)
			return
		}
	}
	// Buffer closed by Close/drop: close the wire too.
	_ = c.c.Close()
}

// readLoop handles client -> server messages (remote emit).
func (s *Server) readLoop(c *clientConn) {
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var msg struct {
			Emit *eventbus.Event `json:"emit"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.Emit == nil {
			continue // unknown message type: ignore, do not disconnect
		}
		s.publishMu.RLock()
		fn := s.publish
		s.publishMu.RUnlock()
		if fn != nil {
			fn(*msg.Emit)
		}
	}
	s.drop(c)
}

// drop removes a client (idempotent).
func (s *Server) drop(c *clientConn) {
	s.mu.Lock()
	if _, ok := s.clients[c]; ok {
		delete(s.clients, c)
		close(c.buf)
	}
	s.mu.Unlock()
	_ = c.c.Close()
}

// ---------------------------------------------------------------------------
// Client side.

// Client is a connected relay subscriber.
type Client struct {
	conn net.Conn
	sc   *bufio.Scanner
}

// Dial connects to the relay socket for root.
func Dial(root string) (*Client, error) {
	conn, err := net.Dial("unix", SocketPath(root))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, sc: bufio.NewScanner(conn)}, nil
}

// Next returns the next event, or an error when the server closed the
// connection. A read-deadline timeout is returned as-is; the internal
// scanner is rebuilt so subsequent Next calls keep working (bufio.Scanner
// is single-shot after any read error). Note: a timeout that lands
// mid-line drops that partial line — deadlines are a polling aid; the
// watch loop blocks without them.
func (c *Client) Next() (eventbus.Event, error) {
	var e eventbus.Event
	if !c.sc.Scan() {
		err := c.sc.Err()
		if err != nil && errors.Is(err, os.ErrDeadlineExceeded) {
			c.sc = bufio.NewScanner(c.conn)
		}
		if err != nil {
			return e, err
		}
		return e, net.ErrClosed
	}
	if err := json.Unmarshal(c.sc.Bytes(), &e); err != nil {
		return e, err
	}
	return e, nil
}

// Emit publishes an event through the owning server's bus.
func (c *Client) Emit(e eventbus.Event) error {
	msg := map[string]any{"emit": e}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(data, '\n'))
	return err
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// PublishPersisted publishes events both durably and live: each event is
// appended to the conventional persistence file (eventbus.DefaultPersistPath,
// replayed by relay owners and kern-server at startup) and, when a relay
// currently owns the socket, emitted through it so `kern events watch`
// subscribers and the owner's bus receive the events immediately instead of
// at the next replay. It is the one-shot producer counterpart to Client:
// dial, emit everything, hang up.
//
// Best-effort by design: persistence failures are logged by the bus, and a
// missing relay owner is not an error (the durable leg already ran). Events
// keep their explicit IDs so a persisting owner that re-appends a live-emitted
// copy produces a duplicate line that eventbus idempotency collapses at
// replay.
func PublishPersisted(root string, events []eventbus.Event) {
	if len(events) == 0 {
		return
	}
	bus := eventbus.New()
	bus.EnablePersistence(eventbus.DefaultPersistPath(root))
	for _, e := range events {
		bus.Publish(e)
	}
	c, err := Dial(root)
	if err != nil {
		return // no live owner; the durable leg already ran
	}
	defer c.Close()
	for _, e := range events {
		if c.Emit(e) != nil {
			return
		}
	}
}
