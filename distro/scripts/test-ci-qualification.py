#!/usr/bin/python3
from __future__ import annotations

import hashlib
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
    run_id = 1730000000000000000
    attempt = 1

    def fixture(self, mutate=None) -> Path:
        root = Path(tempfile.mkdtemp())

        def retained(name: str, content: bytes) -> dict:
            (root / name).write_bytes(content)
            return {"sha256": hashlib.sha256(content).hexdigest(), "size": len(content)}

        jobs = []
        for index, name in enumerate(sorted(MODULE.REQUIRED_JOBS)):
            filename = f"job-{index}.log"
            jobs.append({"name": name, "status": "success", "log": filename, **retained(filename, name.encode())})
        artifacts = []
        for prefix in sorted(MODULE.ARTIFACT_PREFIXES):
            logical = f"{prefix}-{self.run_id}-{self.attempt}"
            filename = f"{logical}.tar.gz"
            artifacts.append({"name": logical, "file": filename, **retained(filename, logical.encode())})
        document = {
            "schema": MODULE.EVIDENCE_SCHEMA,
            "sourceCommit": self.commit,
            "runId": self.run_id,
            "runAttempt": self.attempt,
            "startedAt": "2026-07-31T12:00:00Z",
            "completedAt": "2026-07-31T12:30:00Z",
            "jobs": jobs,
            "artifacts": artifacts,
        }
        if mutate:
            mutate(document, root)
        path = root / "evidence.json"
        path.write_text(json.dumps(document), encoding="utf-8")
        return path

    def test_pre_one_without_evidence_is_explicit(self):
        result = MODULE.prepare("0.2.6", "stable", self.commit, None)
        self.assertEqual(result["status"], "not-qualified")
        self.assertFalse(result["required"])

    def test_one_requires_local_evidence(self):
        with self.assertRaisesRegex(ValueError, "retained local qualification evidence"):
            MODULE.prepare(self.version, "stable", self.commit, None)

    def test_exact_commit_local_matrix_and_artifacts_qualify(self):
        result = MODULE.prepare(self.version, "stable", self.commit, self.fixture())
        self.assertEqual(result["status"], "qualified")
        self.assertTrue(result["required"])
        self.assertEqual(result["workflow"], MODULE.LOCAL_RUNNER)
        self.assertEqual(result["runId"], self.run_id)
        self.assertEqual(len(result["jobs"]), 7)
        self.assertEqual(len(result["artifacts"]), 3)
        self.assertEqual(len(result["jobEvidence"]), 7)
        self.assertEqual(len(result["artifactEvidence"]), 3)

    def test_wrong_commit_is_rejected(self):
        fixture = self.fixture(lambda doc, _: doc.update(sourceCommit="b" * 40))
        with self.assertRaisesRegex(ValueError, "exact source commit"):
            MODULE.prepare(self.version, "stable", self.commit, fixture)

    def test_failed_or_missing_job_is_rejected(self):
        fixture = self.fixture(lambda doc, _: doc["jobs"][0].update(status="failure"))
        with self.assertRaisesRegex(ValueError, "did not succeed"):
            MODULE.prepare(self.version, "stable", self.commit, fixture)

    def test_changed_job_log_is_rejected(self):
        def mutate(doc, root):
            (root / doc["jobs"][0]["log"]).write_text("changed", encoding="utf-8")

        with self.assertRaisesRegex(ValueError, "size changed|digest changed"):
            MODULE.prepare(self.version, "stable", self.commit, self.fixture(mutate))

    def test_missing_or_changed_artifact_is_rejected(self):
        def mutate(doc, root):
            (root / doc["artifacts"][0]["file"]).unlink()

        with self.assertRaisesRegex(ValueError, "missing"):
            MODULE.prepare(self.version, "stable", self.commit, self.fixture(mutate))

    def test_symlink_evidence_is_rejected(self):
        def mutate(doc, root):
            row = doc["jobs"][0]
            path = root / row["log"]
            target = root / "target.log"
            target.write_bytes(path.read_bytes())
            path.unlink()
            path.symlink_to(target)

        with self.assertRaisesRegex(ValueError, "not a regular file"):
            MODULE.prepare(self.version, "stable", self.commit, self.fixture(mutate))

    def test_path_traversal_is_rejected(self):
        fixture = self.fixture(lambda doc, _: doc["jobs"][0].update(log="../job.log"))
        with self.assertRaisesRegex(ValueError, "plain file names"):
            MODULE.prepare(self.version, "stable", self.commit, fixture)


if __name__ == "__main__":
    unittest.main()
