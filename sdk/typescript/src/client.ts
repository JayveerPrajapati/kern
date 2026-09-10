/**
 * Thin HTTP client for the kern-server REST API.
 */

const DEFAULT_BASE_URL = "http://localhost:8090";

export class KernError extends Error {
  status: number;
  constructor(message: string, status: number = 0) {
    super(message);
    this.name = "KernError";
    this.status = status;
  }
}

export interface AnalyzeResult {
  packet: any;
  text: string;
}

export interface IncidentInvestigation {
  incident: any;
  hypotheses: any[];
  affected_service: string;
}

export class Client {
  private base: string;
  private timeout: number;

  constructor(baseURL: string = DEFAULT_BASE_URL, timeout: number = 10000) {
    this.base = baseURL.replace(/\/+$/, "");
    this.timeout = timeout;
  }

  private async request(method: string, path: string, body?: any): Promise<any> {
    const url = this.base + path;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeout);

    try {
      const headers: Record<string, string> = { Accept: "application/json" };
      let data: string | undefined;
      if (body !== undefined) {
        data = JSON.stringify(body);
        headers["Content-Type"] = "application/json";
      }
      const resp = await fetch(url, {
        method,
        headers,
        body: data,
        signal: controller.signal,
      });
      clearTimeout(timer);

      const text = await resp.text();
      if (!resp.ok) {
        throw new KernError(`${resp.status} ${resp.statusText}: ${text}`, resp.status);
      }
      if (!text) return null;
      return JSON.parse(text);
    } catch (e) {
      clearTimeout(timer);
      if (e instanceof KernError) throw e;
      if (e instanceof Error) {
        if (e.name === "AbortError") {
          throw new KernError(`request timeout after ${this.timeout}ms`);
        }
        throw new KernError(`connection error: ${e.message}`);
      }
      throw e;
    }
  }

  private post(path: string, body?: any): Promise<any> {
    return this.request("POST", path, body);
  }

  private get(path: string): Promise<any> {
    return this.request("GET", path);
  }

  // --- API methods ---

  health(): Promise<any> {
    return this.get("/api/health");
  }

  analyze(change: string): Promise<AnalyzeResult> {
    return this.post("/v1/analyze", { change });
  }

  plan(change: string): Promise<AnalyzeResult> {
    return this.post("/v1/plan", { change });
  }

  whatIf(change: string, kind: string, newTarget: string = ""): Promise<any> {
    const body: any = { change, kind };
    if (newTarget) body.new_target = newTarget;
    return this.post("/v1/what-if", body);
  }

  impact(change: string): Promise<any> {
    return this.post("/v1/impact", { change });
  }

  verify(types: string[] = []): Promise<any> {
    return this.post("/v1/verify", { types });
  }

  investigateIncident(alert: any): Promise<IncidentInvestigation> {
    return this.post("/v1/incidents/investigate", { alert });
  }

  memoryList(): Promise<any> {
    return this.get("/v1/memory");
  }

  memoryAdd(content: string, memType: string = "lesson", scope: string = "", tags: string[] = []): Promise<any> {
    return this.post("/v1/memory", { content, type: memType, scope, tags });
  }

  graph(entity: string): Promise<any> {
    return this.get(`/v1/graph/${encodeURIComponent(entity)}`);
  }

  context(change: string): Promise<any> {
    return this.post("/v1/context", { change });
  }

  risk(change: string): Promise<any> {
    return this.post("/v1/risk", { change });
  }

  task(taskId: string): Promise<any> {
    return this.get(`/v1/tasks/${encodeURIComponent(taskId)}`);
  }

  agents(): Promise<any> {
    return this.post("/v1/agents", {});
  }

  loop(intent: string, level: string = "L0"): Promise<any> {
    return this.post("/v1/loop", { intent, level });
  }

  taskSubmit(input: string, taskType: string = "code"): Promise<any> {
    return this.post("/v1/tasks", { input, type: taskType });
  }

  execute(patch: string): Promise<any> {
    return this.post("/v1/execute", { patch });
  }

  correlate(alert: any, snapshot: string = ""): Promise<any> {
    const body: any = { alert };
    if (snapshot) body.snapshot = snapshot;
    return this.post("/v1/correlate", body);
  }

  learn(threshold: number = 3): Promise<any> {
    return this.post("/v1/learn", { threshold });
  }

  modernize(): Promise<any> {
    return this.post("/v1/modernize", {});
  }

  artifactsList(): Promise<any> {
    return this.get("/v1/artifacts");
  }

  artifactGet(artifactId: string): Promise<any> {
    return this.get(`/v1/artifacts/${encodeURIComponent(artifactId)}`);
  }

  audit(taskId: string): Promise<any> {
    if (!taskId) {
      throw new KernError("audit() requires a taskId");
    }
    return this.get(`/v1/audit/${encodeURIComponent(taskId)}`);
  }

  approve(approvalId: string, approver: string): Promise<any> {
    if (!approvalId || !approver) {
      throw new KernError("approve() requires approvalId and approver");
    }
    return this.post("/v1/approve", { id: approvalId, approver });
  }

  reject(approvalId: string, approver: string): Promise<any> {
    if (!approvalId || !approver) {
      throw new KernError("reject() requires approvalId and approver");
    }
    return this.post("/v1/reject", { id: approvalId, approver });
  }

  deploy(taskId: string, version: string = ""): Promise<any> {
    if (!taskId) {
      throw new KernError("deploy() requires a taskId");
    }
    return this.post(`/v1/tasks/${encodeURIComponent(taskId)}/deploy`, { version });
  }

  approvalsPending(): Promise<any> {
    return this.get("/v1/approvals/pending");
  }

  incidents(): Promise<any> {
    return this.get("/v1/incidents");
  }

  incident(incidentId: string): Promise<any> {
    if (!incidentId) {
      throw new KernError("incident() requires an incidentId");
    }
    return this.get(`/v1/incidents/${encodeURIComponent(incidentId)}`);
  }

  /**
   * Opens the live event stream (GET /v1/events/stream) and yields each
   * SSE "data:" payload as JSON (or the raw string when it is not valid
   * JSON). The generator ends when the server closes the stream.
   */
  async *eventsStream(): AsyncGenerator<any> {
    const url = this.base + "/v1/events/stream";
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeout);
    let resp: Response;
    try {
      resp = await fetch(url, {
        method: "GET",
        headers: { Accept: "text/event-stream" },
        signal: controller.signal,
      });
      clearTimeout(timer);
    } catch (e) {
      clearTimeout(timer);
      if (e instanceof Error && e.name === "AbortError") {
        throw new KernError(`request timeout after ${this.timeout}ms`);
      }
      throw new KernError(`connection error: ${e instanceof Error ? e.message : String(e)}`);
    }
    if (!resp.ok) {
      throw new KernError(`${resp.status} ${resp.statusText}`, resp.status);
    }
    if (!resp.body) {
      throw new KernError("events stream: no response body");
    }
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let idx: number;
      while ((idx = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, idx).trim();
        buf = buf.slice(idx + 1);
        if (!line.startsWith("data:")) continue;
        const payload = line.slice(5).trim();
        if (!payload) continue;
        try {
          yield JSON.parse(payload);
        } catch {
          yield payload;
        }
      }
    }
  }
}