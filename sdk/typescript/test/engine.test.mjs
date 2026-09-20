// Tests for TypeScript SDK KernEngine.
import { test } from "node:test";
import assert from "node:assert/strict";
import { KernEngine } from "../dist/index.js";

test("KernEngine resolves binary path via constructor or env", () => {
  const engine1 = new KernEngine("/custom/path/to/kern");
  assert.equal(engine1.binaryPath, "/custom/path/to/kern");

  process.env.KERN_BINARY_PATH = "/env/path/to/kern";
  const engine2 = new KernEngine();
  assert.equal(engine2.binaryPath, "/env/path/to/kern");
  delete process.env.KERN_BINARY_PATH;
});

test("KernEngine handles missing binary gracefully", async () => {
  const engine = new KernEngine("non_existent_kern_binary_12345");
  await assert.rejects(
    async () => {
      await engine.optimizePrompt("hello");
    },
    /Failed to execute kern engine/
  );
});
