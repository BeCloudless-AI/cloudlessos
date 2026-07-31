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

    def boot_health(self, boot_id, *, healthy=True, display_expected=True, probes=None):
        path = self.root / f"boot-health-{boot_id}.json"
        ready_probes = {
            "defaultTarget": "ready",
            "lightdm": "ready",
            "xDisplay": "ready",
            "graphicalSession": "ready",
            "kioskBrowser": "ready",
            "browserProfile": "ready",
            "orchestrator": "ready",
        }
        if probes:
            ready_probes.update(probes)
        path.write_text(
            json.dumps(
                {
                    "schema": "cloudless.boot-health.v1",
                    "healthy": healthy,
                    "consecutiveHealthyBoots": 1,
                    "bootId": boot_id,
                    "checkedAt": "2026-07-31T00:00:00Z",
                    "elapsedSeconds": 2,
                    "attempts": 2,
                    "platform": "generic",
                    "displayExpected": display_expected,
                    "reason": "ready" if healthy else "lightdm-not-active",
                    "probes": ready_probes,
                }
            ),
            encoding="utf-8",
        )
        return path

    def record_healthy_boot(self, campaign, boot_id):
        return qualify.record_boot(
            campaign,
            boot_id,
            boot_health_path=self.boot_health(boot_id),
        )

    def test_complete_campaign_and_tamper_detection(self):
        campaign = self.begin()
        for number in range(10):
            self.record_healthy_boot(campaign, str(uuid.UUID(int=number + 1)))
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

    def test_completed_campaign_exports_as_self_verifying_archive(self):
        campaign = self.begin()
        for number in range(10):
            self.record_healthy_boot(campaign, str(uuid.UUID(int=number + 1)))
        self.record_all(campaign, "virtualbox-amd64")
        qualify.activate_campaign(campaign, self.root)
        archive = self.root / "qualified.zip"
        with mock.patch.dict(
            qualify.os.environ, {"CLOUDLESS_QUALIFICATION_ROOT": str(self.root)}, clear=False
        ):
            self.assertEqual(qualify.export_campaign(campaign, self.matrix, archive), archive)
        self.assertFalse((self.root / "active-campaign.json").exists())
        manifest = qualify.verify_export(archive)
        self.assertEqual(manifest["schema"], qualify.EXPORT_SCHEMA)
        self.assertEqual(manifest["target"], "virtualbox-amd64")
        self.assertEqual(manifest["version"], "1.2.3-rc1")
        self.assertIn("qualification-result.json", manifest["records"])
        with zipfile.ZipFile(archive) as exported:
            self.assertEqual(set(exported.namelist()), set(manifest["records"]) | {"qualification-export.json"})

    def test_export_rejects_incomplete_campaign_and_campaign_local_output(self):
        campaign = self.begin()
        with self.assertRaisesRegex(ValueError, "incomplete"):
            qualify.export_campaign(campaign, self.matrix, self.root / "incomplete.zip")
        for number in range(10):
            self.record_healthy_boot(campaign, str(uuid.UUID(int=number + 1)))
        self.record_all(campaign, "virtualbox-amd64")
        with self.assertRaisesRegex(ValueError, "outside"):
            qualify.export_campaign(campaign, self.matrix, campaign / "unsafe.zip")

    def test_export_verification_detects_member_tampering(self):
        campaign = self.begin()
        for number in range(10):
            self.record_healthy_boot(campaign, str(uuid.UUID(int=number + 1)))
        self.record_all(campaign, "virtualbox-amd64")
        archive = qualify.export_campaign(campaign, self.matrix, self.root / "qualified.zip")
        tampered = self.root / "tampered.zip"
        with zipfile.ZipFile(archive) as source, zipfile.ZipFile(tampered, "w") as target:
            for name in source.namelist():
                payload = source.read(name)
                if name == "campaign.json":
                    payload += b"\n"
                target.writestr(name, payload)
        with self.assertRaisesRegex(ValueError, "integrity verification"):
            qualify.verify_export(tampered)

    def qualification_set_fixtures(self):
        matrix = qualify.load_matrix(self.matrix)
        archives = []
        manifests = {}
        for target in matrix["targets"]:
            archive = self.root / f"{target['id']}.zip"
            archive.write_bytes(f"sealed {target['id']}\n".encode())
            archives.append(archive)
            manifests[archive] = {
                "schema": qualify.EXPORT_SCHEMA,
                "target": target["id"],
                "version": "1.2.3-rc1",
                "sourceCommit": "0123456789abcdef0123456789abcdef01234567",
                "exportedAt": "2026-07-31T00:00:00Z",
            }
        return archives, manifests

    def test_complete_export_set_binds_every_target_to_one_candidate(self):
        archives, manifests = self.qualification_set_fixtures()
        with mock.patch.object(qualify, "verify_export", side_effect=lambda path: manifests[path]):
            result = qualify.verify_export_set(archives, self.matrix)
        expected = [target["id"] for target in qualify.load_matrix(self.matrix)["targets"]]
        self.assertEqual(result["schema"], qualify.SET_SCHEMA)
        self.assertEqual(result["targets"], expected)
        self.assertEqual([record["target"] for record in result["archives"]], expected)
        self.assertEqual(result["version"], "1.2.3-rc1")
        self.assertEqual(result["sourceCommit"], "0123456789abcdef0123456789abcdef01234567")

    def test_export_set_rejects_missing_duplicate_and_mixed_candidate_evidence(self):
        archives, manifests = self.qualification_set_fixtures()
        with mock.patch.object(qualify, "verify_export", side_effect=lambda path: manifests[path]):
            with self.assertRaisesRegex(ValueError, "missing targets"):
                qualify.verify_export_set(archives[:-1], self.matrix)

            duplicate = self.root / "duplicate.zip"
            duplicate.write_bytes(b"duplicate target\n")
            manifests[duplicate] = dict(manifests[archives[0]])
            with self.assertRaisesRegex(ValueError, "duplicate target"):
                qualify.verify_export_set(archives + [duplicate], self.matrix)

            manifests[archives[-1]] = dict(manifests[archives[-1]], sourceCommit="f" * 40)
            with self.assertRaisesRegex(ValueError, "one exact version and source commit"):
                qualify.verify_export_set(archives, self.matrix)

            manifests[archives[-1]] = dict(
                manifests[archives[0]], target=manifests[archives[-1]]["target"], version="1.2.4-rc1"
            )
            with self.assertRaisesRegex(ValueError, "one exact version and source commit"):
                qualify.verify_export_set(archives, self.matrix)

    def test_unique_boots_and_cluster_checks_cannot_be_bypassed(self):
        campaign = self.begin("dgx-spark-arm64-2", "aarch64", "dgx-spark")
        boot = str(uuid.UUID(int=1))
        health = self.boot_health(boot)
        self.assertTrue(qualify.record_boot(campaign, boot, boot_health_path=health))
        self.assertFalse(qualify.record_boot(campaign, boot, boot_health_path=health))
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

    def test_boot_count_requires_same_boot_complete_graphical_audit(self):
        campaign = self.begin()
        boot = str(uuid.UUID(int=17))
        with self.assertRaisesRegex(ValueError, "has not completed"):
            qualify.record_boot(campaign, boot, boot_health_path=self.root / "missing.json")

        different = str(uuid.UUID(int=18))
        with self.assertRaisesRegex(ValueError, "different kernel boot"):
            qualify.record_boot(campaign, boot, boot_health_path=self.boot_health(different))

        with self.assertRaisesRegex(ValueError, "not graphically healthy"):
            qualify.record_boot(campaign, boot, boot_health_path=self.boot_health(boot, healthy=False))

        with self.assertRaisesRegex(ValueError, "did not require a display"):
            qualify.record_boot(
                campaign,
                boot,
                boot_health_path=self.boot_health(boot, display_expected=False),
            )

        with self.assertRaisesRegex(ValueError, "complete desktop ownership chain"):
            qualify.record_boot(
                campaign,
                boot,
                boot_health_path=self.boot_health(boot, probes={"kioskBrowser": "waiting"}),
            )
        self.assertEqual(list((campaign / "boots").iterdir()), [])

    def test_active_campaign_records_only_exact_verified_installed_boot(self):
        campaign = self.begin()
        pointer = qualify.activate_campaign(campaign, self.root)
        self.assertTrue(pointer.is_file())
        status = self.root / "status.json"
        status.write_text(
            json.dumps(
                {
                    "currentVersion": "1.2.3-rc1",
                    "currentSourceCommit": "0123456789abcdef0123456789abcdef01234567",
                }
            ),
            encoding="utf-8",
        )
        boot_id = str(uuid.UUID(int=88))
        with mock.patch.object(qualify, "current_boot_id", return_value=boot_id):
            active, created = qualify.record_active_boot(
                self.root,
                self.matrix,
                status,
                self.boot_health(boot_id),
                machine_arch="x86_64",
                machine_kind="virtualbox",
            )
            self.assertEqual(active, campaign.resolve())
            self.assertTrue(created)
            _, created = qualify.record_active_boot(
                self.root,
                self.matrix,
                status,
                self.boot_health(boot_id),
                machine_arch="x86_64",
                machine_kind="virtualbox",
            )
            self.assertFalse(created)

        status.write_text(
            json.dumps(
                {
                    "currentVersion": "1.2.3-rc1",
                    "currentSourceCommit": "89abcdef0123456789abcdef0123456789abcdef",
                }
            ),
            encoding="utf-8",
        )
        with self.assertRaisesRegex(ValueError, "source commit does not match"):
            qualify.record_active_boot(
                self.root,
                self.matrix,
                status,
                self.boot_health(str(uuid.UUID(int=89))),
                machine_arch="x86_64",
                machine_kind="virtualbox",
            )

    def test_active_campaign_pointer_detects_substitution(self):
        campaign = self.begin()
        qualify.activate_campaign(campaign, self.root)
        manifest = json.loads((campaign / "campaign.json").read_text(encoding="utf-8"))
        manifest["operator"] = "Substituted operator"
        (campaign / "campaign.json").write_text(json.dumps(manifest), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "identity has changed"):
            qualify.active_campaign(self.root)

    def test_embedded_graphical_boot_audit_is_revalidated(self):
        campaign = self.begin()
        boot = str(uuid.UUID(int=19))
        self.record_healthy_boot(campaign, boot)
        record_path = campaign / "boots" / f"{boot}.json"
        record = json.loads(record_path.read_text(encoding="utf-8"))
        self.assertEqual(record["schema"], "cloudless.physical-boot.v2")
        self.assertEqual(record["graphicalBootAudit"]["probes"]["lightdm"], "ready")
        record["graphicalBootAudit"]["probes"]["lightdm"] = "waiting"
        record_path.write_text(json.dumps(record), encoding="utf-8")
        errors = qualify.validate_campaign(campaign, self.matrix)
        self.assertTrue(any("invalid graphical audit" in error for error in errors))

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
            self.record_healthy_boot(campaign, str(uuid.UUID(int=number + 1)))
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

    def test_evidence_symlink_cannot_bypass_regular_file_boundary(self):
        campaign = self.begin()
        linked = self.root / "linked-evidence.txt"
        linked.symlink_to(self.evidence)
        with self.assertRaisesRegex(ValueError, "non-symlink"):
            qualify.record_check(
                campaign, self.matrix, "clean-install", "pass", "Install observed", [linked]
            )

    def test_interrupted_evidence_copy_is_not_published(self):
        campaign = self.begin()
        with mock.patch.object(qualify.shutil, "copyfileobj", side_effect=OSError("copy interrupted")):
            with self.assertRaisesRegex(OSError, "copy interrupted"):
                qualify.record_check(
                    campaign,
                    self.matrix,
                    "clean-install",
                    "pass",
                    "Install observed",
                    [self.evidence],
                )
        evidence_dir = campaign / "evidence" / "clean-install"
        self.assertEqual([], list(evidence_dir.iterdir()))

    def test_evidence_destination_symlink_is_rejected(self):
        campaign = self.begin()
        digest = qualify.sha256_file(self.evidence)
        evidence_dir = campaign / "evidence" / "clean-install"
        evidence_dir.mkdir(parents=True)
        destination = evidence_dir / f"{digest[:12]}-{self.evidence.name}"
        destination.symlink_to(self.root / "outside.txt")
        with self.assertRaisesRegex(ValueError, "destination is not a regular file"):
            qualify.record_check(
                campaign,
                self.matrix,
                "clean-install",
                "pass",
                "Install observed",
                [self.evidence],
            )

    def test_recorded_evidence_cannot_be_replaced_by_an_in_campaign_symlink(self):
        campaign = self.begin()
        qualify.record_check(
            campaign, self.matrix, "clean-install", "pass", "Install observed", [self.evidence]
        )
        result = qualify.load_json(campaign / "checks" / "clean-install.json")
        destination = campaign / result["evidence"][0]["path"]
        substitute = campaign / "evidence" / "substitute.txt"
        shutil.copyfile(destination, substitute)
        destination.unlink()
        destination.symlink_to(substitute)
        errors = qualify.validate_campaign(campaign, self.matrix)
        self.assertIn("clean-install: evidence file must not be a symlink", errors)

    def test_check_result_cannot_be_replaced_by_a_symlink(self):
        campaign = self.begin()
        qualify.record_check(
            campaign, self.matrix, "clean-install", "pass", "Install observed", [self.evidence]
        )
        result = campaign / "checks" / "clean-install.json"
        substitute = campaign / "checks" / "clean-install-original.json"
        result.rename(substitute)
        result.symlink_to(substitute)
        errors = qualify.validate_campaign(campaign, self.matrix)
        self.assertIn("clean-install: result must not be a symlink", errors)

    @staticmethod
    def soak_sample(index, *, failed=False):
        return {
            "capturedAt": f"2026-07-31T00:00:0{index}Z",
            "bootId": "00000000-0000-0000-0000-000000000001",
            "errors": ([{"probe": "engine", "kind": "TimeoutError"}] if failed else []),
            "health": {"status": "ok", "dockerOK": True},
            "locale": {"timezone": "Asia/Dubai", "utcOffset": "+04:00", "locale": "en_US.UTF-8", "country": "AE"},
            "input": {"physicalKeyboard": False, "physicalMouse": False, "physicalPointer": False},
            "display": {"available": True, "width": 3840 if index else 1920, "height": 2160 if index else 1080},
            "engine": {"active": "vllm", "ready": index > 0, "phase": "loading" if index == 0 else ""},
            "apps": [{"name": "cloudless-hermes", "state": "running"}],
            "cluster": {"configured": False, "healthy": False, "nodeCount": 0},
            "browser": {"available": True, "running": index % 2 == 0, "minimized": False},
            "terminal": {"ready": True},
        }

    def test_physical_soak_is_resumable_and_records_transition_summary(self):
        campaign = self.begin()
        calls = []

        def interrupted(_):
            calls.append(len(calls))
            if len(calls) == 2:
                raise RuntimeError("simulated interruption")
            return self.soak_sample(0)

        with self.assertRaisesRegex(RuntimeError, "simulated interruption"):
            qualify.run_physical_soak(
                campaign,
                self.matrix,
                2,
                1,
                sample_provider=interrupted,
                sleeper=lambda _: None,
            )
        state = campaign / "evidence" / ".physical-soak-state.json"
        self.assertTrue(state.is_file())
        self.assertTrue(any("interrupted endurance" in error for error in qualify.validate_campaign(campaign, self.matrix)))

        remaining = iter([self.soak_sample(1), self.soak_sample(2, failed=True)])
        evidence = qualify.run_physical_soak(
            campaign,
            self.matrix,
            2,
            1,
            sample_provider=lambda _: next(remaining),
            sleeper=lambda _: None,
        )
        self.assertFalse(state.exists())
        payload = json.loads(evidence.read_text(encoding="utf-8"))
        self.assertEqual(payload["schema"], qualify.SOAK_SCHEMA)
        self.assertEqual(payload["summary"]["samples"], 3)
        self.assertEqual(payload["summary"]["failedSamples"], 1)
        self.assertEqual(payload["summary"]["transitions"]["engine"], 1)
        self.assertEqual(payload["summary"]["transitions"]["browser"], 2)
        self.assertEqual(payload["summary"]["transitions"]["display"], 1)

    def test_physical_soak_rejects_remote_api_and_secret_bearing_samples(self):
        campaign = self.begin()
        with self.assertRaisesRegex(ValueError, "loopback"):
            qualify.run_physical_soak(
                campaign, self.matrix, 1, 1, "https://example.com", sleeper=lambda _: None
            )
        with self.assertRaisesRegex(ValueError, "credential"):
            qualify.run_physical_soak(
                campaign,
                self.matrix,
                1,
                1,
                sample_provider=lambda _: {"capturedAt": "now", "errors": [], "note": "password=secret-value"},
                sleeper=lambda _: None,
            )

    def test_physical_soak_rejects_concurrent_runner_and_lock_symlink(self):
        campaign = self.begin()
        with qualify.physical_soak_lock(campaign):
            with self.assertRaisesRegex(ValueError, "already running"):
                qualify.run_physical_soak(
                    campaign,
                    self.matrix,
                    1,
                    1,
                    sample_provider=lambda _: self.soak_sample(0),
                    sleeper=lambda _: None,
                )
        lock = campaign / ".physical-soak.lock"
        lock.unlink()
        lock.symlink_to(self.evidence)
        with self.assertRaisesRegex(ValueError, "non-symlink"):
            qualify.run_physical_soak(
                campaign,
                self.matrix,
                1,
                1,
                sample_provider=lambda _: self.soak_sample(0),
                sleeper=lambda _: None,
            )

    def test_physical_soak_sample_omits_sensitive_and_unbounded_api_fields(self):
        responses = {
            "/api/health": {"status": "ok", "dockerOK": True, "diagnostic": "private-log"},
            "/api/system": {"system": {"hostname": "private-host"}, "locale": {"timezone": "Asia/Dubai", "country": "AE"}},
            "/api/system/input": {"keyboard": False, "mouse": True, "keyboardNames": ["Private Keyboard"]},
            "/api/system/display": {"available": True, "output": "private-output-name", "width": 3840, "height": 2160, "error": "private-display-error"},
            "/api/engine": {"active": "vllm", "ready": True, "startup": {"update": {"phase": "ready", "message": "private-model-path"}}},
            "/api/apps": [{"name": "cloudless-hermes", "state": "running", "image": "private-registry/image"}],
            "/api/system/browser": {"available": True, "running": True, "downloadsPath": "/home/private/Downloads"},
            "/api/system/spark-cluster": {"configured": True, "healthy": True, "nodeCount": 2, "peerHost": "private-ip", "username": "private-user"},
        }
        with mock.patch.object(qualify, "current_boot_id", return_value="boot-1"), mock.patch.object(
            qualify, "soak_http_json", side_effect=lambda _base, path, _timeout: responses[path]
        ), mock.patch.object(qualify.socket, "create_connection", return_value=mock.MagicMock()):
            sample = qualify.collect_soak_sample("http://127.0.0.1:8765")
        rendered = json.dumps(sample, sort_keys=True)
        for private in (
            "private-log", "private-host", "Private Keyboard", "private-output-name",
            "private-display-error", "private-model-path", "private-registry", "private/Downloads",
            "private-ip", "private-user",
        ):
            self.assertNotIn(private, rendered)
        self.assertEqual(sample["display"]["width"], 3840)
        self.assertEqual(sample["locale"]["timezone"], "Asia/Dubai")
        self.assertTrue(sample["input"]["physicalMouse"])

    def test_update_rollback_rehearsal_records_verified_continuity(self):
        campaign = self.begin()
        qualify.activate_campaign(campaign, self.root)
        updater = self.root / "cloudless-updater"
        updater.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        updater.chmod(0o700)
        commit = "0123456789abcdef0123456789abcdef01234567"

        def snapshot(version, source_commit, available, phase, *, state="available", error=""):
            public = {
                "capturedAt": "2026-07-31T00:00:00Z",
                "bootId": "00000000-0000-0000-0000-000000000001",
                "engine": {"active": "vllm", "ready": True, "unloaded": False},
                "operations": [
                    {
                        "idHash": "a" * 24,
                        "kind": "run",
                        "phase": phase,
                    }
                ],
                "update": {
                    "state": state,
                    "currentVersion": version,
                    "currentSourceCommit": source_commit,
                    "availableVersion": available,
                    "availableSourceCommit": commit if available else "",
                    "progress": 100,
                    "rebootRequired": False,
                },
            }
            return public, {"state": state, "error": error}

        baseline = snapshot("1.2.2", "1" * 40, "1.2.3-rc1", "preparing")
        rollback = snapshot(
            "1.2.2",
            "1" * 40,
            "1.2.3-rc1",
            "recovering",
            state="failed",
            error="qualification requested a deliberate rollback after the candidate continuity probe passed",
        )
        upgrade = snapshot("1.2.3-rc1", commit, "", "active", state="updated")
        responses = iter([baseline, baseline, rollback, rollback, upgrade])
        commands = []

        def runner(command):
            commands.append(command)
            return 1 if command[-1] == "qualification-rollback" else 0

        with mock.patch.object(
            qualify, "update_rehearsal_snapshot", side_effect=lambda _: next(responses)
        ):
            evidence = qualify.run_update_rollback_rehearsal(
                campaign,
                self.matrix,
                updater=updater,
                runner=runner,
                reporter=lambda _: None,
                qualification_root_path=self.root,
            )
        self.assertEqual(
            commands,
            [[str(updater), "qualification-rollback"], [str(updater), "apply"]],
        )
        self.assertTrue(evidence.is_file())
        self.assertFalse((campaign / "evidence" / ".update-rollback-state.json").exists())
        result = qualify.load_json(campaign / "checks" / "update-and-rollback.json")
        self.assertEqual("pass", result["status"])

    def test_update_rollback_rehearsal_rejects_wrong_candidate_identity(self):
        campaign = self.begin()
        qualify.activate_campaign(campaign, self.root)
        updater = self.root / "cloudless-updater"
        updater.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        updater.chmod(0o700)
        baseline = {
            "capturedAt": "2026-07-31T00:00:00Z",
            "bootId": "00000000-0000-0000-0000-000000000001",
            "engine": {"active": "vllm", "ready": True, "unloaded": False},
            "operations": [{"idHash": "a" * 24, "kind": "run", "phase": "preparing"}],
            "update": {
                "state": "available",
                "currentVersion": "1.2.2",
                "currentSourceCommit": "1" * 40,
                "availableVersion": "1.2.3-rc1",
                "availableSourceCommit": "f" * 40,
                "progress": 100,
                "rebootRequired": False,
            },
        }
        with mock.patch.object(qualify, "update_rehearsal_snapshot", return_value=(baseline, {})):
            with self.assertRaisesRegex(ValueError, "source commit"):
                qualify.run_update_rollback_rehearsal(
                    campaign,
                    self.matrix,
                    updater=updater,
                    runner=lambda _: self.fail("updater must not run"),
                    reporter=lambda _: None,
                    qualification_root_path=self.root,
                )

    def test_interrupted_update_rehearsal_prevents_campaign_sealing(self):
        campaign = self.begin()
        checkpoint = campaign / "evidence" / ".update-rollback-state.json"
        checkpoint.write_text(
            json.dumps({"schema": qualify.UPDATE_REHEARSAL_SCHEMA, "stage": "rollback-started"}),
            encoding="utf-8",
        )
        errors = qualify.validate_campaign(campaign, self.matrix)
        self.assertIn(
            "update-and-rollback: an interrupted rehearsal must be resumed or discarded",
            errors,
        )


if __name__ == "__main__":
    unittest.main()
