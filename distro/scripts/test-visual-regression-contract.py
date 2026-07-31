#!/usr/bin/env python3
import json
from pathlib import Path

root = Path(__file__).resolve().parents[2]
baseline_path = root / "scripts" / "visual-baseline.json"
workflow_path = root / ".github" / "workflows" / "multiarch.yml"
script_path = root / "scripts" / "visual-regression.ps1"

baseline = json.loads(baseline_path.read_text(encoding="utf-8-sig"))
if baseline.get("schema") != 1 or baseline.get("sample_size") != 32:
    raise SystemExit("visual baseline schema/sample size is unsupported")
threshold = baseline.get("max_mean_absolute_error")
if not isinstance(threshold, (int, float)) or threshold <= 0 or threshold > 3:
    raise SystemExit("visual baseline threshold must be positive and no greater than 3")

expected = {
    "desktop-720p.png",
    "desktop-1080p.png",
    "desktop-1440p.png",
    "settings.png",
    "model-manager.png",
    "model-manager-compact.png",
    "model-detail.png",
    "app-launcher.png",
    "assistant.png",
    "model-startup.png",
    "api-access.png",
    "model-launch.png",
    "inference-tabs.png",
    "power-dialog.png",
    "update-center.png",
    "recipe-library.png",
    "cluster-wizard.png",
}
captures = baseline.get("captures", [])
by_name = {item.get("file"): item for item in captures}
if set(by_name) != expected:
    raise SystemExit(
        f"visual baseline surface set differs: missing={sorted(expected-set(by_name))}, "
        f"extra={sorted(set(by_name)-expected)}"
    )
for name, capture in by_name.items():
    signature = capture.get("signature")
    if capture.get("width", 0) <= 0 or capture.get("height", 0) <= 0:
        raise SystemExit(f"{name} has invalid dimensions")
    if not isinstance(signature, list) or len(signature) != 32 * 32:
        raise SystemExit(f"{name} has an incomplete visual signature")
    if any(not isinstance(value, int) or value < 0 or value > 255 for value in signature):
        raise SystemExit(f"{name} has an invalid luminance sample")

workflow = workflow_path.read_text(encoding="utf-8")
if "run: ./scripts/visual-regression.ps1\n" not in workflow:
    raise SystemExit("CI must run the full visual-regression matrix without -Quick")
for required in ("name: visual-regression", "path: artifacts/visual-regression", "if-no-files-found: error"):
    if required not in workflow:
        raise SystemExit(f"visual artifact retention is missing {required!r}")

script = script_path.read_text(encoding="utf-8-sig")
for required in (
    "Get-VisualSignature",
    "max_mean_absolute_error",
    "mean_absolute_error",
    "Visual regression detected",
    "UpdateBaseline",
):
    if required not in script:
        raise SystemExit(f"visual comparison implementation is missing {required!r}")

print("Visual baseline contract covers 17 full-matrix surfaces with a bounded comparison threshold.")
