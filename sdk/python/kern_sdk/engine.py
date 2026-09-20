"""Local execution engine for the kern CLI binary."""

import os
import platform
import shutil
import subprocess
from pathlib import Path
from typing import Optional


class KernEngine:
    """Subprocess runner for local, deterministic kern context optimization.

    Searches for an embedded binary, a custom binary path (via KERN_BINARY_PATH),
    or falls back to the system PATH.
    """

    def __init__(self, binary_path: Optional[str] = None):
        self.binary_path = binary_path or self._resolve_binary()

    def _resolve_binary(self) -> str:
        if os.getenv("KERN_BINARY_PATH"):
            return os.getenv("KERN_BINARY_PATH")

        system = platform.system().lower()
        machine = platform.machine().lower()
        base_dir = Path(__file__).parent / "bin"

        if system == "linux" and ("64" in machine or "x86_64" in machine):
            candidate = base_dir / "kern_linux_amd64"
            if candidate.is_file():
                return str(candidate)
        elif system == "darwin" and ("arm" in machine or "aarch" in machine):
            candidate = base_dir / "kern_darwin_arm64"
            if candidate.is_file():
                return str(candidate)
        elif system == "windows" and ("64" in machine or "amd64" in machine):
            candidate = base_dir / "kern_windows_amd64.exe"
            if candidate.is_file():
                return str(candidate)

        # Fallback to system path lookup
        sys_path = shutil.which("kern")
        if sys_path:
            return sys_path

        # Default fallback
        return "kern"

    def optimize_log(self, log_text: str, max_lines: int = 200, context_before: int = 0, context_after: int = 0) -> str:
        """Compress logs down to critical error/stack traces with adaptive windowing."""
        cmd = [self.binary_path, "log"]
        if context_before > 0:
            cmd.extend(["--context-before", str(context_before)])
        if context_after > 0:
            cmd.extend(["--context-after", str(context_after)])
        try:
            process = subprocess.Popen(
                cmd, shell=False,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            stdout, stderr = process.communicate(input=log_text)
            if process.returncode != 0:
                raise RuntimeError(f"kern error: {stderr.strip()}")
            return stdout
        except Exception as e:
            raise RuntimeError(f"Failed to execute kern engine: {e}") from e

    def optimize_prompt(self, prompt: str, attached_log: str = "") -> str:
        """Strip fluff from prompts and attach compressed logs."""
        cmd = [self.binary_path, "optimize", prompt]
        if attached_log:
            cmd.extend(["--attached-log", attached_log])
        try:
            process = subprocess.Popen(
                cmd, shell=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            stdout, stderr = process.communicate()
            if process.returncode != 0:
                raise RuntimeError(f"kern error: {stderr.strip()}")
            return stdout
        except Exception as e:
            raise RuntimeError(f"Failed to execute kern engine: {e}") from e

    def fetch_raw_anchor(self, file_or_symbol: str, lines: int = 0, root: str = ".") -> str:
        """Hydrate raw context for an anchor citation or omitted block."""
        cmd = [self.binary_path, "context", file_or_symbol, "--root", root]
        if lines > 0:
            cmd.extend(["--lines", str(lines)])
        try:
            process = subprocess.Popen(
                cmd, shell=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            stdout, stderr = process.communicate()
            if process.returncode != 0:
                raise RuntimeError(f"kern error: {stderr.strip()}")
            return stdout
        except Exception as e:
            raise RuntimeError(f"Failed to execute kern engine: {e}") from e

    def search(self, query: str, root: str = ".", limit: int = 20, semantic: bool = False) -> str:
        """Execute AST & semantic symbol search across codebase."""
        cmd = [self.binary_path, "search", query, "--json", "--root", root]
        if limit > 0:
            cmd.extend(["--limit", str(limit)])
        if semantic:
            cmd.append("--semantic")
        try:
            process = subprocess.Popen(
                cmd, shell=False,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            stdout, stderr = process.communicate()
            if process.returncode != 0:
                raise RuntimeError(f"kern error: {stderr.strip()}")
            return stdout
        except Exception as e:
            raise RuntimeError(f"Failed to execute kern engine: {e}") from e

