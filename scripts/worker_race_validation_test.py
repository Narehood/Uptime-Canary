"""Regression coverage for race-runner diagnostics, without compiling Go."""
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("worker_race", Path(__file__).with_name("worker-race-validation.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


def output(*events):
    return ("diagnostic outside JSON\n" + "\n".join(json.dumps(e) for e in events)).encode()


class TestRaceDiagnostics(unittest.TestCase):
    def test_package_timeout_is_visible_without_named_failure(self):
        result = runner.summarize_tests(output(
            {"Action": "run", "Package": "canary", "Test": "TestPaging"},
            {"Action": "output", "Package": "canary", "Output": "panic: test timed out after 20m0s\n"},
            {"Action": "fail", "Package": "canary"},
        ))
        self.assertEqual(result["failedTests"], [])
        self.assertEqual(result["failedPackages"], ["canary"])
        self.assertEqual(result["unfinishedTests"], ["TestPaging"])
        self.assertEqual(result["panicMessages"], ["panic: test timed out after 20m0s"])

    def test_terminal_events_clear_running_tests_and_keep_outcomes(self):
        events = []
        for name, action in (("TestOK", "pass"), ("TestBroken", "fail"), ("TestOptional", "skip")):
            events.extend(({"Action": "run", "Test": name}, {"Action": action, "Test": name}))
        result = runner.summarize_tests(output(*events))
        self.assertEqual(result["passedTestEvents"], 1)
        self.assertEqual([e["Test"] for e in result["failedTests"]], ["TestBroken"])
        self.assertEqual([e["Test"] for e in result["skippedTests"]], ["TestOptional"])
        self.assertEqual(result["unfinishedTests"], [])
        self.assertEqual(result["failedPackages"], [])


if __name__ == "__main__":
    unittest.main()
