import sys
import unittest
from pathlib import Path
from unittest import mock


sys.path.insert(0, str(Path(__file__).parents[1]))

from kern import _bootstrap  # noqa: E402


class VerifySha256Tests(unittest.TestCase):
    def test_missing_manifest_fails_closed(self):
        with mock.patch.object(
            _bootstrap.urllib.request, "urlopen", side_effect=OSError("offline")
        ):
            with self.assertRaises(SystemExit) as ctx:
                _bootstrap._verify_sha256("/tmp/kern-darwin-arm64.tar.gz", "v0.9.15")
        self.assertIn("SHA256SUMS is required", str(ctx.exception))

    def test_missing_entry_fails_closed(self):
        response = mock.MagicMock()
        response.__enter__.return_value.read.return_value = (
            b"0000  kern-different.tar.gz\n"
        )
        with mock.patch.object(
            _bootstrap.urllib.request, "urlopen", return_value=response
        ):
            with self.assertRaises(SystemExit) as ctx:
                _bootstrap._verify_sha256("/tmp/kern-darwin-arm64.tar.gz", "v0.9.15")
        self.assertIn("does not contain an entry", str(ctx.exception))

    def test_matching_entry_is_returned(self):
        response = mock.MagicMock()
        response.__enter__.return_value.read.return_value = (
            b"abc123  kern-darwin-arm64.tar.gz\n"
        )
        with mock.patch.object(
            _bootstrap.urllib.request, "urlopen", return_value=response
        ):
            self.assertEqual(
                _bootstrap._verify_sha256("/tmp/kern-darwin-arm64.tar.gz", "v0.9.15"),
                "abc123",
            )


if __name__ == "__main__":
    unittest.main()
