#!/usr/bin/env python3
"""Contract tests for fail-closed beta promotion verification."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import pathlib
import tempfile
import types
import unittest


SCRIPT = pathlib.Path(__file__).with_name("verify-beta-promotion.py")
SPEC = importlib.util.spec_from_file_location("verify_beta_promotion", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class BetaPromotionTests(unittest.TestCase):
    version = "1.2.3"
    commit = "a" * 40

    def fixture(self) -> tuple[pathlib.Path, pathlib.Path, pathlib.Path, dict]:
        root = pathlib.Path(self.temp.name)
        packages = root / "packages"
        artifacts = root / "artifacts"
        packages.mkdir()
        artifacts.mkdir()
        package_records = []
        for architecture in sorted(MODULE.ARCHITECTURES):
            for name in sorted(MODULE.PACKAGES):
                filename = f"{name}_{self.version}_{architecture}.deb"
                data = f"{name}/{architecture}".encode()
                (packages / filename).write_bytes(data)
                package_records.append({
                    "name": name,
                    "architecture": architecture,
                    "version": self.version,
                    "filename": f"pool/main/c/{name}/{filename}",
                    "sha256": hashlib.sha256(data).hexdigest(),
                    "size": len(data),
                })
        artifact_names = set(MODULE.ARTIFACTS) | {f"cloudless-{self.version}.spdx.json"}
        artifact_records = []
        prefix = f"artifacts/{self.version}/beta/"
        for name in sorted(artifact_names):
            data = f"payload/{name}".encode()
            signature = f"signature/{name}".encode()
            (artifacts / name).write_bytes(data)
            (artifacts / f"{name}.asc").write_bytes(signature)
            artifact_records.append({
                "name": name,
                "path": prefix + name,
                "sha256": hashlib.sha256(data).hexdigest(),
                "size": len(data),
                "signature": prefix + name + ".asc",
                "signatureSha256": hashlib.sha256(signature).hexdigest(),
                "signatureSize": len(signature),
            })
        physical = {
            "schema": "cloudless.physical-release.v1",
            "status": "not-qualified",
            "required": False,
            "version": self.version,
            "channel": "beta",
            "sourceCommit": self.commit,
        }
        validation = {
            "schema": "cloudless.release-gates.v1",
            "version": self.version,
            "channel": "beta",
            "sourceCommit": self.commit,
            "matrixSha256": "b" * 64,
            "passedGates": sorted(MODULE.REQUIRED_GATES),
            "targets": [
                {"platform": platform, "architecture": architecture}
                for platform, architecture in sorted(MODULE.REQUIRED_TARGETS)
            ],
            "physicalQualification": physical,
            "securityReadiness": {
                "schema": "cloudless.security-readiness.v1",
                "status": "not-operational",
                "required": False,
                "version": self.version,
                "channel": "beta",
                "sourceCommit": self.commit,
            },
            "ciQualification": {
                "schema": "cloudless.ci-qualification.v1",
                "status": "not-qualified",
                "required": False,
                "version": self.version,
                "channel": "beta",
                "sourceCommit": self.commit,
            },
        }
        release = {
            "schema": "cloudless.release.v2",
            "version": self.version,
            "channel": "beta",
            "sourceCommit": self.commit,
            "publishedAt": "2026-01-01T00:00:00Z",
            "validation": validation,
            "compatibility": {"matrixSha256": "b" * 64, "targets": validation["targets"]},
            "physicalQualification": physical,
            "securityReadiness": validation["securityReadiness"],
            "ciQualification": validation["ciQualification"],
            "packages": package_records,
            "artifacts": artifact_records,
        }
        manifest = root / "cloudless-release.json"
        manifest.write_text(json.dumps(release), encoding="utf-8")
        return manifest, packages, artifacts, release

    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()

    def tearDown(self) -> None:
        self.temp.cleanup()

    def args(self, manifest: pathlib.Path, packages: pathlib.Path, artifacts: pathlib.Path) -> types.SimpleNamespace:
        return types.SimpleNamespace(
            manifest=str(manifest),
            packages=str(packages),
            artifacts=str(artifacts),
            version=self.version,
            source_commit=self.commit,
            minimum_age_seconds=604800,
            publicly_available_at="",
            now="2026-01-08T00:00:00Z",
        )

    def test_accepts_complete_soaked_generation(self) -> None:
        manifest, packages, artifacts, _ = self.fixture()
        result = MODULE.verify(self.args(manifest, packages, artifacts))
        self.assertEqual(result["schema"], "cloudless.beta-promotion.v1")
        self.assertEqual(len(result["packages"]), 12)
        self.assertEqual(len(result["artifacts"]), 9)

    def test_rejects_incomplete_soak(self) -> None:
        manifest, packages, artifacts, _ = self.fixture()
        args = self.args(manifest, packages, artifacts)
        args.now = "2026-01-07T23:59:59Z"
        with self.assertRaisesRegex(ValueError, "soak is incomplete"):
            MODULE.verify(args)

    def test_soak_starts_when_beta_becomes_public(self) -> None:
        manifest, packages, artifacts, _ = self.fixture()
        args = self.args(manifest, packages, artifacts)
        args.publicly_available_at = "2026-01-07T12:00:00Z"
        args.now = "2026-01-08T00:00:00Z"
        with self.assertRaisesRegex(ValueError, "soak is incomplete"):
            MODULE.verify(args)

    def test_rejects_tampered_package(self) -> None:
        manifest, packages, artifacts, release = self.fixture()
        filename = pathlib.PurePosixPath(release["packages"][0]["filename"]).name
        (packages / filename).write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "integrity mismatch"):
            MODULE.verify(self.args(manifest, packages, artifacts))

    def test_rejects_channel_ambiguous_artifact(self) -> None:
        manifest, packages, artifacts, release = self.fixture()
        release["artifacts"][0]["path"] = f"artifacts/{self.version}/{release['artifacts'][0]['name']}"
        manifest.write_text(json.dumps(release), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "not channel scoped"):
            MODULE.verify(self.args(manifest, packages, artifacts))

    def test_rejects_incomplete_beta_gate_set(self) -> None:
        manifest, packages, artifacts, release = self.fixture()
        release["validation"]["passedGates"].remove("atomic-repository")
        manifest.write_text(json.dumps(release), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "complete production gate set"):
            MODULE.verify(self.args(manifest, packages, artifacts))


if __name__ == "__main__":
    unittest.main()
