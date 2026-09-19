import unittest
from unittest.mock import MagicMock
from kern_sdk import KernEngine, LangChainKernCompressor, LlamaIndexKernCompressor


class TestKernPythonIntegrations(unittest.TestCase):
    def test_engine_init(self):
        engine = KernEngine(binary_path="/mock/bin/kern")
        self.assertEqual(engine.binary_path, "/mock/bin/kern")

    def test_langchain_compressor(self):
        mock_engine = MagicMock()
        mock_engine.optimize_log.return_value = "ERROR database timeout"

        class MockDocument:
            def __init__(self, content):
                self.page_content = content
            def copy(self):
                return MockDocument(self.page_content)

        compressor = LangChainKernCompressor(engine=mock_engine)
        docs = [MockDocument("2024-01-01 INFO 500 lines of chatter\nERROR database timeout")]
        compressed = compressor.transform_documents(docs)
        self.assertEqual(len(compressed), 1)
        self.assertEqual(compressed[0].page_content, "ERROR database timeout")

    def test_llamaindex_compressor(self):
        mock_engine = MagicMock()
        mock_engine.optimize_log.return_value = "ERROR failed to connect"

        class MockNode:
            def __init__(self, content):
                self._content = content
            def get_content(self):
                return self._content
            def set_content(self, c):
                self._content = c

        class MockNodeWithScore:
            def __init__(self, node):
                self.node = node

        postprocessor = LlamaIndexKernCompressor(engine=mock_engine)
        nodes = [MockNodeWithScore(MockNode("100 lines\nERROR failed to connect"))]
        res = postprocessor.postprocess_nodes(nodes)
        self.assertEqual(len(res), 1)
        self.assertEqual(res[0].node.get_content(), "ERROR failed to connect")


if __name__ == "__main__":
    unittest.main()
