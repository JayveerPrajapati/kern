"""CrewAI context trimmer and task output optimizer."""

from typing import Any, Optional
from ..engine import KernEngine


class CrewAIKernTool:
    """CrewAI tool wrapper providing AST search and log compression to autonomous crews.

    Usage:
        from kern_sdk.integrations import CrewAIKernTool
        kern_tool = CrewAIKernTool()
        crew = Crew(agents=[...], tasks=[...], tools=[kern_tool])
    """

    name: str = "kern_context_optimizer"
    description: str = "Compresses logs, stack traces, and queries AST symbols across codebase."

    def __init__(self, engine: Optional[KernEngine] = None):
        self.engine = engine or KernEngine()

    def run(self, action: str, text: str) -> str:
        """Execute a kern optimization action: 'log' or 'prompt'."""
        if action == "log":
            return self.engine.optimize_log(text)
        elif action == "prompt":
            return self.engine.optimize_prompt(text)
        return text
