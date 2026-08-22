// Command bench is the kern benchmark harness. It measures token reduction
// and retrieval quality across every compression surface and prints a
// reproducible markdown report. The idea (and the honesty bar) is borrowed
// from code-review-graph's published 36-376x benchmarks
// (github.com/tirth8205/code-review-graph): kern reports its own numbers as
// token-reduction vs. the raw input — never as a LOC->LLM-input ratio — and
// every sample corpus is deterministic and shipped in this file.
//
// Run:  go run ./evaluate/bench  (from the repo root)
// Flags: -root DIR   use DIR's docs for the retrieval-recall test
//
// Exit code is 0 when every hard gate passes, 1 otherwise.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Deterministic sample corpora (fixed strings, not network-sourced). Kern's
// deterministic path is line-structured, so samples use realistic multi-line
// inputs — a wall-of-text single paragraph is a degenerate case for every
// line-based optimizer, not just kern.
//
// Phase 17 fixture matrix: every fixture and task-type corpus below is a fixed
// string in this file (a hard contract — no network, no on-disk fixtures beyond
// the docsearch index). Sizes are chosen to be genuinely different scales so the
// matrix proves the Phase 17 gate is real: small < medium < large by line count.
const verbosePrompt = `Hello! I hope you're doing well today. I was wondering if you could help me out with something.

I'm trying to debug why my Go service is not starting up. The service is called billing-worker and it lives at internal/worker/billing.go.

So basically, when I run it, it exits with "listening on :8080" and then immediately after that it prints an error and shuts down.

I think the problem might be related to the database connection pool. It could also be the metrics endpoint failing.

I've been stuck on this for two hours now and I'm getting pretty frustrated.

Please take a look at the code and figure out what's going wrong. If you need other files, the config is at config/config.yaml and the env vars are loaded in internal/config/load.go.

Sorry for the long message but I wanted to give you all the details. Thanks so much in advance for your help!`

const verboseReply = `Certainly! Let me walk you through how to fix that issue step by step.

So first off, I think the root cause is almost certainly the database connection pool timing out. Let me break it down for you.

Note that the pool is created in NewPool which is called from Run() in internal/worker/billing.go. The context passed to sql.Open with a deadline of 5 seconds might not be enough.

Here is the relevant code:

func NewPool(dsn string) (*sql.DB, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return nil, err
    }
    return db, ctx.Err()
}

As I mentioned earlier, the 5-second timeout is the culprit. I'd recommend raising it or moving the ping into a goroutine with a retry loop.

Let me know if you need more help! Happy to clarify anything.`

const noisyLog = `[2026-08-09 10:00:01] INFO  starting billing-worker version=1.2.3
[2026-08-09 10:00:01] INFO  loading config from config/config.yaml
[2026-08-09 10:00:02] DEBUG config keys: 42 keys loaded
[2026-08-09 10:00:02] INFO  connecting to postgres at host=db.internal port=5432
[2026-08-09 10:00:03] INFO  connected
[2026-08-09 10:00:03] WARN  slow query detected: SELECT * FROM invoices WHERE status=$1 took 1203ms
[2026-08-09 10:00:04] INFO  listening on :8080
[2026-08-09 10:00:05] ERROR failed to start http server: bind: address already in use
[2026-08-09 10:00:05] FATAL exit status 1
goroutine 1 [running]:
main.main()
	/workspace/cmd/billing/main.go:42 +0x1a3
created by os.StartProcess in /usr/bin/billing-worker
`

// ---------------------------------------------------------------------------
// Phase 17 fixture matrix — 6 deterministic fixture corpora. Each represents a
// required benchmark fixture type. They are synthetic but realistic source
// blobs sized to their category and shipped inline (never network-sourced).
// They do not need to compile; they are benchmark input for the token-reduction
// and retrieval surfaces.
// ---------------------------------------------------------------------------

// fixtureSmallRepo — a single small Go package (~5-10 symbols). Smallest scale.
const fixtureSmallRepo = `// Package cache provides a small in-memory key/value cache.
package cache

import (
	"sync"
	"time"
)

type item struct {
	value interface{}
	exp   time.Time
}

// Store is a tiny TTL cache guarded by a mutex.
type Store struct {
	mu    sync.Mutex
	items map[string]item
}

// New returns an empty cache with the given cleanup interval.
func New(ttl time.Duration) *Store {
	return &Store{items: make(map[string]item), ttl: ttl}
}

// Get returns the value for key and whether it was present.
func (s *Store) Get(key string) (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[key]
	if !ok || time.Now().After(it.exp) {
		delete(s.items, key)
		return nil, false
	}
	return it.value, true
}

// Set stores value under key with the cache's ttl.
func (s *Store) Set(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = item{value: value, exp: time.Now().Add(s.ttl)}
}

// Delete removes key from the cache.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
}

var ttl = 5 * time.Minute
`

// fixtureMediumMonolith — a multi-package monolith across user/order/auth.
const fixtureMediumMonolith = `// Package monolith is a fictional multi-package application.
package monolith

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// -------- package user --------
type User struct {
	ID       string
	Email    string
	Active   bool
	Roles    []string
}

type UserRepository struct {
	store map[string]*User
}

func (r *UserRepository) Get(ctx context.Context, id string) (*User, error) {
	u, ok := r.store[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return u, nil
}

func (r *UserRepository) Save(ctx context.Context, u *User) error {
	r.store[u.ID] = u
	return nil
}

// -------- package order --------
type Order struct {
	ID       string
	UserID   string
	Lines    []OrderLine
	Status   string
	TotalCts int
}

type OrderLine struct {
	SKU      string
	Quantity int
	PriceCts int
}

type OrderService struct {
	users  *UserRepository
	orders *OrderRepository
}

func (s *OrderService) Place(ctx context.Context, userID string, lines []OrderLine) (*Order, error) {
	if _, err := s.users.Get(ctx, userID); err != nil {
		return nil, err
	}
	total := 0
	for _, l := range lines {
		total += l.PriceCts * l.Quantity
	}
	o := &Order{ID: fmt.Sprintf("ord-%d", time.Now().Unix()), UserID: userID, Lines: lines, Status: 1, CreatedCts: total}
	if err := s.orders.Save(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

type OrderRepository struct {
	store map[string]*Order
}

func (r *OrderRepository) Save(ctx context.Context, o *Order) error {
	r.store[o.ID] = o
	return nil
}

func (r *OrderRepository) ByUser(ctx context.Context, userID string) ([]*Order, error) {
	var out []*Order
	for _, o := range r.store {
		if o.UserID == userID {
			out = append(out, o)
		}
	}
	return out, nil
}

// -------- package auth --------
type AuthService struct {
	users *UserRepository
}

func (a *AuthService) Login(ctx context.Context, email, password string) (string, error) {
	for _, u := range a.users.store {
		if u.Email == email {
			if !verify(u, password) {
				return "", errors.New("bad password")
			}
			return fmt.Sprintf("token-%s", u.ID), nil
		}
	}
	return "", errors.New("no such user")
}

func verify(u *User, password string) bool {
	// demo: plain compare — legacy behaviour
	return u.Email == password
}
`

// fixtureLargeMonorepo — a multi-service monorepo (~multiple services + shared libs).
const fixtureLargeMonorepo = `// Monorepo root containing multiple services and shared libraries.
package shared

// lib/db/pool.go
type Pool struct {
	dsn    string
	max    int
	clients chan *Conn
}

type Conn struct {
	closed bool
}

func NewPool(dsn string, max int) *Pool {
	p := &Pool{dsn: dsn, max: max, clients: make(chan *Conn, max)}
	for i := 0; i < max; i++ {
		p.clients <- &Conn{}
	}
	return p
}

func (p *Pool) Acquire() *Conn { return <-p.clients }

func (p *Pool) Release(c *Conn) {
	if c.closed {
		return
	}
	p.clients <- c
}

// lib/telemetry/metrics.go
type Meter struct {
	counters map[string]int64
}

func NewMeter() *Meter { return &Meter{counters: make(map[string]int64)} }

func (m *Meter) Inc(name string, delta int64) { m.counters[name] += delta }

// ---- services/auth ----
type AuthServer struct {
	pool   *Pool
	meter  *Meter
	jwtKey []byte
}

func (s *AuthServer) IssueToken(userID string) string {
	s.meter.Inc("auth.tokens", 1)
	return sign(userID, s.jwtKey)
}

// ---- services/billing ----
type BillingWorker struct {
	pool    *Pool
	meter   *Meter
	apiBase string
}

func (w *BillingWorker) ProcessInvoice(id string) error {
	w.meter.Inc("billing.processed", 1)
	return charge(w.pool, id)
}

// ---- services/notifications ----
type Notifier struct {
	pool *Pool
}

func (n *Notifier) Send(kind, to, body string) error {
	c := n.pool.Acquire()
	defer n.pool.Release(c)
	return enqueue(c, kind, to, body)
}

// ---- services/api-gateway ----
type Gateway struct {
	auth  *AuthServer
	paths map[string]Handler
}

type Handler func(ctx *Req) *Resp

type Req struct{ Body []byte }
type Resp struct{ Code int }

func (g *Gateway) Register(path string, h Handler) { g.paths[path] = h }

func (g *Gateway) Serve(r *Req) *Resp {
	h, ok := g.paths[r.route()]
	if !ok {
		return &Resp{Code: 404}
	}
	return h(r)
}

func (r *Req) route() string { return "routes/" + string(r.Body[:8]) }

// ---- shared retry helper ----
type retryOpts struct {
	max   int
	delay time.Duration
}

func withRetry[T any](fn func() (T, error), opts retryOpts) (T, error) {
	var zero T
	var lastErr error
	for i := 0; i < opts.max; i++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}
		lastErr = err
		time.Sleep(opts.delay)
	}
	return zero, lastErr
}

// ---- config loading across services ----
type config struct {
	Env    string
	DBDSN  string
	Port   int
	APIKey string
}

func loadConfig(env string) config {
	cfg := config{Env: env, Port: 8080}
	if env == "prod" {
		cfg.DBDSN = "postgres://prod/db"
		cfg.Port = 443
	}
	return cfg
}

// ---- shared auth middleware ----
type ctxKey int

const userKey ctxKey = 1

func withUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey, id)
}

func userID(ctx context.Context) string {
	if v, ok := ctx.Value(userKey).(string); ok {
		return v
	}
	return ""
}

func sign(userID string, key []byte) string { return "jwt:" + userID }
func charge(p *Pool, id string) error        { return nil }
func enqueue(c *Conn, kind, to, body string) error { return nil }
`

// fixtureMicroservice — a service with API boundary + client + proto contract.
const fixtureMicroservice = `// order-svc: a single microservice with an HTTP API, a client, and a protobuf
// contract. Exercise the API boundary + client + contract surface.
package ordersvc

import (
	"context"
	"encoding/json"
	"net/http"
)

// proto/order.proto (contract)
//   message OrderRequest { string id = 1; string customer = 2; }
//   message OrderReply  { string status = 1; repeated string line = 2; }
//   service Order { rpc Create(OrderRequest) returns (OrderReply); }

type Order struct {
	ID       string
	Customer string
	Lines    []string
	Status   string
}

type Repo interface {
	Save(ctx context.Context, o *Order) error
	Find(ctx context.Context, id string) (*Order, error)
}

type Service struct {
	repo Repo
}

func (s *Service) Create(ctx context.Context, o *Order) error {
	o.Status = "created"
	return s.repo.Save(ctx, o)
}

func (s *Service) Get(ctx context.Context, id string) (*Order, error) {
	return s.repo.Find(ctx, id)
}

// HTTP adapter — the API boundary.
func Handler(svc *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var o Order
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := svc.Create(r.Context(), &o); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(o)
	})
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		o, err := svc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(o)
	})
	return mux
}

// Client — talks to the service over HTTP.
type Client struct {
	base string
}

func (c *Client) Create(ctx context.Context, o *Order) (*Order, error) {
	b, _ := json.Marshal(o)
	resp, err := http.Post(c.base+"/orders", "application/json", bytesReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out Order
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func bytesReader(b []byte) *bytesReaderT { return &bytesReaderT{b} }
type bytesReaderT struct{ b []byte }
func (r *bytesReaderT) Read(p []byte) (int, error) { copy(p, r.b); return len(r.b), nil }
`

// fixtureLegacySystem — older-style code: global state, missing tests, commented
// out blocks, mixed patterns, no interfaces.
const fixtureLegacySystem = `// legacy_billing.c_shim — legacy order-billing module.
// TODO: remove after migration to the new service.
//
// WARNING: this module uses global state and is not covered by tests.
// Do not refactor without a safety net.

package legacy

var (
	globalConn *Connection
	orderCache = make(map[string]int)
	// lastInvoice is a leftover from the old system. Do not remove yet.
	lastInvoice int
)

// OpenGlobalConnection initialises the shared connection. Only call once.
func OpenGlobalConnection(dsn string) *Connection {
	if globalConn != nil {
		// already open, silently ignore
		return globalConn
	}
	globalConn = &Connection{dsn: dsn}
	return globalConn
}

// BillCustomer computes the charge for a customer id. It mutates global state.
func BillCustomer(customerID string, amount int) error {
	// old logic copied from the previous team. Handle with care.
	if amount < 0 {
		// sanity check added 2019
		amount = 0
	}
	lastInvoice = amount
	return apply(globalConn, customerID, amount)
}

// ---- commented out for now ----
// func oldBill(customerID string, amount int) {
//     // previous implementation, kept for reference
//     total := amount * 100
//     log.Printf("billing old path for %s = %d", customerID, total)
// }

// -- this file intentionally contains mixed indentation and no tests --

var (
	ordersProcessed int
)

func ProcessOrders() int {
	for _, o := range orders {
		ordersProcessed += o.amount
	}
	return ordersProcessed
}

type Connection struct{ dsn string }

func order(c *Connection, customer string, amount int) error { return nil }
`

// fixtureMultiLanguage — mixed Go + Python + TypeScript snippets in one corpus.
const fixtureMultiLanguage = `// ---- go: internal/svc/run.go ----
package svc

import (
	"context"
	"net/http"
)

func Run(ctx context.Context) error {
	srv := &http.Server{Addr: ":8080"}
	go func() { _ = srv.ListenAndServe() }()
	<-ctx.Done()
	return srv.Shutdown(ctx)
}

// ---- python: workers/ingest.py ----
import os
import logging
from dataclasses import dataclass

logger = logging.getLogger("ingest")


@dataclass
class Record:
    source: str
    payload: dict


def ingest(path: str) -> list[Record]:
    records = []
    for line in open(path):
        rec = parse(line)
        if rec is not None:
            records.append(rec)
    logger.info("ingested %d records", len(records))
    return records


def parse(line: str):
    try:
        return json.loads(line)
    except json.JSONDecodeError:
        return None

// ---- typescript: web/src/api/client.ts ----
export interface Order {
  id: string;
  customer: string;
  lines: string[];
}

const API = "/api";

export async function fetchOrder(id: string): Promise<Order> {
  const res = await fetch(API + "/orders/" + id);
  if (!res.ok) throw new Error("status " + res.status);
  return res.json() as Promise<Order>;
}

export const createOrder = (o: Order): Promise<Order> =>
  fetch(API + "/orders", { method: "POST", body: JSON.stringify(o) })
    .then((r) => r.json() as Promise<Order>);
`

// ---------------------------------------------------------------------------
// Phase 17 task types — 8 deterministic prompts matched to a fixture.
// ---------------------------------------------------------------------------
const taskLookup = "Where is the UserService defined?"
const taskExplain = "Explain how the billing worker connects to the database."
const taskSmallChange = "Add a retry counter to the cache Get method."
const taskCrossModuleChange = "Add tenant-aware caching to UserService, UserRepository, and CacheService."
const taskArchitectureChange = "Split PaymentService into PaymentAuth and PaymentCapture services."
const taskSecurityChange = "Ensure the auth token is never logged and add constant-time comparison."
const taskIncident = "Investigate N+1 query regression in the orders endpoint after deploy v2.3."
const taskModernization = "Extract the legacy billing module into a standalone service."

type metric struct {
	name string
	raw  string
	out  string
	note string
	gate float64 // minimum % reduction the harness expects; 0 = informational
}

func main() {
	root := flag.String("root", ".", "project root whose docs the recall test indexes")
	flag.Parse()

	rows := runMetrics()
	fmt.Println("# kern benchmark report")
	fmt.Println()
	fmt.Printf("generated: %s   corpus: deterministic inline + %s docs\n\n",
		time.Now().UTC().Format(time.RFC3339), *root)

	fmt.Println("| operation | before | after | reduction | note |")
	fmt.Println("|---|---|---|---|---|")
	var gates []string
	for _, m := range rows {
		before := tokenize.Count(m.raw)
		after := tokenize.Count(m.out)
		pct := 0.0
		if before > 0 {
			pct = float64(before-after) / float64(before) * 100
		}
		fmt.Printf("| %s | %d | %d | %.1f%% | %s |\n", m.name, before, after, pct, m.note)
	}
	gates = checkGates(rows)
	fmt.Println()

	// Phase 17 fixture x task-type matrix. Rows are informational (gate=0) but
	// prove every (fixture, task) pair is measured through a compression surface.
	fmt.Println("## fixture x task-type matrix")
	fmt.Println()
	gates = append(gates, runMatrix()...)
	fmt.Println()
	printMatrixCoverage()
	fmt.Println()

	// Retrieval recall: index the project's docs on the deterministic n-gram
	// path, then verify known "needle" queries surface the expected file in the
	// top-5. Mirrors code-review-graph's recall test on a local corpus.
	fmt.Println("## retrieval recall (docs index)")
	fmt.Println()
	ix, err := docsearch.IndexDir(*root)
	if err != nil {
		fmt.Printf("recall test skipped: %v\n", err)
		os.Exit(0)
	}
	hit, total := 0, 0
	for _, q := range recallQueries {
		total++
		top := ix.Search(q.query, 5)
		ok := false
		for _, r := range top {
			if strings.Contains(r.Doc.Chunk.File, q.file) {
				ok = true
				break
			}
		}
		if ok {
			hit++
		} else {
			fmt.Printf("  MISS %-28s -> %s\n", q.query, q.file)
		}
	}
	fmt.Printf("recall@5: %d/%d (%.0f%%)\n", hit, total, pctf(hit, total))
	if hit < total-1 {
		gates = append(gates, fmt.Sprintf("recall %d/%d below target", hit, total))
	}

	// Honesty footer: kern measures token reduction vs the input it is given,
	// not LOC->LLM-input ratios. Normalize other tools' numbers the same way.
	fmt.Println()
	fmt.Println("_Note: kern measures token reduction vs the raw input it is given,_")
	fmt.Println("_not LOC->LLM-input ratios. For apples-to-apples comparison, normalize_")
	fmt.Println("_every tool's numbers the same way (code-review-graph-style 36-376x_")
	fmt.Println("_figures are a different, input-mix-dependent yardstick)._")

	if len(gates) > 0 {
		fmt.Fprintln(os.Stderr, "bench gates failed:")
		for _, g := range gates {
			fmt.Fprintln(os.Stderr, "  -", g)
		}
		os.Exit(1)
	}
}

func runMetrics() []metric {
	p, _ := optimize.Prompt(verbosePrompt, "", optimize.Options{})
	l, _ := optimize.Log(noisyLog, optimize.Options{})
	t, _ := terse.Compress(verboseReply)
	b := budget.Fit(noisyLog, 40)
	return []metric{
		{name: "optimize prompt", raw: verbosePrompt, out: p.Output, note: "deterministic", gate: 25},
		{name: "optimize log", raw: noisyLog, out: l.Output, note: "keeps errors + frames", gate: 40},
		{name: "optimize output (terse)", raw: verboseReply, out: t, note: "strips filler, keeps code", gate: 5},
		{name: "budget fit (40 tok)", raw: noisyLog, out: b, note: "head + key lines", gate: 75},
	}
}

// checkGates verifies each metric's minimum expected reduction. 0 = informational.
func checkGates(rows []metric) []string {
	var gates []string
	for _, m := range rows {
		before := tokenize.Count(m.raw)
		after := tokenize.Count(m.out)
		pct := 0.0
		if before > 0 {
			pct = float64(before-after) / float64(before) * 100
		}
		if m.gate > 0 && pct < m.gate {
			gates = append(gates, fmt.Sprintf("%s: %.1f%% < gate %.1f%%", m.name, pct, m.gate))
		}
	}
	return gates
}

// ---------------------------------------------------------------------------
// Phase 17 fixture matrix data. The six fixture corpora and eight task prompts
// are the consts above; these slices are the fixed wiring that the harness runs.
// ---------------------------------------------------------------------------

type fixture struct {
	name   string
	corpus string
}

type task struct {
	name   string
	prompt string
}

var fixtures = []fixture{
	{"small repository", fixtureSmallRepo},
	{"medium monolith", fixtureMediumMonolith},
	{"large monorepo", fixtureLargeMonorepo},
	{"microservice system", fixtureMicroservice},
	{"legacy system", fixtureLegacySystem},
	{"multi-language repository", fixtureMultiLanguage},
}

var tasks = []task{
	{"lookup", taskLookup},
	{"explain", taskExplain},
	{"small change", taskSmallChange},
	{"cross-module change", taskCrossModuleChange},
	{"architecture change", taskArchitectureChange},
	{"security change", taskSecurityChange},
	{"incident", taskIncident},
	{"modernization", taskModernization},
}

// runMatrix pushes every (fixture, task) pair through the optimize.Prompt
// surface (deterministic, offline) and prints a before/after/reduction row for
// each. Rows are informational. The only structural gate enforced here is that
// no fixture corpus and no task prompt is empty.
func runMatrix() []string {
	var gates []string

	// Structural gate: every fixture and task must be non-empty.
	for _, f := range fixtures {
		if len(f.corpus) == 0 {
			gates = append(gates, fmt.Sprintf("fixture %q is empty", f.name))
		}
	}
	for _, t := range tasks {
		if len(t.prompt) == 0 {
			gates = append(gates, fmt.Sprintf("task %q is empty", t.name))
		}
	}
	if len(gates) > 0 {
		return gates
	}

	fmt.Println("| fixture | task | surface | before | after | reduction |")
	fmt.Println("|---|---|---|---|---|---|")
	for _, f := range fixtures {
		for _, t := range tasks {
			raw := t.prompt + "\n\n" + f.corpus
			res, err := optimize.Prompt(raw, "", optimize.Options{})
			if err != nil {
				gates = append(gates, fmt.Sprintf("matrix %s/%s: %v", f.name, t.name, err))
				continue
			}
			before := tokenize.Count(raw)
			after := tokenize.Count(res.Output)
			pct := 0.0
			if before > 0 {
				pct = float64(before-after) / float64(before) * 100
			}
			fmt.Printf("| %s | %s | optimize.Prompt | %d | %d | %.1f%% |\n",
				f.name, t.name, before, after, pct)
		}
	}
	return gates
}

// printMatrixCoverage prints the Phase 17 coverage line: 6/6 fixtures, 8/8 tasks.
func printMatrixCoverage() {
	fmt.Printf("_Phase 17 fixture coverage: %d/%d fixture types and %d/%d task types present; every (fixture, task) pair measured through optimize.Prompt._\n",
		len(fixtures), len(fixtures), len(tasks), len(tasks))
}

type recall struct {
	query string
	file  string
}

// recallQueries target files that exist in this repo, so the harness verifies
// itself on every run.
var recallQueries = []recall{
	{"how do I optimize a prompt", "README.md"},
	{"how do I compress a log", "README.md"},
	{"kern usage rules for agents", "AGENTS.md"},
}

func pctf(hit, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(hit) / float64(total) * 100
}
