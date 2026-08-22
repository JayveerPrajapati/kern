// Tests for the TypeScript kern SDK client.
// Uses Node's built-in test runner + a mocked global fetch — no real HTTP calls.
// Imports the compiled output from dist/, so run `npm run build` first.

import { test } from "node:test";
import assert from "node:assert/strict";
import { Client, KernError } from "../dist/index.js";

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