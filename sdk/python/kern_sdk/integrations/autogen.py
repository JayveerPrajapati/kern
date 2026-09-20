"""AutoGen multi-agent conversation hook for context window compression."""

from typing import Any, Dict, List, Optional
from ..engine import KernEngine


class AutoGenKernHook:
    """AutoGen conversation pre-reply hook to prune message bloat and error traces.

    Usage:
        from kern_sdk.integrations import AutoGenKernHook
        hook = AutoGenKernHook()
        user_proxy.register_hook("process_all_messages_before_reply", hook.process_messages)
    """

    def __init__(self, engine: Optional[KernEngine] = None):
        self.engine = engine or KernEngine()

    def process_messages(self, messages: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
        """Prune verbose logs in multi-agent dialogue history."""
        processed = []
        for msg in messages:
            content = msg.get("content", "")
            if isinstance(content, str) and len(content) > 1500:
                if "traceback" in content.lower() or "error" in content.lower():
                    try:
                        msg = dict(msg)
                        msg["content"] = self.engine.optimize_log(content)
                    except Exception:
                        pass
            processed.append(msg)
        return processed
