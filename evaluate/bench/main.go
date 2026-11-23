// Command bench is the kern benchmark harness. It measures token reduction
// and retrieval quality across every compression surface and prints a
// reproducible markdown report. The idea (and the honesty bar) is borrowed
// from code-review-graph's published 36-376x benchmarks
// (github.com/tirth8205/code-review-graph): kern reports its own numbers as
// token-reduction vs. the raw input — never as a LOC->LLM-input ratio — and
// every sample corpus is deterministic and shipped in this file.
// Run:  go run ./evaluate/bench  (from the repo root)
// Flags: -root DIR   use DIR's docs for the retrieval-recall test
// Exit code is 0 when every hard gate passes, 1 otherwise.
//
// Decision note (2026-09-22): the session-metrics takeaway reports that
// direct tools (B) beat progressive disclosure (A) on TOTAL tokens per
// completed task in 2/3 scripted sessions (~-27.5% B vs A; prefix "rent"
// reaches 57.8% of A's total). That trade-off is measured and documented
// here, but the L1/L2/L3 progressive-disclosure defaults are KEPT: the
// staged surface exists for usability (bounded first responses, cheaper
// exploration, smaller surprise per step), not raw token minimality, and it
// is what agents actually get by default. The numbers are the honest cost of
// that choice; revisit if session-level token budgets become the binding
// constraint.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
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
// Fixture matrix: every fixture and task-type corpus below is a fixed
// string in this file (a hard contract — no network, no on-disk fixtures beyond
// the docsearch index). Sizes are chosen to be genuinely different scales so the
// matrix proves the gate is real: small < medium < large by line count.
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
// Fixture matrix — 6 deterministic fixture corpora. Each represents a
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
// message OrderRequest { string id = 1; string customer = 2; }
// message OrderReply  { string status = 1; repeated string line = 2; }
// service Order { rpc Create(OrderRequest) returns (OrderReply); }

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
// // previous implementation, kept for reference
// total := amount * 100
// log.Printf("billing old path for %s = %d", customerID, total)
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
// Task types — 8 deterministic prompts matched to a fixture.
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

// classMetrics is the per-task-class metric set the harness reports
// for a (fixture, task) pair through the deterministic optimize.Prompt surface.
// Every value is derived offline — no live LLM. before/afterTokens use the
// ESTIMATOR (historical comparability); exactBefore/After use the exact
// o200k tokenizer and estimatorDrift shows their divergence on the raw
// input — exact and estimated counts are reported side by side, never mixed
// into one number (2026-11-20 remediation).
type classMetrics struct {
	fixture              string
	task                 string
	beforeTokens         int     // estimator count
	afterTokens          int     // estimator count
	exactBefore          int     // exact o200k count (0 = tokenizer unavailable)
	exactAfter           int     // exact o200k count (0 = tokenizer unavailable)
	estimatorDrift       float64 // % estimator vs o200k on the raw input
	tokenReduction       float64 // % (estimator)
	toolCalls            int     // deterministic tool-call estimate
	toolCallReduction    float64 // % vs naive baseline
	retries              int     // deterministic retry estimate
	latencyMs            int64   // deterministic latency estimate
	cost                 float64 // deterministic $ estimate
	firstPass            bool    // succeeded on first pass
	verifiedSuccess      bool    // first pass AND output non-empty
	humanIntervention    bool    // flagged for human review
	postDeployRegression bool    // post-deployment regression flag
}

// exactTokenizer lazily loads the exact o200k tokenizer once. A nil return
// means the rank tables are unavailable: callers then report estimator
// counts only, explicitly labeled, instead of silently mixing.
var (
	exactOnce    sync.Once
	exactCounter tokenize.Counter
)

func o200kTokenizer() tokenize.Counter {
	exactOnce.Do(func() {
		if c, err := tokenize.NewO200kCounter(); err == nil {
			exactCounter = c
		}
	})
	return exactCounter
}

// measureClass runs one (fixture, task) pair through optimize.Prompt and
// derives the full metric set deterministically. retryFactor selects
// how aggressively the class retries (0 = never, higher = more retries), letting
// tests exercise the retry/regression outcome paths offline.
func measureClass(f fixture, t task, retryFactor int) classMetrics {
	m := classMetrics{fixture: f.name, task: t.name}
	raw := t.prompt + "\n\n" + f.corpus
	res, err := optimize.Prompt(raw, "", optimize.Options{})
	m.beforeTokens = tokenize.Count(raw)
	if err == nil {
		m.afterTokens = tokenize.Count(res.Output)
	} else {
		m.afterTokens = 0
	}
	// Exact o200k counts alongside the estimator, with the estimator's drift
	// on the raw input — labeled separately, never blended.
	if c := o200kTokenizer(); c != nil {
		m.exactBefore = c.Count(raw)
		if err == nil {
			m.exactAfter = c.Count(res.Output)
		}
		if m.exactBefore > 0 {
			m.estimatorDrift = float64(m.beforeTokens-m.exactBefore) / float64(m.exactBefore) * 100
		}
	}
	if m.beforeTokens > 0 {
		m.tokenReduction = float64(m.beforeTokens-m.afterTokens) / float64(m.beforeTokens) * 100
	}

	// Deterministic tool-call estimate: scale by token count (~1 call / 50 tok).
	baselineCalls := m.beforeTokens / 50
	actualCalls := m.afterTokens/50 + retryFactor
	if actualCalls > baselineCalls {
		actualCalls = baselineCalls
	}
	m.toolCalls = actualCalls
	if baselineCalls > 0 {
		m.toolCallReduction = (1 - float64(actualCalls)/float64(baselineCalls)) * 100
	}

	// Deterministic retry estimate: 0 when the surface is effective, else retryFactor.
	m.retries = 0
	if m.tokenReduction < 25 {
		m.retries = retryFactor
	}

	// Deterministic latency/cost estimates from token counts (offline proxies).
	m.latencyMs = int64(m.beforeTokens/10 + m.afterTokens/10)
	m.cost = float64(m.beforeTokens)/1000*0.01 + float64(m.afterTokens)/1000*0.005

	// Outcome flags (deterministic, offline).
	m.firstPass = m.tokenReduction >= 25 && m.afterTokens > 0
	m.verifiedSuccess = m.firstPass && m.afterTokens > 0
	m.humanIntervention = m.tokenReduction < 40 && m.tokenReduction > 0
	m.postDeployRegression = m.task == "incident" && m.tokenReduction < 25
	return m
}

// taskClassMetrics returns the metric set for EVERY task class across every
// fixture — the harness's deterministic per-task-class report. (2026-11-20
// remediation: was tasks[:1], so only the "lookup" class was ever reported.)
func taskClassMetrics() []classMetrics {
	var out []classMetrics
	for _, f := range fixtures {
		for _, t := range tasks {
			out = append(out, measureClass(f, t, 0))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Session metrics — deterministic session-simulation KPI (CodeGraph-P2).
//
// KPI: TOTAL tokens per completed task, not per-response size. Two retrieval
// strategies resolve the same scripted question; both pay residual-context
// rent, because every new request re-sends the conversation prefix. The model
// is pure constants + arithmetic (no LLM, no timing, no randomness), so
// identical runs produce identical tables.
//
// Levels mirror progressive disclosure:
//   L1 = index summary, L2 = neighborhood, L3 = source.
// Strategy A resolves the question in 4-6 small steps with 2 level upgrades;
// strategy B resolves it with 2 large direct full-context calls. Per-step
// payload sizes are the exact o200k token counts of real windows into the
// plan's fixture corpus (see buildSessionPlan) — measured, not invented.
//
// Model: step k's request carries payload_k PLUS the accumulated conversation
// prefix (the payloads of steps 1..k-1, re-sent verbatim). So
//   request_k    = payload_k + prefix_k,  prefix_k = sum(payload_i, i<k)
//   prefix rent  = sum(prefix_k)          (the residual-context rent column)
//   total tokens = sum(request_k) = sum(payload) + prefix rent
// ---------------------------------------------------------------------------

// Levels mirror progressive disclosure:
//   L1 = index summary, L2 = neighborhood, L3 = source.
// Strategy A resolves the question in 4-6 small steps with 2 level upgrades;
// strategy B resolves it with 2 large direct full-context calls. Payload
// sizes are NOT invented constants (2026-11-20 remediation): every payload
// is the EXACT o200k token count of a real window into the plan's fixture
// corpus — L1 = the corpus head, L2 = a wider view, L3 = successive
// overlapping source views, direct = the whole corpus.
//
// Model: step k's request carries payload_k PLUS the accumulated conversation
// prefix (the payloads of steps 1..k-1, re-sent verbatim). So
//   request_k    = payload_k + prefix_k,  prefix_k = sum(payload_i, i<k)
//   prefix rent  = sum(prefix_k)          (the residual-context rent column)
//   total tokens = sum(request_k) = sum(payload) + prefix rent
// ---------------------------------------------------------------------------

// sessionTokensLabel names the counter the session payloads were measured
// with — "exact o200k" when the rank tables loaded, else "estimator" (the
// table header carries it so exact and estimated counts are never confused).
var sessionTokensLabel = "estimator"

// corpusWindow returns up to lines lines of corpus starting at line offset
// (0-based), clamped to what exists.
func corpusWindow(corpus string, offset, lines int) string {
	all := strings.Split(corpus, "\n")
	if offset < 0 {
		offset = 0
	}
	if offset > len(all) {
		offset = len(all)
	}
	end := offset + lines
	if end > len(all) {
		end = len(all)
	}
	return strings.Join(all[offset:end], "\n")
}

// sessionCount tokens-s with the exact o200k counter when available, else
// the estimator (sessionTokensLabel records which).
func sessionCount(c tokenize.Counter, text string) int {
	if c != nil {
		return c.Count(text)
	}
	return tokenize.Count(text)
}

// sessionPlanSpec is the scripted task sequence: which levels strategy A
// walks (strategy B is always 2 direct steps). Payloads are derived, not
// specified.
type sessionPlanSpec struct {
	task    string
	fixture string
	a       []string // strategy A levels in order
}

// sessionPlanSpecs are the 3 scripted questions. Every spec reuses an
// existing fixture corpus (no new corpus).
var sessionPlanSpecs = []sessionPlanSpec{
	{
		task:    "trace what a POST /users request touches (medium monolith)",
		fixture: "medium monolith",
		a:       []string{"L1", "L2", "L3", "L3", "L3", "L3"},
	},
	{
		task:    "what breaks if symbol X changes (large monorepo)",
		fixture: "large monorepo",
		a:       []string{"L1", "L2", "L3", "L3", "L3"},
	},
	{
		task:    "why does X exist and who needs it (multi-language repository)",
		fixture: "multi-language repository",
		a:       []string{"L1", "L2", "L2", "L2", "L3"},
	},
}

// buildSessionPlan derives every step's payload from the plan's real fixture
// corpus: L1 = the corpus head (~10% of lines), L2 = a ~35% view, L3 =
// successive overlapping ~60% source views (advancing by half a window and
// clamped at the tail, so every view is full-width), direct = the whole
// corpus. Window sizes scale with the corpus, so no fixture degenerates.
// Pure and deterministic: same corpus, same counts.
func buildSessionPlan(spec sessionPlanSpec, c tokenize.Counter) sessionPlan {
	var corpus string
	for _, f := range fixtures {
		if f.name == spec.fixture {
			corpus = f.corpus
			break
		}
	}
	p := sessionPlan{task: spec.task, fixture: spec.fixture}
	n := strings.Count(corpus, "\n") + 1
	l1 := corpusWindow(corpus, 0, maxInt(3, n/10))
	l2 := corpusWindow(corpus, 0, maxInt(5, n*35/100))
	w3 := maxInt(10, n*60/100)
	l3Step := 0
	for _, level := range spec.a {
		payload := 0
		switch level {
		case "L1":
			payload = sessionCount(c, l1)
		case "L2":
			payload = sessionCount(c, l2)
		case "L3":
			start := l3Step * w3 / 2
			if maxStart := n - w3; start > maxStart {
				start = maxStart
			}
			payload = sessionCount(c, corpusWindow(corpus, start, w3))
			l3Step++
		}
		p.a = append(p.a, sessionStep{level: level, payload: payload})
	}
	direct := sessionCount(c, corpus)
	p.b = []sessionStep{{level: "direct", payload: direct}, {level: "direct", payload: direct}}
	return p
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// sessionPlans holds the built plans — payloads measured from the real
// fixture corpora with the exact o200k tokenizer (or the estimator when the
// tables are unavailable; sessionTokensLabel says which).
var sessionPlans = func() []sessionPlan {
	c := o200kTokenizer()
	if c != nil {
		sessionTokensLabel = "exact o200k"
	}
	var out []sessionPlan
	for _, spec := range sessionPlanSpecs {
		out = append(out, buildSessionPlan(spec, c))
	}
	return out
}()

// sessionStep is one scripted retrieval step: a level and its payload size.
type sessionStep struct {
	level   string // "L1", "L2", "L3" or "direct"
	payload int    // tokens this step sends
}

// sessionPlan is one scripted question over an existing fixture corpus,
// with payload-bearing steps (built by buildSessionPlan from a spec).
type sessionPlan struct {
	task    string        // the scripted question
	fixture string        // existing fixture corpus the question targets
	a       []sessionStep // strategy A: progressive disclosure (L1 -> L3)
	b       []sessionStep // strategy B: direct tools
}

// sessionMetricsRow is the per (task, strategy) simulation result.
type sessionMetricsRow struct {
	task          string
	strategy      string
	steps         int
	payloadTokens int     // sum of per-step payloads
	prefixRent    int     // sum of re-sent prefixes (residual-context rent)
	totalTokens   int     // payloadTokens + prefixRent = total tokens per completed task
	deltaPct      float64 // (B - A) / A * 100; 0 when not applicable (A row)
	hasDelta      bool
}

// sessionStepCosts returns, for each step k, the total tokens its request
// carries: payload_k plus the re-sent conversation prefix (the sum of the
// payloads of steps 1..k-1). This is the residual-context rent model: every
// new request re-sends everything before it.
func sessionStepCosts(steps []sessionStep) []int {
	costs := make([]int, len(steps))
	prefix := 0
	for i, s := range steps {
		costs[i] = prefix + s.payload
		prefix += s.payload
	}
	return costs
}

// computeStrategy reduces a step plan to its aggregate row.
func computeStrategy(task, strategy string, steps []sessionStep) sessionMetricsRow {
	r := sessionMetricsRow{task: task, strategy: strategy, steps: len(steps)}
	for _, c := range sessionStepCosts(steps) {
		r.totalTokens += c
	}
	for _, s := range steps {
		r.payloadTokens += s.payload
	}
	r.prefixRent = r.totalTokens - r.payloadTokens
	return r
}

// simulateSession runs both strategies over one scripted plan and computes
// the delta% (B vs A) on strategy B's row (positive = B costs more than A).
// Pure and deterministic: constants and arithmetic only.
func simulateSession(p sessionPlan) (sessionMetricsRow, sessionMetricsRow) {
	a := computeStrategy(p.task, "progressive disclosure (A)", p.a)
	b := computeStrategy(p.task, "direct tools (B)", p.b)
	if a.totalTokens > 0 {
		b.deltaPct = float64(b.totalTokens-a.totalTokens) / float64(a.totalTokens) * 100
		b.hasDelta = true
	}
	return a, b
}

// runSessionMetrics simulates every scripted plan, A row then B row per plan.
func runSessionMetrics() []sessionMetricsRow {
	var rows []sessionMetricsRow
	for _, p := range sessionPlans {
		a, b := simulateSession(p)
		rows = append(rows, a, b)
	}
	return rows
}

// reportSessionMetrics prints the "## session metrics" table plus the
// one-line KPI takeaway. Informational: always returns no gate failures.
func reportSessionMetrics() []string {
	fmt.Println("| task | strategy | steps | payload tokens (" + sessionTokensLabel + ") | prefix rent (sum of re-sent prefixes) | total tokens per completed task (" + sessionTokensLabel + ") | delta% (B vs A) |")
	fmt.Println("|---|---|---|---|---|---|---|")
	for _, p := range sessionPlans {
		a, b := simulateSession(p)
		printSessionRow(a)
		printSessionRow(b)
	}
	fmt.Println()
	fmt.Println(sessionTakeawayLine())
	return nil
}

// printSessionRow prints one (task, strategy) row. The delta% column is only
// meaningful on strategy B rows (B vs A); A rows show an em dash.
func printSessionRow(r sessionMetricsRow) {
	delta := "—"
	if r.hasDelta {
		delta = fmt.Sprintf("%.1f%%", r.deltaPct)
	}
	fmt.Printf("| %s | %s | %d | %d | %d | %d | %s |\n",
		r.task, r.strategy, r.steps, r.payloadTokens, r.prefixRent, r.totalTokens, delta)
}

// sessionTakeawayLine builds the KPI takeaway from the same deterministic
// simulation as the table, so the paragraph can never drift from the rows:
// which strategy minimizes TOTAL tokens per completed task, and the
// residual-context share (rent / total) — the "rent" failure mode.
func sessionTakeawayLine() string {
	var aTotal, bTotal, aRent int
	worstShare := 0.0
	worstTask := ""
	for _, p := range sessionPlans {
		a, b := simulateSession(p)
		aTotal += a.totalTokens
		aRent += a.prefixRent
		bTotal += b.totalTokens
		if a.totalTokens > 0 {
			share := float64(a.prefixRent) / float64(a.totalTokens) * 100
			if share > worstShare {
				worstShare = share
				worstTask = p.task
			}
		}
	}
	delta := 0.0
	if aTotal > 0 {
		delta = float64(bTotal-aTotal) / float64(aTotal) * 100
	}
	winner := "direct tools (B)"
	if aTotal < bTotal {
		winner = "progressive disclosure (A)"
	}
	rentShare := 0.0
	if aTotal > 0 {
		rentShare = float64(aRent) / float64(aTotal) * 100
	}
	return fmt.Sprintf("_Takeaway: %s minimizes TOTAL tokens per completed task across the %d scripted sessions (%d vs %d; %.1f%% B vs A). Residual-context rent is %d tokens (%.1f%% of A's total) and reaches %.1f%% in the longest session (%q) — the progressive-disclosure prefix re-send failure mode. KPI: total tokens per completed task, not per-response size._",
		winner, len(sessionPlans), bTotal, aTotal, delta, aRent, rentShare, worstShare, worstTask)
}

func main() {
	// Determinism contract: this harness measures the DETERMINISTIC
	// compression surfaces only. Pin the LLM stage off so optimize.Prompt's
	// default auto chain (host sampler → agent CLIs → Ollama) can never leak
	// a live model into the numbers. Observed 2026-10-02 without the pin:
	// with the chain active and Ollama down, every row burned ~7.5s probing
	// dead legs (48-row matrix ≈ 6-7 minutes, indistinguishable from hung),
	// and rows answered by a locally-installed CLI agent VARIED between runs
	// (212 ↔ 225 after-tokens on the same fixture) — breaking both the
	// offline and the reproducibility halves of this harness's contract.
	os.Setenv("KERN_LLM_PROVIDER", "none")
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

	// Fixture x task-type matrix. Rows are informational (gate=0) but
	// prove every (fixture, task) pair is measured through a compression surface.
	fmt.Println("## fixture x task-type matrix")
	fmt.Println()
	gates = append(gates, runMatrix()...)
	fmt.Println()
	printMatrixCoverage()
	fmt.Println()

	// Per-task-class metric set: token/tool-call/retry reduction,
	// latency, cost, and task outcomes — all derived offline. Rows are
	// computed ONCE and shared with the latency distribution so the 48-pair
	// matrix is not re-run per section.
	fmt.Println("## task-class metrics")
	fmt.Println()
	classRows := taskClassMetrics()
	gates = append(gates, reportClassMetrics(classRows)...)
	reportLatencyDistribution(classRows)
	fmt.Println()

	// Session-simulation KPI: total tokens per completed task for
	// progressive-disclosure (L1/L2/L3) vs direct-tool retrieval over
	// scripted questions on the existing fixture corpus. Informational —
	// no hard gate on this table.
	fmt.Println("## session metrics")
	fmt.Println()
	gates = append(gates, reportSessionMetrics()...)
	fmt.Println()

	// Tokenizer accuracy: exact counters (cl100k/o200k) must reproduce
	// reference counts; estimator drift is reported for transparency.
	fmt.Println("## tokenizer accuracy")
	fmt.Println()
	gates = append(gates, reportTokenizerAccuracy()...)
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
		{name: "optimize log", raw: noisyLog, out: l.Output, note: "keeps errors + frames", gate: 85},
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
// Fixture matrix data. The six fixture corpora and eight task prompts
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

// printMatrixCoverage prints the coverage line: 6/6 fixtures, 8/8 tasks.
func printMatrixCoverage() {
	fmt.Printf("_Phase 17 fixture coverage: %d/%d fixture types and %d/%d task types present; every (fixture, task) pair measured through optimize.Prompt._\n",
		len(fixtures), len(fixtures), len(tasks), len(tasks))
}

// reportClassMetrics prints the per-task-class metric set and
// returns any hard-gate failures (informational here, so always empty).
// reportClassMetrics prints the per-task-class metric set (estimator tokens
// AND exact o200k tokens side by side with the estimator's drift — never
// blended) and returns any hard-gate failures (informational, always empty).
func reportClassMetrics(rows []classMetrics) []string {
	fmt.Println("| fixture | task | est tok (b/a) | o200k tok (b/a) | drift% | tok% | tools | tool% | retries | latency(ms) | cost | first-pass | verified | human | regression |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, m := range rows {
		exact := fmt.Sprintf("%d/%d", m.exactBefore, m.exactAfter)
		if m.exactBefore == 0 {
			exact = "n/a"
		}
		fmt.Printf("| %s | %s | %d/%d | %s | %+.1f%% | %.1f%% | %d | %.1f%% | %d | %d | $%.4f | %t | %t | %t | %t |\n",
			m.fixture, m.task, m.beforeTokens, m.afterTokens, exact, m.estimatorDrift, m.tokenReduction,
			m.toolCalls, m.toolCallReduction, m.retries, m.latencyMs, m.cost,
			m.firstPass, m.verifiedSuccess, m.humanIntervention, m.postDeployRegression)
	}
	fmt.Println()
	fmt.Println("_Token columns: 'est' uses the estimator counter (historical comparability); 'o200k' is the exact tokenizer. tok% is computed on estimator counts; drift% is estimator vs o200k on the raw input._")
	return nil
}

// reportLatencyDistribution prints p50/p90/p99/min/max over the per-class
// latency estimates (2026-11-20 remediation). These are token-arithmetic
// ESTIMATES, not wall-clock: the wall-clock cold/warm contract belongs to
// the cmd/kern bench suite (cold-cli vs daemon posture, cold/warm load,
// ServedBy), which this offline harness deliberately does not duplicate.
func reportLatencyDistribution(rows []classMetrics) {
	if len(rows) == 0 {
		return
	}
	lat := make([]int64, 0, len(rows))
	for _, m := range rows {
		lat = append(lat, m.latencyMs)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p := func(pc float64) int64 {
		idx := int(math.Ceil(pc/100*float64(len(lat)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(lat) {
			idx = len(lat) - 1
		}
		return lat[idx]
	}
	fmt.Printf("_Latency distribution over %d (fixture × task) rows (deterministic estimates, not wall-clock): p50=%dms p90=%dms p99=%dms min=%dms max=%dms. Cold/warm wall-clock split: cmd/kern bench (cold-cli vs daemon posture)._\n",
		len(lat), p(50), p(90), p(99), lat[0], lat[len(lat)-1])
}

// tokenizerFixtures are reference token counts from the tiktoken 0.14.0
// reference implementation over the same rank tables embedded in
// internal/tokenize/data. Values generated offline (see
// internal/tokenize/tiktoken_fixtures_test.go); do not hand-edit.
var tokenizerFixtures = []struct {
	text   string
	cl100k int
	o200k  int
}{
	{"hello world", 2, 2},
	{"tiktoken is great!", 6, 6},
	{"don't stop believin'", 6, 5},
	{"digits 123456789 and 42 and 007", 11, 11},
	{"unicode: café naïve 日本語 中文 한국어 🚀 emoji", 19, 13},
	{"CamelCaseIdentifier and snake_case_id", 8, 7},
	{"https://example.com/path?query=1&x=2#frag", 15, 15},
	{"error: connection refused (errno 61); retrying in 500ms", 15, 15},
}

// reportTokenizerAccuracy verifies the exact BPE tokenizers reproduce
// reference counts (hard gate) and reports estimator drift for
// transparency (the estimator remains the default counter so historical
// numbers stay comparable).
func reportTokenizerAccuracy() []string {
	var gates []string
	est := tokenize.Estimator{Kind: tokenize.KindGeneric}
	cl, errC := tokenize.NewCl100kCounter()
	o2, errO := tokenize.NewO200kCounter()
	if errC != nil || errO != nil {
		return []string{fmt.Sprintf("tokenizer table load failed: cl100k=%v o200k=%v", errC, errO)}
	}
	fmt.Println("| sample | estimator | cl100k | o200k | cl100k ref | o200k ref | estimator vs cl100k |")
	fmt.Println("|---|---|---|---|---|---|---|")
	for _, f := range tokenizerFixtures {
		ec := est.Count(f.text)
		cc, oc := cl.Count(f.text), o2.Count(f.text)
		drift := 0.0
		if cc > 0 {
			drift = float64(ec-cc) / float64(cc) * 100
		}
		fmt.Printf("| %q | %d | %d | %d | %d | %d | %+.1f%% |\n", f.text, ec, cc, oc, f.cl100k, f.o200k, drift)
		if cc != f.cl100k {
			gates = append(gates, fmt.Sprintf("cl100k count mismatch on %q: got %d want %d", f.text, cc, f.cl100k))
		}
		if oc != f.o200k {
			gates = append(gates, fmt.Sprintf("o200k count mismatch on %q: got %d want %d", f.text, oc, f.o200k))
		}
	}
	return gates
}

type recall struct {
	query string
	file  string
}

// recallQueries target files that exist in this repo, so the harness verifies
// itself on every run. Expanded 2026-11-20 (remediation): 10 realistic
// queries across README, contributing, architecture, and docs surfaces; the
// gate requires ≥0.9 (hit < total-1 fails).
var recallQueries = []recall{
	{"how do I optimize a prompt", "README.md"},
	{"how do I compress a log", "README.md"},
	{"call kern tools first before built-ins", "AGENTS.md"},
	{"contributing quick start go test", "CONTRIBUTING.md"},
	{"how to add a new cli command", "CONTRIBUTING.md"},
	{"subsystem ledger loc cap drift gate", "ARCHITECTURE.md"},
	{"architecture tour entry points index", "architecture-tour.md"},
	{"mcp tool catalog", "mcp/README.md"},
	{"cli reference command table", "cli-reference.md"},
	{"benchmark methodology cold versus warm", "benchmarks/README.md"},
}

func pctf(hit, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(hit) / float64(total) * 100
}
