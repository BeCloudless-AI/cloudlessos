#!/usr/bin/python3
import importlib.machinery
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "distro/scripts/prepare-physical-qualification.py"
loader = importlib.machinery.SourceFileLoader("prepare_physical_qualification", str(SCRIPT))
spec = importlib.util.spec_from_loader(loader.name, loader)
physical = importlib.util.module_from_spec(spec)
loader.exec_module(physical)


class PhysicalReleaseTest(unittest.TestCase):
    def setUp(self):
        self.matrix = ROOT / "distro/release/physical-validation-matrix.json"
        self.commit = "0123456789abcdef0123456789abcdef01234567"

    def test_pre_one_stable_and_beta_can_disclose_missing_physical_evidence(self):
        stable = physical.prepare("0.9.9", "stable", self.commit, self.matrix, [])
        beta = physical.prepare("1.0.0-rc1", "beta", self.commit, self.matrix, [])
        self.assertEqual(stable["status"], "not-qualified")
        self.assertFalse(stable["required"])
        self.assertEqual(beta["status"], "not-qualified")
        self.assertFalse(beta["required"])

    def test_one_zero_stable_requires_the_complete_set(self):
        with self.assertRaisesRegex(ValueError, "require the complete physical qualification set"):
            physical.prepare("1.0.0", "stable", self.commit, self.matrix, [])

    def test_matching_complete_set_is_bound_to_release_identity(self):
        qualification_set = {
            "schema": "cloudless.physical-set.v1",
            "version": "1.0.0",
            "sourceCommit": self.commit,
            "matrixSha256": physical.sha256_file(self.matrix),
            "verifiedAt": "2026-07-31T00:00:00Z",
            "targets": ["all-required-targets"],
            "archives": [{"target": "all-required-targets", "sha256": "f" * 64, "bytes": 1}],
        }
        qualifier = mock.Mock()
        qualifier.verify_export_set.return_value = qualification_set
        with mock.patch.object(physical, "load_qualifier", return_value=qualifier):
            result = physical.prepare("1.0.0", "stable", self.commit, self.matrix, [Path("proof.zip")])
        self.assertEqual(result["status"], "qualified")
        self.assertTrue(result["required"])
        self.assertEqual(result["qualificationSet"], qualification_set)
        self.assertRegex(result["qualificationSetSha256"], r"^[0-9a-f]{64}$")

    def test_mixed_candidate_set_is_rejected(self):
        qualifier = mock.Mock()
        qualifier.verify_export_set.return_value = {
            "version": "0.9.9",
            "sourceCommit": self.commit,
            "matrixSha256": physical.sha256_file(self.matrix),
        }
        with mock.patch.object(physical, "load_qualifier", return_value=qualifier):
            with self.assertRaisesRegex(ValueError, "does not match the release"):
                physical.prepare("1.0.0", "stable", self.commit, self.matrix, [Path("proof.zip")])

    def test_descriptor_is_written_atomically_with_private_permissions(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "qualification.json"
            payload = physical.prepare("0.9.9", "stable", self.commit, self.matrix, [])
            physical.atomic_json(output, payload)
            self.assertEqual(json.loads(output.read_text(encoding="utf-8")), payload)
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
