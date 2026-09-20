"""Unit tests for Python SDK framework integrations."""

import unittest
from kern_sdk.integrations import (
    LangChainKernCompressor,
    LlamaIndexKernCompressor,
    LangGraphKernReducer,
    CrewAIKernTool,
    AutoGenKernHook,
)


class MockEngine:
    def optimize_log(self, text: str, **kwargs) -> str:
        return f"[COMPRESSED LOG: {len(text)} bytes]"

    def optimize_prompt(self, text: str, **kwargs) -> str:
        return f"[OPTIMIZED PROMPT: {text}]"


class MockDocument:
    def __init__(self, page_content: str, metadata: dict = None):
        self.page_content = page_content
        self.metadata = metadata or {}

    def copy(self):
        return MockDocument(self.page_content, dict(self.metadata))


class MockNode:
    def __init__(self, text: str):
        self.text = text

    def get_content(self):
        return self.text

    def set_content(self, text):
        self.text = text


class MockNodeWithScore:
    def __init__(self, node: MockNode, score: float = 1.0):
        self.node = node
        self.score = score


class MockMessage:
    def __init__(self, content: str):
        self.content = content


class TestIntegrations(unittest.TestCase):
    def setUp(self):
        self.engine = MockEngine()

    def test_langchain_compressor(self):
        compressor = LangChainKernCompressor(engine=self.engine)
        docs = [MockDocument("Error: division by zero\nTraceback at line 42")]
        transformed = compressor.transform_documents(docs)
        self.assertEqual(len(transformed), 1)
        self.assertTrue(transformed[0].page_content.startswith("[COMPRESSED LOG"))

    def test_llamaindex_compressor(self):
        compressor = LlamaIndexKernCompressor(engine=self.engine)
        nodes = [MockNodeWithScore(MockNode("Traceback (most recent call last):"))]
        result = compressor.postprocess_nodes(nodes)
        self.assertEqual(len(result), 1)
        self.assertTrue(result[0].node.get_content().startswith("[COMPRESSED LOG"))

    def test_langgraph_reducer(self):
        reducer = LangGraphKernReducer(engine=self.engine)
        state = {
            "messages": [
                MockMessage("Short message"),
                MockMessage("Error occurred: " + "Traceback at line " * 100),
            ]
        }
        reduced = reducer.reduce_state(state)
        self.assertEqual(len(reduced["messages"]), 2)
        self.assertEqual(reduced["messages"][0].content, "Short message")
        self.assertTrue(reduced["messages"][1].content.startswith("[COMPRESSED LOG"))

    def test_crewai_tool(self):
        tool = CrewAIKernTool(engine=self.engine)
        self.assertEqual(tool.name, "kern_context_optimizer")
        out = tool.run("log", "Error traceback")
        self.assertTrue(out.startswith("[COMPRESSED LOG"))
        prompt_out = tool.run("prompt", "Analyze code")
        self.assertEqual(prompt_out, "[OPTIMIZED PROMPT: Analyze code]")

    def test_autogen_hook(self):
        hook = AutoGenKernHook(engine=self.engine)
        msgs = [
            {"role": "user", "content": "hello"},
            {"role": "assistant", "content": "Traceback error: " + "line " * 300},
        ]
        processed = hook.process_messages(msgs)
        self.assertEqual(processed[0]["content"], "hello")
        self.assertTrue(processed[1]["content"].startswith("[COMPRESSED LOG"))


if __name__ == "__main__":
    unittest.main()
