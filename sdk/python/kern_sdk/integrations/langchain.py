"""LangChain Document Transformer integration for Kern context compression."""

from typing import Any, Sequence
from ..engine import KernEngine


class LangChainKernCompressor:
    """LangChain-compatible Document Transformer that applies Kern deterministic compression.

    Usage:
        from kern_sdk.integrations import LangChainKernCompressor
        compressor = LangChainKernCompressor()
        compressed_docs = compressor.transform_documents(docs)
    """

    def __init__(self, engine: Any = None):
        self.engine = engine or KernEngine()

    def transform_documents(self, documents: Sequence[Any], **kwargs: Any) -> Sequence[Any]:
        """Compress text contents of a sequence of LangChain Document objects."""
        compressed_docs = []
        for doc in documents:
            content = getattr(doc, "page_content", str(doc))
            compressed_content = self.engine.optimize_log(content)
            if hasattr(doc, "copy"):
                new_doc = doc.copy()
                new_doc.page_content = compressed_content
                compressed_docs.append(new_doc)
            elif hasattr(doc, "page_content"):
                doc.page_content = compressed_content
                compressed_docs.append(doc)
            else:
                compressed_docs.append(compressed_content)
        return compressed_docs
