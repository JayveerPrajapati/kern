"""Thin HTTP client for the kern-server REST API."""

import json
from typing import Any, Optional
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

DEFAULT_BASE_URL = "http://localhost:8090"


class KernError(Exception):
    """Raised when the kern-server returns an error."""
    def __init__(self, message: str, status: int = 0):
        super().__init__(message)
        self.status = status


class Client:
    """Typed client for the kern-server REST API.

    Args:
        base_url: Server URL (default http://localhost:8090).
        timeout: Request timeout in seconds (default 10).
    """

    def __init__(self, base_url: str = DEFAULT_BASE_URL, timeout: int = 10):
        self.base = base_url.rstrip("/")
        self.timeout = timeout

    def _request(self, method: str, path: str, body: Any = None) -> Any:
        url = self.base + path
        data = None
        headers = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        req = Request(url, data=data, method=method, headers=headers)
        try:
            with urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read()
                if not raw:
                    return None
                return json.loads(raw)
        except HTTPError as e:
            err_body = ""
            try:
                err_body = e.read().decode("utf-8", errors="replace")
            except Exception:
                pass
            raise KernError(f"{e.code} {e.reason}: {err_body}", e.code) from None
        except URLError as e:
            raise KernError(f"connection error: {e.reason}") from None

    def _post(self, path: str, body: Any = None) -> Any:
        return self._request("POST", path, body)

    def _get(self, path: str) -> Any:
        return self._request("GET", path)

    # --- API methods ---

    def analyze(self, change: str) -> dict:
        return self._post("/v1/analyze", {"change": change})

    def plan(self, change: str) -> dict:
        return self._post("/v1/plan", {"change": change})

    def what_if(self, change: str, kind: str, new_target: str = "") -> dict:
        body = {"change": change, "kind": kind}
        if new_target:
            body["new_target"] = new_target
        return self._post("/v1/what-if", body)

    def impact(self, change: str) -> dict:
        return self._post("/v1/impact", {"change": change})

    def verify(self, types: list = None) -> dict:
        return self._post("/v1/verify", {"types": types or []})

    def investigate_incident(self, alert: dict) -> dict:
        return self._post("/v1/incidents/investigate", {"alert": alert})

    def memory_list(self) -> dict:
        return self._get("/v1/memory")

    def memory_add(self, content: str, mem_type: str = "lesson", scope: str = "", tags: list = None) -> dict:
        return self._post("/v1/memory", {
            "content": content,
            "type": mem_type,
            "scope": scope,
            "tags": tags or [],
        })

    def graph(self, entity: str) -> dict:
        from urllib.parse import quote
        return self._get(f"/v1/graph/{quote(entity, safe='')}")

    def context(self, change: str) -> dict:
        return self._post("/v1/context", {"change": change})

    def risk(self, change: str) -> dict:
        return self._post("/v1/risk", {"change": change})

    def task(self, task_id: str) -> dict:
        from urllib.parse import quote
        return self._get(f"/v1/tasks/{quote(task_id, safe='')}")

    def agents(self) -> dict:
        return self._post("/v1/agents", {})

    def loop(self, intent: str, level: str = "L0") -> dict:
        return self._post("/v1/loop", {"intent": intent, "level": level})

    def task_submit(self, input: str, task_type: str = "code") -> dict:
        return self._post("/v1/tasks", {"input": input, "type": task_type})

    def execute(self, patch: str) -> dict:
        return self._post("/v1/execute", {"patch": patch})

    def correlate(self, alert: dict, snapshot: str = "") -> dict:
        body = {"alert": alert}
        if snapshot:
            body["snapshot"] = snapshot
        return self._post("/v1/correlate", body)

    def learn(self, threshold: int = 3) -> dict:
        return self._post("/v1/learn", {"threshold": threshold})

    def modernize(self) -> dict:
        return self._post("/v1/modernize", {})

    def artifacts_list(self) -> dict:
        return self._get("/v1/artifacts")

    def artifact_get(self, artifact_id: str) -> dict:
        from urllib.parse import quote
        return self._get(f"/v1/artifacts/{quote(artifact_id, safe='')}")

    def audit(self, task_id: str) -> dict:
        if not task_id:
            raise ValueError("audit() requires a task_id")
        from urllib.parse import quote
        return self._get(f"/v1/audit/{quote(task_id, safe='')}")

    def approve(self, approval_id: str, approver: str) -> dict:
        """Approve a pending approval (Phase 19 REST: /v1/approve)."""
        if not approval_id or not approver:
            raise ValueError("approve() requires approval_id and approver")
        return self._post("/v1/approve", {"id": approval_id, "approver": approver})

    def reject(self, approval_id: str, approver: str) -> dict:
        """Reject a pending approval (Phase 19 REST: /v1/reject)."""
        if not approval_id or not approver:
            raise ValueError("reject() requires approval_id and approver")
        return self._post("/v1/reject", {"id": approval_id, "approver": approver})

    def deploy(self, task_id: str, version: str = "") -> dict:
        """Deploy a task through the task-action alias (Phase 19: /v1/tasks/{id}/deploy)."""
        if not task_id:
            raise ValueError("deploy() requires a task_id")
        from urllib.parse import quote
        return self._post(f"/v1/tasks/{quote(task_id, safe='')}/deploy", {"version": version})

    def approvals_pending(self) -> dict:
        """Return the pending approval roster (GET /v1/approvals/pending)."""
        return self._get("/v1/approvals/pending")

    def incidents(self) -> dict:
        """Return a flattened summary of persisted incidents (GET /v1/incidents)."""
        return self._get("/v1/incidents")

    def incident(self, incident_id: str) -> dict:
        """Return a single incident by ID (GET /v1/incidents/{id})."""
        if not incident_id:
            raise ValueError("incident() requires an incident_id")
        from urllib.parse import quote
        return self._get(f"/v1/incidents/{quote(incident_id, safe='')}")

    def events_stream(self):
        """Yield parsed "data:" payloads from the SSE stream (GET /v1/events/stream).

        A blocking generator: each yield is the JSON-decoded data line of an SSE
        frame (or the raw string when it is not valid JSON). Callers iterate it in
        a background thread and consume until it ends (on connection close).
        """
        from urllib.request import Request
        url = self.base + "/v1/events/stream"
        req = Request(url, method="GET", headers={"Accept": "text/event-stream"})
        with urlopen(req, timeout=self.timeout) as resp:
            for line in resp:
                line = line.decode("utf-8", errors="replace").strip()
                if line.startswith("data:"):
                    payload = line[len("data:"):].strip()
                    if payload:
                        try:
                            yield json.loads(payload)
                        except Exception:
                            yield payload