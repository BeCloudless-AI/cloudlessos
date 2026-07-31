#!/usr/bin/python3
from __future__ import annotations

import datetime as dt
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("prepare-security-readiness.py")
SPEC = importlib.util.spec_from_file_location("security_readiness", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
SPEC.loader.exec_module(MODULE)


class SecurityReadinessTests(unittest.TestCase):
    now = dt.datetime(2026, 7, 31, 12, 0, tzinfo=dt.timezone.utc)
    commit = "a" * 40

    def attestation(self, **changes) -> Path:
        document = {
            "schema": "cloudless.security-contact-attestation.v1",
            "monitored": True,
            "securityContact": "mailto:security@becloudless.ai",
            "escalationOwner": "CloudlessOS release owner",
            "verifiedAt": "2026-07-30T12:00:00Z",
            "acknowledgementBusinessDays": 3,
            **changes,
        }
        directory = Path(tempfile.mkdtemp())
        path = directory / "attestation.json"
        path.write_text(json.dumps(document), encoding="utf-8")
        return path

    def test_pre_one_without_attestation_is_explicitly_not_operational(self):
        result = MODULE.prepare("0.2.6", "stable", self.commit, None, self.now)
        self.assertEqual(result["status"], "not-operational")
        self.assertFalse(result["required"])

    def test_one_zero_stable_fails_closed_without_attestation(self):
        with self.assertRaisesRegex(ValueError, "require a current"):
            MODULE.prepare("1.0.0", "stable", self.commit, None, self.now)

    def test_current_operational_attestation_is_public_release_evidence(self):
        result = MODULE.prepare("1.0.0", "stable", self.commit, self.attestation(), self.now)
        self.assertEqual(result["status"], "operational")
        self.assertEqual(result["securityContact"], "mailto:security@becloudless.ai")
        self.assertNotIn("schema", result.get("attestation", {}))

    def test_stale_attestation_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "older than 30 days"):
            MODULE.prepare("1.0.0", "stable", self.commit, self.attestation(verifiedAt="2026-06-01T00:00:00Z"), self.now)

    def test_unmonitored_or_credentialed_contact_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "explicitly attested"):
            MODULE.prepare("1.0.0", "stable", self.commit, self.attestation(monitored=False), self.now)
        with self.assertRaisesRegex(ValueError, "without credentials"):
            MODULE.prepare("1.0.0", "stable", self.commit, self.attestation(securityContact="https://user:pass@example.com/security"), self.now)


if __name__ == "__main__":
    unittest.main()
