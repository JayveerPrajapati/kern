"""LangGraph Context Compressor & State Reducer for agent memory optimization."""

from typing import Any, Dict, List, Optional
from ..engine import KernEngine


class LangGraphKernReducer:
    """State reducer and message trimmer for LangGraph workflows.

    Usage:
        from kern_sdk.integrations import LangGraphKernReducer
        reducer = LangGraphKernReducer()
        trimmed_state = reducer.reduce_state(state)
    """

    def __init__(self, engine: Optional[KernEngine] = None, max_messages: int = 20):
        self.engine = engine or KernEngine()
        self.max_messages = max_messages

    def reduce_messages(self, messages: List[Any]) -> List[Any]:
        """Compress tool messages and stack traces in a LangGraph message stream."""
        reduced = []
        for msg in messages:
            content = getattr(msg, "content", "")
            if isinstance(content, str) and len(content) > 1000:
                # If message contains stack traces or logs, compress with kern
                if "error" in content.lower() or "traceback" in content.lower() or "exception" in content.lower():
                    try:
                        compressed = self.engine.optimize_log(content)
                        if hasattr(msg, "content"):
                            msg.content = compressed
                    except Exception:
                        pass
            reduced.append(msg)
        return reduced

    def reduce_state(self, state: Dict[str, Any]) -> Dict[str, Any]:
        """Apply deterministic compression across state dictionary values."""
        if "messages" in state and isinstance(state["messages"], list):
            state["messages"] = self.reduce_messages(state["messages"])
        return state
