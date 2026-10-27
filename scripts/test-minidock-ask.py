#!/usr/bin/env python3
"""Offline tests for the MiniDock OpenRouter request and response validation."""

import json
import sys
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import minidock_ask


class FakeResponse:
    def __init__(self, payload):
        self.payload = json.dumps(payload).encode()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False

    def read(self, _limit):
        return self.payload


def completion(spec):
    return {"choices": [{"message": {"content": json.dumps(spec)}}]}


class AskTests(unittest.TestCase):
    def setUp(self):
        self.spec = {
            "image": "alpine-local",
            "command": ["/bin/sh", "-c", "echo hi"],
            "memory_limit": "64M",
            "cpu_limit": None,
            "network": False,
        }

    def test_request_uses_json_mode_and_local_images(self):
        observed = {}

        def opener(request, timeout):
            observed["url"] = request.full_url
            observed["headers"] = request.headers
            observed["body"] = json.loads(request.data)
            observed["timeout"] = timeout
            return FakeResponse(completion(self.spec))

        result = minidock_ask.request_spec("say hi", ["alpine-local"], "dummy", opener=opener)
        self.assertEqual(self.spec, result)
        self.assertEqual(minidock_ask.ENDPOINT, observed["url"])
        self.assertEqual("Bearer dummy", observed["headers"]["Authorization"])
        self.assertEqual({"type": "json_object"}, observed["body"]["response_format"])
        self.assertEqual("openrouter/auto", observed["body"]["model"])
        self.assertIn('"staged_images": ["alpine-local"]', observed["body"]["messages"][1]["content"])
        self.assertEqual(45, observed["timeout"])

    def test_rejects_image_not_staged_locally(self):
        def opener(_request, timeout):
            return FakeResponse(completion(self.spec))

        with self.assertRaisesRegex(ValueError, "not staged locally"):
            minidock_ask.request_spec("say hi", ["other-image"], "dummy", opener=opener)

    def test_rejects_non_argv_command(self):
        self.spec["command"] = "/bin/sh -c echo hi"
        with self.assertRaisesRegex(ValueError, "invalid command"):
            minidock_ask.validate_spec(self.spec, ["alpine-local"])


if __name__ == "__main__":
    unittest.main()
