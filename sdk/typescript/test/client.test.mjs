// Tests for the TypeScript kern SDK client.
// Uses Node's built-in test runner + a mocked global fetch — no real HTTP calls.
// Imports the compiled output from dist/, so run `npm run build` first.

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { existsSync, openSync, closeSync, readFileSync } from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Client, KernError } from "../dist/index.js";

// The REAL fetch is captured at module load: the mocked tests below replace
// global.fetch and never restore it, so the live cross-check must use the
// original (see the live test at the bottom of this file).
const realFetch = globalThis.fetch.bind(globalThis);

// loadContractFixture reads the SHARED contract fixture every SDK suite
// asserts against (sdk/contract/tools_call.json — the POST /v1/tools/{name}
// route shape). A route-shape change must fail the Go, Python and TypeScript
// suites until the clients are updated.
function loadContractFixture() {
  const url = new URL("../../contract/tools_call.json", import.meta.url);
  const fx = JSON.parse(readFileSync(url, "utf8"));
  assert.equal(fx._version, 1, "contract fixture version must be 1");
  assert.ok(fx.cases.length > 0, "contract fixture must have cases");
  return fx.cases;
}

// Helper: capture the fetch call arguments.
function mockFetch(responseBody, status = 200, statusText = "OK") {
  const calls = [];
  global.fetch = async (url, init) => {
    calls.push({ url, init });
    return {
      ok: status >= 200 && status < 300,
      status,
      statusText,
      text: async () => JSON.stringify(responseBody),
    };
  };
  return calls;
}

test("analyze posts change to /v1/analyze", async () => {
  const calls = mockFetch({ packet: {}, text: "ok" });
  const c = new Client("http://test:8090/");
  const out = await c.analyze("change x");
  assert.equal(out.text, "ok");
  assert.equal(calls[0].url, "http://test:8090/v1/analyze");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(JSON.parse(calls[0].init.body).change, "change x");
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
});

test("plan posts change to /v1/plan", async () => {
  const calls = mockFetch({ packet: {}, text: "plan" });
  const c = new Client("http://test:8090");
  await c.plan("change y");
  assert.equal(calls[0].url, "http://test:8090/v1/plan");
  assert.equal(JSON.parse(calls[0].init.body).change, "change y");
});

test("whatIf builds body with optional new_target", async () => {
  const c = new Client("http://test:8090");
  let calls = mockFetch({});
  await c.whatIf("change", "log");
  assert.deepEqual(JSON.parse(calls[0].init.body), { change: "change", kind: "log" });

  calls = mockFetch({});
  await c.whatIf("change", "log", "http://new");
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    change: "change",
    kind: "log",
    new_target: "http://new",
  });
});

test("memoryAdd posts content/type/scope/tags to /v1/memory", async () => {
  const calls = mockFetch({});
  const c = new Client("http://test:8090");
  await c.memoryAdd("content", "note", "svc", ["a", "b"]);
  assert.equal(calls[0].url, "http://test:8090/v1/memory");
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    content: "content",
    type: "note",
    scope: "svc",
    tags: ["a", "b"],
  });
});

test("memoryAdd uses defaults", async () => {
  const calls = mockFetch({});
  const c = new Client("http://test:8090");
  await c.memoryAdd("content");
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    content: "content",
    type: "lesson",
    scope: "",
    tags: [],
  });
});

test("health GETs /api/health", async () => {
  const calls = mockFetch({ status: "ok" });
  const c = new Client("http://test:8090");
  const out = await c.health();
  assert.deepEqual(out, { status: "ok" });
  assert.equal(calls[0].url, "http://test:8090/api/health");
  assert.equal(calls[0].init.method, "GET");
});

test("task GETs encoded id from /v1/tasks/{id}", async () => {
  const calls = mockFetch({});
  const c = new Client("http://test:8090");
  await c.task("abc def");
  assert.equal(calls[0].url, "http://test:8090/v1/tasks/abc%20def");
  assert.equal(calls[0].init.method, "GET");
});

test("loop posts intent and level to /v1/loop", async () => {
  const c = new Client("http://test:8090");
  let calls = mockFetch({});
  await c.loop("intent", "L3");
  assert.deepEqual(JSON.parse(calls[0].init.body), { intent: "intent", level: "L3" });

  calls = mockFetch({});
  await c.loop("intent");
  assert.deepEqual(JSON.parse(calls[0].init.body), { intent: "intent", level: "L0" });
});

test("execute POSTs patch to /v1/execute", async () => {
  const calls = mockFetch({});
  const c = new Client("http://test:8090");
  await c.execute("patch text");
  assert.equal(calls[0].url, "http://test:8090/v1/execute");
  assert.deepEqual(JSON.parse(calls[0].init.body), { patch: "patch text" });
});

test("audit GETs /v1/audit/{id} when a task id is given", async () => {
  const c = new Client("http://test:8090");
  const calls = mockFetch({});
  await c.audit("task-123");
  assert.equal(calls[0].url, "http://test:8090/v1/audit/task-123");
  assert.equal(calls[0].init.method, "GET");
});

test("audit throws when no task id is given", async () => {
  const c = new Client("http://test:8090");
  assert.throws(() => c.audit(""), /requires a taskId/);
  assert.throws(() => c.audit(undefined), /requires a taskId/);
});

test("non-2xx raises KernError with status", async () => {
  global.fetch = async () => ({
    ok: false,
    status: 404,
    statusText: "Not Found",
    text: async () => '{"error": "no such task"}',
  });
  const c = new Client("http://test:8090");
  await assert.rejects(
    () => c.task("missing"),
    (e) => e instanceof KernError && e.status === 404
  );
});

test("connection failure raises KernError", async () => {
  global.fetch = async () => {
    throw new Error("fetch failed");
  };
  const c = new Client("http://test:8090");
  await assert.rejects(
    () => c.memoryList(),
    (e) => e instanceof KernError && /connection error/.test(e.message)
  );
});
test("approvalsPending gets /v1/approvals/pending", async () => {
  const calls = mockFetch({ items: [] });
  const c = new Client("http://test:8090");
  const out = await c.approvalsPending();
  assert.deepEqual(out, { items: [] });
  assert.equal(calls[0].url, "http://test:8090/v1/approvals/pending");
  assert.equal(calls[0].init.method, "GET");
});

test("incidents gets /v1/incidents", async () => {
  const calls = mockFetch({ items: [] });
  const c = new Client("http://test:8090");
  const out = await c.incidents();
  assert.deepEqual(out, { items: [] });
  assert.equal(calls[0].url, "http://test:8090/v1/incidents");
});

test("callTool posts args to /v1/tools/{name} and returns the output payload", async () => {
  const calls = mockFetch({ output: "masked 1 secrets" });
  const c = new Client("http://test:8090");
  const out = await c.callTool("kern_mask_pii", { text: "token=sk-abc123" });
  assert.equal(out, "masked 1 secrets");
  assert.equal(calls[0].url, "http://test:8090/v1/tools/kern_mask_pii");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body), { text: "token=sk-abc123" });
  assert.equal(calls[0].init.headers["Content-Type"], "application/json");
});

test("callTool defaults args to an empty object", async () => {
  const calls = mockFetch({ output: "" });
  const c = new Client("http://test:8090");
  const out = await c.callTool("kern_no_such_tool");
  assert.equal(out, "");
  assert.equal(calls[0].url, "http://test:8090/v1/tools/kern_no_such_tool");
  assert.deepEqual(JSON.parse(calls[0].init.body), {});
});

test("callTool obeys the shared contract fixture (sdk/contract/tools_call.json)", async (t) => {
  for (const tc of loadContractFixture()) {
    await t.test(tc.id, async () => {
      const status = tc.response.status;
      const body = tc.response.body;
      const url = `http://test:8090/v1/tools/${encodeURIComponent(tc.name)}`;
      if (status === 200) {
        const calls = mockFetch(body);
        const c = new Client("http://test:8090");
        const out = await c.callTool(tc.name, tc.args);
        assert.equal(out, tc.expect_output);
        assert.equal(calls[0].url, url);
        assert.equal(calls[0].init.method, "POST");
        assert.deepEqual(JSON.parse(calls[0].init.body), tc.args);
        assert.equal(calls[0].init.headers["Content-Type"], "application/json");
      } else {
        global.fetch = async () => ({
          ok: false,
          status,
          statusText: "err",
          text: async () => JSON.stringify(body),
        });
        const c = new Client("http://test:8090");
        await assert.rejects(
          () => c.callTool(tc.name, tc.args),
          (e) => e instanceof KernError && e.status === status,
        );
      }
    });
  }
});

test("incident gets /v1/incidents/{id} and requires an id", async () => {
  const calls = mockFetch({ ID: "inc-1" });
  const c = new Client("http://test:8090");
  const out = await c.incident("inc-1");
  assert.equal(out.ID, "inc-1");
  assert.equal(calls[0].url, "http://test:8090/v1/incidents/inc-1");
  assert.throws(() => c.incident(""), (e) => e instanceof KernError && /incidentId/.test(e.message));
});

test("eventsStream yields parsed SSE data payloads", async () => {
  const frames = 'event: ping\ndata: {"kind":"x","n":1}\n\ndata: plain-text\n\n';
  const stream = new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(frames));
      controller.close();
    },
  });
  global.fetch = async () => ({
    ok: true,
    status: 200,
    statusText: "OK",
    body: stream,
    text: async () => "",
  });
  const c = new Client("http://test:8090");
  const out = [];
  for await (const p of c.eventsStream()) out.push(p);
  assert.deepEqual(out, [{ kind: "x", n: 1 }, "plain-text"]);
});

test("eventsStream yields SSE data payloads (F-032 parity)", async () => {
  const stream = new ReadableStream({
    start(controller) {
      controller.enqueue(
        new TextEncoder().encode('data: {"id":1}\n\ndata: hello\n'),
      );
      controller.close();
    },
  });
  const calls = [];
  global.fetch = async (url, init) => {
    calls.push({ url, init });
    return { ok: true, status: 200, statusText: "OK", body: stream };
  };
  const c = new Client("http://test:8090", 10000);
  const out = [];
  for await (const p of c.eventsStream()) {
    out.push(p);
  }
  assert.equal(calls[0].url, "http://test:8090/v1/events/stream");
  assert.equal(calls[0].init.method, "GET");
  assert.equal(calls[0].init.headers.Accept, "text/event-stream");
  // JSON payloads decode; non-JSON payloads pass through as raw strings.
  assert.deepEqual(out, [{ id: 1 }, "hello"]);
});

test("eventsStream rejects non-2xx with KernError", async () => {
  global.fetch = async () => ({
    ok: false,
    status: 500,
    statusText: "boom",
    text: async () => "{}",
  });
  const c = new Client("http://test:8090", 10000);
  await assert.rejects(
    async () => {
      for await (const _ of c.eventsStream()) {
        /* consume */
      }
    },
    (e) => e instanceof KernError && e.status === 500,
  );
});

// --- OPTIONAL live-route cross-check (R4) ---
//
// The mocked tests above pin the shared fixture (sdk/contract/tools_call.json)
// against a fake transport; live-route drift detection rests on the Go suite's
// in-process route test. This test closes the gap: when a kern server can be
// started from the repo (a `go` toolchain + the repo root relative to this
// test file, falling back to a prebuilt `kern` binary on PATH), it starts
// `go run ./cmd/kern serve` on an ephemeral port with KERN_AUTH_TOKEN set and
// runs the fixture's 4 cases against the REAL POST /v1/tools/{name} route. It
// SKIPS gracefully (with a one-line reason) in CI without Go or in a
// standalone SDK checkout.

function kernRepoRoot() {
  // The test file lives in sdk/typescript/test/: the repo root is THREE
  // levels up (the contract fixture at sdk/contract is two, so the fixture
  // loader above uses "../..").
  return fileURLToPath(new URL("../../../", import.meta.url));
}

function findFreePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const port = srv.address().port;
      srv.close(() => resolve(port));
    });
  });
}

function randomToken() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function waitForKernServer(base, token, proc, logPath, timeoutMs, opts = {}) {
  const { usedPrebuilt = false, onSkip = () => {}, onStop = () => {} } = opts;
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    const poll = async () => {
      if (proc.exitCode !== null) {
        const tail = readFileSync(logPath, "utf8").split("\n").slice(-8).join("\n");
        reject(new Error(`kern serve exited early (code ${proc.exitCode}):\n${tail}`));
        return;
      }
      if (Date.now() > deadline) {
        reject(new Error(`kern serve did not become ready in ${timeoutMs}ms (slow first build? see ${logPath})`));
        return;
      }
      try {
        const resp = await realFetch(base + "/api/health", {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (resp.ok) {
          if (usedPrebuilt) {
            // Prebuilt-binary fallback only: an installed `kern` may predate
            // the /v1/tools/{name} passthrough route (the route shipped with
            // the SDK-passthrough campaign). Probe it once; if the route is
            // absent, skip gracefully instead of failing the optional check
            // on an outdated binary.
            const probe = await realFetch(base + "/v1/tools/kern_no_such_tool", {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                Authorization: `Bearer ${token}`,
              },
              body: "{}",
            });
            const probeText = await probe.text();
            if (!probeText.includes('"error": "unknown tool')) {
              onStop();
              onSkip(
                "live cross-check skipped: prebuilt kern binary does not expose POST /v1/tools/{name} (too old?) — install a newer kern or use go run"
              );
              resolve(false); // skipped: test body returns early
              return;
            }
          }
          resolve(true);
          return;
        }
      } catch {
        /* not up yet — keep polling */
      }
      setTimeout(poll, 1000);
    };
    poll();
  });
}

test("live cross-check: fixture cases against a real kern serve process (skips without Go)", async (t) => {
  const repoRoot = kernRepoRoot();
  const goAvailable = spawnSync("go", ["version"], { stdio: "ignore" }).status === 0;
  const hasRepo = existsSync(path.join(repoRoot, "go.mod"));
  const kernOnPath =
    spawnSync("sh", ["-c", "command -v kern"], { stdio: "ignore" }).status === 0;
  let cmd;
  let usedPrebuilt = false;
  if (goAvailable && hasRepo) {
    cmd = ["go", "run", "./cmd/kern", "serve"]; // prefer repo go run (freshness)
  } else if (kernOnPath) {
    cmd = ["kern", "serve"];
    usedPrebuilt = true;
  } else {
    t.skip(
      "live cross-check skipped: no Go toolchain and no kern binary on PATH (CI without Go / standalone SDK checkout)"
    );
    return;
  }

  const port = await findFreePort();
  const token = randomToken();
  const logPath = path.join(os.tmpdir(), `kern-live-${process.pid}-${Date.now()}.log`);
  const logFd = openSync(logPath, "w");
  const proc = spawn(cmd[0], [...cmd.slice(1), "--addr", `127.0.0.1:${port}`], {
    cwd: repoRoot,
    env: { ...process.env, KERN_AUTH_TOKEN: token },
    stdio: ["ignore", logFd, logFd],
    detached: true, // own process group: the go-run server child dies with it
  });
  const base = `http://127.0.0.1:${port}`;
  try {
    const ready = await waitForKernServer(base, token, proc, logPath, 240000, {
      usedPrebuilt,
      onSkip: (reason) => t.skip(reason),
      onStop: () => {
        try {
          process.kill(-proc.pid, "SIGTERM");
        } catch {
          /* already gone */
        }
      },
    });
    if (!ready) return;

    // The mocked tests above replaced global.fetch and never restored it,
    // so put the REAL fetch back before touching the live server.
    global.fetch = realFetch;
    const authedFetch = async (url, init) => {
      init = {
        ...init,
        headers: { ...(init.headers || {}), Authorization: `Bearer ${token}` },
      };
      return realFetch(url, init);
    };
    try {
      // The server was started with KERN_AUTH_TOKEN set: a bare client (no
      // Authorization header) must be refused with 401 — proves the gate is on.
      const bare = new Client(base);
      await assert.rejects(
        () => bare.callTool("kern_mask_pii", { text: "x" }),
        (e) => e instanceof KernError && e.status === 401
      );
      // From here the client carries the bearer token on every request, then
      // runs the shared fixture's 4 cases against the REAL route: 2xx returns
      // the {"output": ...} payload, 403/404/500 surface as KernError with the
      // mapped status and the documented error body.
      global.fetch = authedFetch;
      const c = new Client(base);
      for (const tc of loadContractFixture()) {
        await t.test(tc.id, async () => {
          try {
            const out = await c.callTool(tc.name, tc.args);
            assert.equal(typeof out, "string");
            assert.ok(out.length > 0, `empty output payload for ${tc.id}`);
            if (tc.id === "success") assert.ok(out.includes("masked 1 secrets"));
            if (tc.id === "tool_failure_500") assert.ok(out.includes("masked 0 secrets"));
          } catch (e) {
            assert.ok(e instanceof KernError, `expected KernError, got ${e}`);
            assert.ok(
              [403, 404, 500].includes(e.status),
              `unexpected status ${e.status} for ${tc.id}`
            );
            if (tc.id === "denied_403") {
              assert.equal(e.status, 403);
              assert.ok(/tool call denied/.test(e.message));
            }
            if (tc.id === "unknown_404") {
              assert.equal(e.status, 404);
              assert.ok(/unknown tool/.test(e.message));
            }
            if (tc.id === "tool_failure_500") {
              assert.equal(e.status, 500);
              assert.ok(/tool call failed/.test(e.message));
            }
          }
        });
      }
    } finally {
      global.fetch = realFetch;
    }
  } finally {
    // Kill the whole process group: `go run` execs the server as a child,
    // and a plain kill on the go process would orphan the server.
    try {
      process.kill(-proc.pid, "SIGTERM");
    } catch {
      proc.kill("SIGTERM");
    }
    await new Promise((res) => {
      const t = setTimeout(res, 15000); // never hang the suite on a stuck server
      proc.once("exit", () => {
        clearTimeout(t);
        res();
      });
    });
    closeSync(logFd);
  }
});
