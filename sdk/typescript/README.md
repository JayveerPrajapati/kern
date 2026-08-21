# kern-sdk (TypeScript)

Thin HTTP client for the kern-server REST API. Uses the built-in `fetch`
(available in Node 18+ and browsers) — no axios dependency.

## Install & build

```bash
cd sdk/typescript
npm install
npm run build   # emits dist/
```

## Usage

```ts
import { Client, KernError } from "@kern/sdk";

const client = new Client(); // defaults to http://localhost:8090, 10s timeout
// or: new Client("http://localhost:9000", 20000)

const result = await client.analyze("update the auth middleware to use JWT");
console.log(result.text);

const plan = await client.plan("refactor the payment gateway");
const mem = await client.memoryAdd("JWT flow validated", "lesson", "auth", ["auth"]);
const task = await client.taskSubmit("bump the cache TTL", "code");
const loop = await client.loop("deploy the canary", "L2");

// Errors surface as KernError with a status code:
try {
  await client.task("missing-id");
} catch (e) {
  if (e instanceof KernError) console.log(e.status, e.message);
}
```

## Running tests

Tests mock the global `fetch` — they never make real HTTP calls. The test file
is plain ESM (`test/client.test.mjs`) and imports the compiled output, so it
runs only after `npm run build`:

```bash
npm run build
npm test   # node --test test/client.test.mjs
```