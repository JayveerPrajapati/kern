# kern-sdk (Python)

Thin, stdlib-only HTTP client for the kern-server REST API. No third-party
dependencies (`urllib` + `json` only).

## Install

```bash
cd sdk/python
pip install -e .
```

## Usage

```python
from kern_sdk import Client

client = Client()  # defaults to http://localhost:8090, 10s timeout
# or: Client(base_url="http://localhost:9000", timeout=20)

result = client.analyze("update the auth middleware to use JWT")
print(result["text"])

plan = client.plan("refactor the payment gateway")
packet = client.what_if("add rate limiting", "impact")
mem = client.memory_add("JWT flow validated", mem_type="lesson", tags=["auth"])

task = client.task_submit("bump the cache TTL", task_type="code")
loop = client.loop("deploy the canary", level="L2")

# Errors surface as KernError with a status code:
from kern_sdk import KernError
try:
    client.task("missing-id")
except KernError as e:
    print(e.status, e)
```

## Local Engine & RAG Framework Adapters

Use `KernEngine` for fast, offline context compression without running background servers:

```python
from kern_sdk.engine import KernEngine
from kern_sdk.integrations.langchain import KernLangChainCompressor
from kern_sdk.integrations.llamaindex import KernLlamaIndexCompressor

# 1. Direct local compression with adaptive context windowing (-A/-B)
engine = KernEngine()
compressed = engine.compress(raw_log_or_code, context_before=2, context_after=3)

# 2. LangChain document compression
compressor = KernLangChainCompressor(context_window=2)
# drops 60-80% of filler tokens from retrieved documents

# 3. LlamaIndex node postprocessor
node_compressor = KernLlamaIndexCompressor(context_window=2)
```

## Running tests

```bash
cd sdk/python
python3 -m pytest test_client.py test_engine.py -v
```

Tests mock `urlopen` and execution — they never make real HTTP calls.