#!/usr/bin/python3
from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("prepare-ci-qualification.py")
SPEC = importlib.util.spec_from_file_location("ci_qualification", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
SPEC.loader.exec_module(MODULE)


class CIQualificationTests(unittest.TestCase):
    commit = "a" * 40
    version = "1.0.0"
    run_id = 12345
    attempt = 2

    def fixture(self, mutate=None) -> Path:
        root = Path(tempfile.mkdtemp())
        run = {
            "id": self.run_id, "run_attempt": self.attempt, "head_sha": self.commit,
            "path": MODULE.WORKFLOW_PATH, "name": MODULE.WORKFLOW_NAME,
            "status": "completed", "conclusion": "success", "event": "push",
            "html_url": "https://github.com/becloudless/cloudlessos/actions/runs/12345",
            "updated_at": "2026-07-31T12:00:00Z",
        }
        jobs = [{"name": name, "status": "completed", "conclusion": "success"} for name in MODULE.REQUIRED_JOBS]
        artifacts = [{"name": f"{prefix}-{self.run_id}-{self.attempt}", "expired": False} for prefix in MODULE.ARTIFACT_PREFIXES]
        documents = {"runs.json": {"workflow_runs": [run]}, "jobs.json": {"jobs": jobs}, "artifacts.json": {"artifacts": artifacts}}
        if mutate:
            mutate(documents)
        for name, document in documents.items():
            (root / name).write_text(json.dumps(document), encoding="utf-8")
        return root

    def test_pre_one_without_evidence_is_explicit(self):
        result = MODULE.prepare("0.2.6", "stable", self.commit, "", None)
        self.assertEqual(result["status"], "not-qualified")
        self.assertFalse(result["required"])

    def test_exact_commit_matrix_and_artifacts_qualify(self):
        result = MODULE.prepare(self.version, "stable", self.commit, "becloudless/cloudlessos", self.fixture())
        self.assertEqual(result["status"], "qualified")
        self.assertTrue(result["required"])
        self.assertEqual(result["runId"], self.run_id)
        self.assertEqual(len(result["jobs"]), 7)
        self.assertEqual(len(result["artifacts"]), 3)

    def test_wrong_commit_is_rejected(self):
        fixture = self.fixture(lambda docs: docs["runs.json"]["workflow_runs"][0].update(head_sha="b" * 40))
        with self.assertRaisesRegex(ValueError, "exactly one"):
            MODULE.prepare(self.version, "stable", self.commit, "becloudless/cloudlessos", fixture)

    def test_failed_or_missing_job_is_rejected(self):
        def mutate(docs):
            docs["jobs.json"]["jobs"][0]["conclusion"] = "failure"
        with self.assertRaisesRegex(ValueError, "did not succeed"):
            MODULE.prepare(self.version, "stable", self.commit, "becloudless/cloudlessos", self.fixture(mutate))

    def test_expired_or_missing_artifact_is_rejected(self):
        def mutate(docs):
            docs["artifacts.json"]["artifacts"][0]["expired"] = True
        with self.assertRaisesRegex(ValueError, "missing retained evidence"):
            MODULE.prepare(self.version, "stable", self.commit, "becloudless/cloudlessos", self.fixture(mutate))


if __name__ == "__main__":
    unittest.main()
