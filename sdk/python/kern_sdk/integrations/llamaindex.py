"""LlamaIndex Node Postprocessor integration for Kern context compression."""

from typing import Any, List, Optional
from ..engine import KernEngine


class LlamaIndexKernCompressor:
    """LlamaIndex-compatible Node Postprocessor that slims retrieved chunks.

    Usage:
        from kern_sdk.integrations import LlamaIndexKernCompressor
        postprocessor = LlamaIndexKernCompressor()
        filtered_nodes = postprocessor.postprocess_nodes(nodes)
    """

    def __init__(self, engine: Any = None):
        self.engine = engine or KernEngine()

    def postprocess_nodes(self, nodes: List[Any], query_bundle: Optional[Any] = None) -> List[Any]:
        """Compress raw node text before synthesis in LLM pipelines."""
        for node_with_score in nodes:
            node = getattr(node_with_score, "node", node_with_score)
            if hasattr(node, "get_content"):
                raw_text = node.get_content()
                compressed_text = self.engine.optimize_log(raw_text)
                if hasattr(node, "set_content"):
                    node.set_content(compressed_text)
            elif hasattr(node, "text"):
                node.text = self.engine.optimize_log(node.text)
        return nodes
