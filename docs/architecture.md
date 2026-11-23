# Kern Architecture & Subsystem Specification

`kern` is a local-first, deterministic code-intelligence engine built in Go. It
pre-indexes symbols and call graphs, answers surgical context questions through
CLI and MCP, and enforces governance around edits, verification, execution, and
audit trails.

---

## 1. High-level architecture

The system is organized into four layers:

```mermaid
graph TD
    subgraph Clients["1. Agent & IDE Interface Layer"]
        CLI["CLI (`cmd/kern`)"]
        MCPServer["MCP Server (`cmd/kern-mcp`)"]
        Agents["Agent Integrations (Claude Code, Codex, Cursor, OpenCode, and others)"]
    end

    subgraph Governance["2. Dispatch & Governance Layer"]
        MCPMux["MCP Dispatcher (`internal/mcp`)"]
        Catalog["Catalog Registry (`internal/mcpcatalog`) — 33 Tools"]
        Gate["Path & Tool Governance (`internal/mcpgate`, `internal/governance`)"]
        Audit["Tamper-Evident Audit Log (`internal/governance`)"]
    end

    subgraph Intelligence["3. Intelligence, AST & Agent Layer"]
        IndexEngine["Symbol Index & Graph (`internal/index`, `internal/intel`)"]
        TreeSitter["Tree-sitter AST Engines (`internal/index`; default build, CGO; opt out with -tags notreesitter)"]
        AgentPipeline["Task & Agent Lifecycle (`internal/agent`, `internal/tasklife`)"]
        Verification["Verification Engine (`internal/verifycmd`, `internal/verification`, `internal/sec`)"]
    end

    subgraph Storage["4. Storage & OS Substrate"]
        SQLite["SQLite WAL + FTS5 (`.kern/index.db`)"]
        GobSnap["Binary Gob Snapshots (`.kern/index.json.snap`)"]
        SemCache["Semantic Fingerprint Cache (`internal/semcache`)"]
        Transport["Unix Domain Socket / Stdio Transports (`internal/mcp/transport`)"]
    end

    Clients --> MCPMux
    MCPMux --> Catalog
    MCPMux --> Gate
    Gate --> Audit
    MCPMux --> IndexEngine
    IndexEngine --> TreeSitter
    IndexEngine --> SQLite
    IndexEngine --> GobSnap
    AgentPipeline --> IndexEngine
    AgentPipeline --> Verification
    Verification --> SemCache
    MCPMux --> Transport
```

`ARCHITECTURE.md` remains the authoritative, machine-checked subsystem ledger.
The `TestArchitectureDocParity` test enforces the LOC caps and allowed internal
dependency graph declared there.

---

## 2. Agent workflow

Kern’s task pipeline follows the standard **Explore → Plan → Edit → Verify**
lifecycle:

```mermaid
flowchart LR
    Explore["Explore<br/>(symbol graph, context, memory)"]
    Plan["Plan<br/>(impact, architecture, task plan)"]
    Edit["Edit<br/>(surgical patch, sandboxed compile, repair)"]
    Verify["Verify<br/>(build, tests, security, architecture, evidence)"]

    Explore --> Plan
    Plan --> Edit
    Edit --> Verify
    Verify --> Evidence["Signed Evidence<br/>& Audit Trail"]
```

The lifecycle is deterministic and local. Optional LLM providers are used only
for higher-level reasoning or healing when explicitly configured; the index,
call graph, blast-radius analysis, and verification engine remain deterministic.

---

## 3. Modular MCP subsystem

`internal/mcp/` pairs a capped dispatcher root with focused leaf packages.
The root is intentionally capped rather than hidden: crossing the LOC ceiling
forces the next family out to a top-level sibling such as `internal/mcpcatalog`,
`internal/mcpmeta`, `internal/mcpcli`, or `internal/mcpserve`.

```mermaid
graph TD
    Monolith["`internal/mcp` Dispatcher"]

    subgraph Context["Context & Navigation"]
        graph["`internal/mcp/graph`"]
        doc["`internal/mcp/doc`"]
        retrieve["`internal/mcp/retrieve`"]
        optimize["`internal/mcp/optimize`"]
        contextwatch["`internal/mcp/contextwatch`"]
        crossrepo["`internal/mcp/crossrepo`"]
    end

    subgraph Governance["Governance & Security"]
        gov["`internal/mcp/gov`"]
        provenance["`internal/mcp/provenance`"]
        policydsl["`internal/mcp/policydsl`"]
        fingerprint["`internal/mcp/fingerprint`"]
        preedit["`internal/mcp/preedit`"]
    end

    subgraph Mutation["Mutation & Repair"]
        refactor["`internal/mcp/refactor`"]
        repair["`internal/mcp/repair`"]
        fragility["`internal/mcp/fragility`"]
        compose["`internal/mcp/compose`"]
    end

    subgraph Runtime["Execution & Agent Control"]
        exec["`internal/mcp/exec`"]
        runtime["`internal/mcp/runtime`"]
        flight["`internal/mcp/flight`"]
        stream["`internal/mcp/stream`"]
        transport["`internal/mcp/transport`"]
        catalog["`internal/mcpcatalog`"]
    end

    Monolith --> Context
    Monolith --> Governance
    Monolith --> Mutation
    Monolith --> Runtime
```

---

## 4. Architectural invariants

1. **Parity testing** — `TestArchitectureDocParity` in
   `internal/architecture/archdoc_test.go` enforces the subsystem ledger.
2. **Fail-closed boundaries** — new internal packages must be declared in
   `ARCHITECTURE.md` with explicit LOC caps and allowed internal dependencies.
3. **No hidden growth** — subsystems that hit a cap must be extracted rather
   than silently enlarged.
4. **Local-first execution** — no implicit telemetry or network calls.
5. **Deterministic intelligence** — symbol indexing, call graphs, and impact
   analysis are deterministic; language precision is explicit.
