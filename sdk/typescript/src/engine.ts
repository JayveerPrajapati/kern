declare const require: any;
declare const process: any;
declare const __dirname: string;

export interface LogOptimizeOptions {
  maxLines?: number;
  contextBefore?: number;
  contextAfter?: number;
}

export interface PromptOptimizeOptions {
  attachedLog?: string;
}

export interface SearchOptions {
  root?: string;
  limit?: number;
  semantic?: boolean;
}

export interface CommandOptions {
  root?: string;
}

export class KernEngine {
  public readonly binaryPath: string;

  constructor(binaryPath?: string) {
    this.binaryPath = binaryPath || this._resolveBinary();
  }

  protected _resolveBinary(): string {
    if (typeof process !== "undefined" && process.env && process.env.KERN_BINARY_PATH) {
      return process.env.KERN_BINARY_PATH;
    }

    try {
      const os = require("os");
      const path = require("path");
      const fs = require("fs");

      const platform = os.platform();
      const arch = os.arch();
      const baseDir = path.join(__dirname, "..", "bin");

      if (platform === "linux" && arch === "x64") {
        const candidate = path.join(baseDir, "kern_linux_amd64");
        if (fs.existsSync(candidate)) return candidate;
      } else if (platform === "darwin" && (arch === "arm64" || arch === "arm")) {
        const candidate = path.join(baseDir, "kern_darwin_arm64");
        if (fs.existsSync(candidate)) return candidate;
      } else if (platform === "win32" && arch === "x64") {
        const candidate = path.join(baseDir, "kern_windows_amd64.exe");
        if (fs.existsSync(candidate)) return candidate;
      }
    } catch {
      // Ignore module loading errors in browser/non-node environments
    }

    return "kern";
  }

  private _run(args: string[], stdinInput?: string): Promise<string> {
    return new Promise((resolve, reject) => {
      let spawn: any;
      try {
        spawn = require("child_process").spawn;
      } catch (err: any) {
        return reject(new Error(`child_process is not supported in this runtime: ${err.message}`));
      }

      const child = spawn(this.binaryPath, args, { stdio: ["pipe", "pipe", "pipe"] });
      let stdout = "";
      let stderr = "";

      child.stdout.on("data", (data: any) => {
        stdout += data.toString();
      });

      child.stderr.on("data", (data: any) => {
        stderr += data.toString();
      });

      child.on("error", (err: any) => {
        reject(new Error(`Failed to execute kern engine (${this.binaryPath}): ${err.message}`));
      });

      child.on("close", (code: any) => {
        if (code !== 0) {
          reject(new Error(`kern exited with code ${code}: ${stderr.trim()}`));
        } else {
          resolve(stdout);
        }
      });

      if (stdinInput !== undefined) {
        child.stdin.write(stdinInput);
        child.stdin.end();
      } else {
        child.stdin.end();
      }
    });
  }

  async optimizeLog(logText: string, options: LogOptimizeOptions = {}): Promise<string> {
    const args = ["log"];
    if (options.contextBefore && options.contextBefore > 0) {
      args.push("--context-before", String(options.contextBefore));
    }
    if (options.contextAfter && options.contextAfter > 0) {
      args.push("--context-after", String(options.contextAfter));
    }
    return this._run(args, logText);
  }

  async optimizePrompt(prompt: string, options: PromptOptimizeOptions = {}): Promise<string> {
    const args = ["optimize", prompt];
    if (options.attachedLog) {
      args.push("--attached-log", options.attachedLog);
    }
    return this._run(args);
  }

  async search(query: string, options: SearchOptions = {}): Promise<string> {
    const args = ["search", query, "--json"];
    if (options.root) {
      args.push("--root", options.root);
    }
    if (options.limit && options.limit > 0) {
      args.push("--limit", String(options.limit));
    }
    if (options.semantic) {
      args.push("--semantic");
    }
    return this._run(args);
  }

  async explore(target: string, options: CommandOptions = {}): Promise<string> {
    const args = ["explore", target, "--json"];
    if (options.root) {
      args.push("--root", options.root);
    }
    return this._run(args);
  }

  async plan(change: string, options: CommandOptions = {}): Promise<string> {
    const args = ["plan", change, "--json"];
    if (options.root) {
      args.push("--root", options.root);
    }
    return this._run(args);
  }

  async fetchRawAnchor(fileOrSymbol: string, options: { lines?: number; root?: string } = {}): Promise<string> {
    const args = ["context", fileOrSymbol];
    if (options.root) {
      args.push("--root", options.root);
    }
    if (options.lines && options.lines > 0) {
      args.push("--lines", String(options.lines));
    }
    return this._run(args);
  }
}

