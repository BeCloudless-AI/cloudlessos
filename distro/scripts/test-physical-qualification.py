#!/usr/bin/python3
import importlib.machinery
import importlib.util
import json
import shutil
import tempfile
import unittest
from unittest import mock
import uuid
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
QUALIFIER_PATH = ROOT / "distro/packages/cloudless-firstboot/cloudless-qualify"
loader = importlib.machinery.SourceFileLoader("cloudless_qualify", str(QUALIFIER_PATH))
spec = importlib.util.spec_from_loader(loader.name, loader)
qualify = importlib.util.module_from_spec(spec)
loader.exec_module(qualify)


class PhysicalQualificationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.matrix = self.root / "matrix.json"
        shutil.copyfile(ROOT / "distro/release/physical-validation-matrix.json", self.matrix)
        self.evidence = self.root / "proof.txt"
        self.evidence.write_text("Operator-observed qualification evidence\n", encoding="utf-8")

    def tearDown(self):
        self.temp.cleanup()

    def begin(self, target="virtualbox-amd64", architecture="x86_64", kind="virtualbox"):
        campaign = self.root / target
        qualify.begin_campaign(
            self.matrix,
            target,
            "1.2.3-rc1",
            "0123456789abcdef0123456789abcdef01234567",
            campaign,
            "Qualification Operator",
            machine_arch=architecture,
            machine_kind=kind,
        )
        return campaign

    def record_all(self, campaign, target):
        matrix = qualify.load_matrix(self.matrix)
        for check in qualify.applicable_checks(matrix, qualify.target_by_id(matrix, target)):
            if check == "ten-boot-cycles":
                continue
            qualify.record_check(
                campaign,
                self.matrix,
                check,
                "pass",
                f"Observed and retained evidence for {check}",
                [self.evidence],
            )

    def test_complete_campaign_and_tamper_detection(self):
        campaign = self.begin()
        for number in range(10):
            qualify.record_boot(campaign, str(uuid.UUID(int=number + 1)))
        self.record_all(campaign, "virtualbox-amd64")
        self.assertEqual(qualify.validate_campaign(campaign, self.matrix), [])
        self.assertEqual(qualify.print_status(campaign, self.matrix), 0)
        copied = next((campaign / "evidence" / "graphical-session").iterdir())
        copied.write_text("changed after qualification\n", encoding="utf-8")
        self.assertTrue(
            any("evidence digest changed" in error for error in qualify.validate_campaign(campaign, self.matrix))
        )
        self.assertEqual(qualify.print_status(campaign, self.matrix), 1)
        self.assertFalse((campaign / "qualification-result.json").exists())

    def test_unique_boots_and_cluster_checks_cannot_be_bypassed(self):
        campaign = self.begin("dgx-spark-arm64-2", "aarch64", "dgx-spark")
        boot = str(uuid.UUID(int=1))
        self.assertTrue(qualify.record_boot(campaign, boot))
        self.assertFalse(qualify.record_boot(campaign, boot))
        matrix = qualify.load_matrix(self.matrix)
        for check in matrix["requiredChecks"]:
            if check != "ten-boot-cycles":
                qualify.record_check(
                    campaign, self.matrix, check, "pass", f"Evidence for {check}", [self.evidence]
                )
        errors = qualify.validate_campaign(campaign, self.matrix)
        self.assertTrue(any("discover-and-enroll" in error for error in errors))
        self.assertTrue(any("cluster-failure--packet-loss--loading" in error for error in errors))
        self.assertTrue(any("1/10 unique boots" in error for error in errors))

    def test_representative_two_spark_target_expands_failure_phase_matrix(self):
        matrix = qualify.load_matrix(self.matrix)
        two_spark = qualify.target_by_id(matrix, "dgx-spark-arm64-2")
        three_spark = qualify.target_by_id(matrix, "dgx-spark-arm64-3")
        two_checks = qualify.applicable_checks(matrix, two_spark)
        three_checks = qualify.applicable_checks(matrix, three_spark)
        failure_checks = [check for check in two_checks if check.startswith("cluster-failure--")]
        self.assertEqual(len(failure_checks), 7 * 9)
        self.assertIn("cluster-failure--coordinator-loss--optimizing", failure_checks)
        self.assertFalse(any(check.startswith("cluster-failure--") for check in three_checks))

    def test_guided_plan_is_resumable_and_detects_tampering(self):
        campaign = self.begin()
        plan = qualify.qualification_plan(campaign, self.matrix)
        self.assertEqual(plan[0]["check"], "clean-install")
        self.assertEqual(plan[0]["status"], "pending")
        qualify.record_check(
            campaign,
            self.matrix,
            "clean-install",
            "pass",
            "Clean installation reached the graphical desktop",
            [self.evidence],
        )
        plan = qualify.qualification_plan(campaign, self.matrix)
        self.assertEqual(plan[0]["status"], "pass")
        self.assertEqual(plan[1]["check"], "ten-boot-cycles")
        for number in range(10):
            qualify.record_boot(campaign, str(uuid.UUID(int=number + 1)))
        self.assertEqual(qualify.qualification_plan(campaign, self.matrix)[1]["status"], "pass")
        copied = next((campaign / "evidence" / "clean-install").iterdir())
        copied.write_text("tampered\n", encoding="utf-8")
        self.assertEqual(qualify.qualification_plan(campaign, self.matrix)[0]["status"], "attention")

    def test_failure_check_guidance_names_fault_and_phase(self):
        guidance = qualify.check_guidance("cluster-failure--coordinator-loss--optimizing")
        self.assertIn("coordinator-loss", guidance)
        self.assertIn("optimizing", guidance)
        self.assertIn("Restore health", guidance)

    def test_secrets_empty_evidence_and_wrong_machine_fail_closed(self):
        campaign = self.begin()
        with self.assertRaisesRegex(ValueError, "credential"):
            qualify.record_check(
                campaign,
                self.matrix,
                "clean-install",
                "pass",
                "token=github_pat_private",
                [self.evidence],
            )

    def test_begin_can_use_matching_verified_updater_identity(self):
        status = self.root / "status.json"
        status.write_text(
            json.dumps(
                {
                    "currentVersion": "1.2.3-rc1",
                    "currentSourceCommit": "89abcdef0123456789abcdef0123456789abcdef",
                }
            ),
            encoding="utf-8",
        )
        with mock.patch.dict(
            qualify.os.environ, {"CLOUDLESS_UPDATE_STATUS": str(status)}, clear=False
        ):
            campaign = qualify.begin_campaign(
                self.matrix,
                "virtualbox-amd64",
                "1.2.3-rc1",
                None,
                self.root / "inferred",
                "Qualification Operator",
                machine_arch="x86_64",
                machine_kind="virtualbox",
            )
        payload = json.loads((campaign / "campaign.json").read_text(encoding="utf-8"))
        self.assertEqual(
            payload["sourceCommit"], "89abcdef0123456789abcdef0123456789abcdef"
        )

        status.write_text(
            json.dumps(
                {
                    "currentVersion": "9.9.9",
                    "currentSourceCommit": "89abcdef0123456789abcdef0123456789abcdef",
                }
            ),
            encoding="utf-8",
        )
        with mock.patch.dict(
            qualify.os.environ, {"CLOUDLESS_UPDATE_STATUS": str(status)}, clear=False
        ):
            with self.assertRaisesRegex(ValueError, "not 1.2.3-rc1"):
                qualify.begin_campaign(
                    self.matrix,
                    "virtualbox-amd64",
                    "1.2.3-rc1",
                    None,
                    self.root / "rejected",
                    "Qualification Operator",
                    machine_arch="x86_64",
                    machine_kind="virtualbox",
                )
        with self.assertRaisesRegex(ValueError, "evidence"):
            qualify.record_check(
                campaign, self.matrix, "clean-install", "pass", "Install observed", []
            )
        with self.assertRaisesRegex(ValueError, "expects arm64"):
            qualify.begin_campaign(
                self.matrix,
                "dgx-spark-arm64-1",
                "1.2.3",
                "0123456789abcdef0123456789abcdef01234567",
                self.root / "wrong",
                "Operator",
                machine_arch="x86_64",
                machine_kind="dgx-spark",
            )

    def test_matrix_change_invalidates_campaign(self):
        campaign = self.begin()
        matrix = json.loads(self.matrix.read_text(encoding="utf-8"))
        matrix["minimumBootCycles"] = 11
        self.matrix.write_text(json.dumps(matrix), encoding="utf-8")
        errors = qualify.validate_campaign(campaign, self.matrix)
        self.assertIn("campaign matrix digest does not match the authoritative matrix", errors)

    def test_zip_evidence_is_scanned_for_credentials_and_traversal(self):
        campaign = self.begin()
        secret_zip = self.root / "secret.zip"
        with zipfile.ZipFile(secret_zip, "w") as archive:
            archive.writestr("log.txt", "Authorization: Bearer qualification-secret-value")
        with self.assertRaisesRegex(ValueError, "credential"):
            qualify.record_check(
                campaign, self.matrix, "support-bundle", "pass", "Bundle generated", [secret_zip]
            )
        traversal_zip = self.root / "traversal.zip"
        with zipfile.ZipFile(traversal_zip, "w") as archive:
            archive.writestr("../outside.txt", "not allowed")
        with self.assertRaisesRegex(ValueError, "unsafe member"):
            qualify.record_check(
                campaign, self.matrix, "support-bundle", "pass", "Bundle generated", [traversal_zip]
            )

    def test_campaign_subdirectories_cannot_be_replaced_by_symlinks(self):
        campaign = self.begin()
        shutil.rmtree(campaign / "checks")
        (campaign / "checks").symlink_to(self.root, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, "real directory"):
            qualify.record_check(
                campaign, self.matrix, "clean-install", "pass", "Install observed", [self.evidence]
            )


if __name__ == "__main__":
    unittest.main()
