"""Tests for kern_sdk.Client using a mocked urlopen — no real HTTP calls."""

import io
import json
import unittest
from unittest import mock
from urllib.error import HTTPError

from kern_sdk import Client, KernError


class ClientTest(unittest.TestCase):
    def setUp(self):
        self.client = Client(base_url="http://test:8090")

    def _call(self, fn, *args, **kwargs):
        """Run fn against a mocked urlopen and return (response, request)."""
        payload = kwargs.pop("_payload", {"ok": True})
        with mock.patch(
            "kern_sdk.client.urlopen", return_value=make_url_response(payload)
        ) as m:
            out = fn(*args, **kwargs)
        req = m.call_args.args[0]
        return out, req

    def test_analyze(self):
        out, req = self._call(self.client.analyze, "change x")
        self.assertEqual(out, {"ok": True})
        self.assertEqual(req.method, "POST")
        self.assertEqual(req.full_url, "http://test:8090/v1/analyze")
        self.assertEqual(json.loads(req.data), {"change": "change x"})
        self.assertEqual(req.get_header("Content-type"), "application/json")

    def test_plan(self):
        out, req = self._call(self.client.plan, "change y")
        self.assertEqual(req.method, "POST")
        self.assertEqual(req.full_url, "http://test:8090/v1/plan")
        self.assertEqual(json.loads(req.data), {"change": "change y"})

    def test_what_if_defaults(self):
        out, req = self._call(self.client.what_if, "change", "log")
        self.assertEqual(req.full_url, "http://test:8090/v1/what-if")
        self.assertEqual(json.loads(req.data), {"change": "change", "kind": "log"})

    def test_what_if_with_target(self):
        out, req = self._call(self.client.what_if, "change", "log", "http://new")
        self.assertEqual(
            json.loads(req.data),
            {"change": "change", "kind": "log", "new_target": "http://new"},
        )

    def test_memory_add(self):
        out, req = self._call(
            self.client.memory_add, "content", "note", "svc", ["a", "b"]
        )
        self.assertEqual(req.method, "POST")
        self.assertEqual(req.full_url, "http://test:8090/v1/memory")
        self.assertEqual(
            json.loads(req.data),
            {"content": "content", "type": "note", "scope": "svc", "tags": ["a", "b"]},
        )

    def test_memory_add_defaults(self):
        out, req = self._call(self.client.memory_add, "content")
        self.assertEqual(
            json.loads(req.data),
            {"content": "content", "type": "lesson", "scope": "", "tags": []},
        )

    def test_task(self):
        out, req = self._call(self.client.task, "abc def")
        self.assertEqual(req.method, "GET")
        self.assertEqual(req.full_url, "http://test:8090/v1/tasks/abc%20def")

    def test_loop(self):
        out, req = self._call(self.client.loop, "intent", "L3")
        self.assertEqual(req.method, "POST")
        self.assertEqual(req.full_url, "http://test:8090/v1/loop")
        self.assertEqual(json.loads(req.data), {"intent": "intent", "level": "L3"})

    def test_loop_default_level(self):
        out, req = self._call(self.client.loop, "intent")
        self.assertEqual(json.loads(req.data), {"intent": "intent", "level": "L0"})

    def test_execute(self):
        out, req = self._call(self.client.execute, "patch text")
        self.assertEqual(req.method, "POST")
        self.assertEqual(req.full_url, "http://test:8090/v1/execute")
        self.assertEqual(json.loads(req.data), {"patch": "patch text"})

    def test_audit_with_task(self):
        out, req = self._call(self.client.audit, "task-123")
        self.assertEqual(req.method, "GET")
        self.assertEqual(req.full_url, "http://test:8090/v1/audit/task-123")

    def test_audit_empty_raises_value_error(self):
        with self.assertRaises(ValueError):
            self.client.audit("")
        with self.assertRaises(ValueError):
            self.client.audit(None)

    def test_correlate_with_snapshot(self):
        out, req = self._call(
            self.client.correlate, {"sev": "high"}, "snap-1"
        )
        self.assertEqual(req.full_url, "http://test:8090/v1/correlate")
        self.assertEqual(
            json.loads(req.data),
            {"alert": {"sev": "high"}, "snapshot": "snap-1"},
        )

    def test_correlate_without_snapshot(self):
        out, req = self._call(self.client.correlate, {"sev": "high"})
        self.assertEqual(json.loads(req.data), {"alert": {"sev": "high"}})

    def test_error_status_raises_kern_error(self):
        http_error = HTTPError(
            "http://test:8090/v1/tasks/missing",
            404,
            "Not Found",
            {},
            io.BytesIO(b'{"error": "no such task"}'),
        )
        with mock.patch(
            "kern_sdk.client.urlopen", side_effect=http_error
        ):
            with self.assertRaises(KernError) as ctx:
                self.client.task("missing")
        self.assertEqual(ctx.exception.status, 404)
        self.assertIn("no such task", str(ctx.exception))

    def test_connection_error_raises_kern_error(self):
        from urllib.error import URLError

        with mock.patch(
            "kern_sdk.client.urlopen", side_effect=URLError("conn refused")
        ):
            with self.assertRaises(KernError) as ctx:
                self.client.memory_list()
        self.assertIn("connection error", str(ctx.exception))


def make_url_response(payload):
    resp = mock.Mock()
    resp.__enter__ = mock.Mock(return_value=resp)
    resp.__exit__ = mock.Mock(return_value=False)
    resp.read = mock.Mock(return_value=json.dumps(payload).encode("utf-8"))
    return resp


if __name__ == "__main__":
    unittest.main()