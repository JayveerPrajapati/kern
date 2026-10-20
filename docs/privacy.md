# Privacy

kern is a local, deterministic code-intelligence engine for AI agents. It runs entirely on your machine — no telemetry, no phone-home, and no network calls in its own operation (the only network use is fetching URLs you explicitly name; see §4).

## 1. Privacy Model

- **100% local** — kern makes no network calls of its own; the only network use is the explicit opt-in fetches listed in §4.
- **No telemetry** — kern does not phone home, report usage, or send crash data.
- **No third-party analytics** — no tracking pixels, no analytics SDKs, no data brokers.
- All data stays on the user's machine. What you index is what stays.

## 2. Data Collection

**What kern collects (all local, derived from your code):**

- Symbol index — the parsed structure of your codebase (symbols, types, functions)
- Call graphs — relationships between symbols and their callers/callees
- Audit logs — a tamper-evident record of governance and approval actions
- Memory lessons — curated engineering knowledge stored in the project brain

**What kern does NOT collect:**

- Source code — only the parsed symbol index is kept, never raw file contents as data
- User files — files outside the indexed scope are not read
- API keys — never logged or transmitted
- PII — personal data is not gathered or stored

**PII masking:**

Before any remote processing (e.g., an LLM rewrite), `kern_mask_pii` (internal/pii/pii.go) masks secrets, tokens, and personal data so nothing sensitive leaves the machine unmasked.

## 3. Data Storage

All data is stored locally on your machine:

| Path | Contents |
|------|----------|
| `~/.cache/kern/` | Symbol index and caches |
| `.kern/` | Audit logs and approval records |

- **No cloud storage** — nothing is uploaded to any remote service.
- **No external APIs by default** — kern works fully offline out of the box.

## 4. Opt-in Features

These features are disabled by default and only activate when you explicitly enable them:

- **LLM rewriting** — uses local Ollama by default; a remote provider is used only if `KERN_LLM_PROVIDER` is set
- **kern docs fetch** — fetches public documentation pages and stores them locally in your doc index
- **Semantic search** — uses local Ollama embeddings; embeddings never leave your machine

## 5. Third-Party Integrations

kern has no hard dependency on external services. The only optional integration:

- **GitHub token (`KERN_GITHUB_TOKEN`)** — used solely for PR creation when you ask kern to open one. Never used for anything else, never logged.

No other external services are contacted.

## 6. Data Retention

- **Index** — persists until manually cleared (`kern` cache invalidation)
- **Audit logs** — append-only and tamper-evident (each record's hash covers its predecessor, so kern never truncates or rotates the chain); they grow until you archive them. Move `.kern/audit/chain.jsonl` aside to start a fresh chain and keep the archive for audit history — `kern doctor` warns once the chain passes 50 MB so growth never goes unnoticed.
- **Memory lessons** — persistent until explicitly archived or deleted

You are in full control of how long data is kept — kern never deletes or expires your data on its own.