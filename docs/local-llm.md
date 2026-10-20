# Local LLM Providers

How kern uses LLMs, what happens when none is available, and how to pin
one. Everything here is **local-first**: the default chain never leaves your
machine, remote providers are explicit opt-ins, and prompts sent to remote
endpoints are always PII-masked first (`kern_mask_pii` runs before any
remote call; local Ollama and local agent CLIs are exempt because nothing
leaves the host).

## The auto chain (default)

`KERN_LLM_PROVIDER` is unset by default, which selects `auto` — a chain
tried in order until one answers:

1. the **host sampler** — when kern is hosted inside an agent session
   (MCP), that session's own model serves the task (the "MCP ack" rule: no
   fresh session is spawned next to the live one);
2. the **local agent CLIs** — `claude`, `opencode`, `codex`, `gemini`,
   `qwen`, `agy`, `antigravity`, whichever are installed on `PATH`;
3. **Ollama** last (`OLLAMA_HOST`, default `http://localhost:11434`).

Construction never touches the network; a dead link fails fast at call
time and the chain moves on.

**Ollama is optional.** The chain prefers your agent CLIs, so a stopped
Ollama next to installed CLIs is the *normal healthy state* — `kern doctor`
reports it as `[ok] llm`. The only features that degrade without Ollama are
the embedding-backed ones: `kern doc` semantic search and semantic
re-ranking fall back to keyword/n-gram matching. Everything else
(compression, heal, planning, code generation) is served by the CLIs.

## What `kern doctor` reports (`[llm]`)

| State | Level |
|---|---|
| Ollama reachable | `ok` |
| Ollama down, agent CLIs installed | `ok` (embeddings degrade — named in the detail) |
| LLM stage disabled (`KERN_LLM_PROVIDER=none`) | `ok` — deterministic mode |
| Provider pinned, key env missing | `warn` (names the env var) |
| No provider at all | `warn` — deterministic tools unaffected |
| No provider **and** `KERN_LLM_REQUIRED=1` | `fail` |

## Pinning a provider

```bash
KERN_LLM_PROVIDER=ollama    # local Ollama only — no CLI fallback
KERN_LLM_PROVIDER=openai    # any OpenAI-compatible API (openrouter, groq,
                            # litellm, vllm, azure use the same wire)
KERN_LLM_PROVIDER=anthropic
KERN_LLM_PROVIDER=google    # Gemini; needs GEMINI_API_KEY
KERN_LLM_PROVIDER=claude    # a specific local agent CLI
KERN_LLM_PROVIDER=none     # off entirely — fully deterministic
```

Pinned remote providers take their credentials from their own environment
variables (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`) — never
from `.kern/config.json`. `kern doctor` reports a pinned provider's wiring
offline (key presence, base URL) and only contacts the endpoint when you
actually make a call.

## Requiring an LLM

Optional is the default: kern degrades gracefully. To make a missing
provider a hard failure instead — CI gating, enterprise deployments — set:

```bash
KERN_LLM_REQUIRED=1
```

(or `"llm": {"required": true}` in `.kern/config.json`). `kern doctor` then
reports `[fail] llm` when nothing can serve.

## Security posture

- `OLLAMA_HOST` must be local (or `127.0.0.1`) unless
  `KERN_ALLOW_REMOTE_OLLAMA=1` is set — prompts are masked before any
  remote hop, but the default assumes you did not mean to send them off-host.
- Remote OpenAI-compatible base URLs must be `https` and not
  private/loopback targets.
- Nothing in the LLM path is telemetry: prompts and completions stay on the
  host (or go only to the endpoint you pinned).

See [configuration.md](configuration.md) for the full configuration catalog.
