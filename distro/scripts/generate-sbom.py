#!/usr/bin/env python3
"""Generate a deterministic SPDX 2.3 inventory for one CloudlessOS release."""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
from datetime import datetime, timezone


def checksum(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def safe_id(value: str) -> str:
    return "SPDXRef-" + re.sub(r"[^A-Za-z0-9.-]+", "-", value).strip("-")


def created_at(root: pathlib.Path, commit: str) -> str:
    configured = os.environ.get("CLOUDLESS_SBOM_CREATED", "").strip()
    if configured:
        return configured
    epoch = os.environ.get("SOURCE_DATE_EPOCH", "").strip()
    if epoch:
        return datetime.fromtimestamp(int(epoch), timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    try:
        value = subprocess.check_output(
            ["git", "-C", str(root), "show", "-s", "--format=%cI", commit],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc).replace(
            microsecond=0
        ).isoformat().replace("+00:00", "Z")
    except (OSError, subprocess.CalledProcessError, ValueError):
        # Synthetic release tests deliberately use a commit that is not in the
        # local object database. A fixed value keeps their artifact repeatable.
        return "1970-01-01T00:00:00Z"


def main() -> int:
    if len(sys.argv) != 3:
        raise SystemExit("Usage: generate-sbom.py VERSION OUTPUT")
    version, output_arg = sys.argv[1:]
    root = pathlib.Path(__file__).resolve().parents[2]
    package_dir = pathlib.Path(os.environ.get("CLOUDLESS_SBOM_PACKAGE_DIR", root / "distro/out/packages"))
    go_sum = pathlib.Path(os.environ.get("CLOUDLESS_SBOM_GO_SUM", root / "orchestrator/go.sum"))
    output = pathlib.Path(output_arg)
    commit = os.environ.get("CLOUDLESS_SOURCE_COMMIT", "")
    if not commit:
        commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()

    packages: list[dict] = []
    relationships: list[dict] = []
    for deb in sorted(package_dir.glob(f"*_{version}_*.deb")):
        match = re.fullmatch(r"(.+)_([^_]+)_([^_]+)\.deb", deb.name)
        if not match:
            continue
        name, package_version, architecture = match.groups()
        spdx_id = safe_id(f"deb-{name}-{architecture}")
        packages.append(
            {
                "SPDXID": spdx_id,
                "name": name,
                "versionInfo": package_version,
                "downloadLocation": "NOASSERTION",
                "filesAnalyzed": False,
                "checksums": [{"algorithm": "SHA256", "checksumValue": checksum(deb)}],
                "externalRefs": [
                    {
                        "referenceCategory": "PACKAGE-MANAGER",
                        "referenceType": "purl",
                        "referenceLocator": f"pkg:deb/cloudless/{name}@{package_version}?arch={architecture}",
                    }
                ],
                "supplier": "Organization: Cloudless",
            }
        )
        relationships.append(
            {"spdxElementId": "SPDXRef-DOCUMENT", "relationshipType": "DESCRIBES", "relatedSpdxElement": spdx_id}
        )

    modules: dict[tuple[str, str], None] = {}
    if go_sum.is_file():
        for line in go_sum.read_text(encoding="utf-8").splitlines():
            fields = line.split()
            if len(fields) < 2 or fields[1].endswith("/go.mod"):
                continue
            modules[(fields[0], fields[1])] = None
    for module, module_version in sorted(modules):
        spdx_id = safe_id(f"go-{module}-{module_version}")
        packages.append(
            {
                "SPDXID": spdx_id,
                "name": module,
                "versionInfo": module_version,
                "downloadLocation": "NOASSERTION",
                "filesAnalyzed": False,
                "externalRefs": [
                    {
                        "referenceCategory": "PACKAGE-MANAGER",
                        "referenceType": "purl",
                        "referenceLocator": f"pkg:golang/{module}@{module_version}",
                    }
                ],
                "supplier": "NOASSERTION",
            }
        )
        relationships.append(
            {"spdxElementId": "SPDXRef-cloudlessd", "relationshipType": "DEPENDS_ON", "relatedSpdxElement": spdx_id}
        )

    packages.append(
        {
            "SPDXID": "SPDXRef-cloudlessd",
            "name": "cloudlessd",
            "versionInfo": version,
            "downloadLocation": "NOASSERTION",
            "filesAnalyzed": False,
            "supplier": "Organization: Cloudless",
        }
    )
    relationships.append(
        {"spdxElementId": "SPDXRef-DOCUMENT", "relationshipType": "DESCRIBES", "relatedSpdxElement": "SPDXRef-cloudlessd"}
    )
    expected_cloudless = {
        (name, architecture)
        for architecture in ("amd64", "arm64")
        for name in (
            "cloudless-orchestrator",
            "cloudless-shell",
            "cloudless-branding",
            "cloudless-hardware",
            "cloudless-firstboot",
            "cloudless-updater",
        )
    }
    actual_cloudless = {
        (
            item["name"],
            item["externalRefs"][0]["referenceLocator"].split("arch=", 1)[1],
        )
        for item in packages
        if item["name"].startswith("cloudless-")
    }
    if actual_cloudless != expected_cloudless:
        missing = sorted(expected_cloudless - actual_cloudless)
        extra = sorted(actual_cloudless - expected_cloudless)
        raise SystemExit(f"incomplete CloudlessOS package matrix; missing={missing}, extra={extra}")

    document = {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"CloudlessOS-{version}",
        "documentNamespace": f"https://updates.becloudless.ai/sbom/{version}/{commit}",
        "creationInfo": {
            "created": created_at(root, commit),
            "creators": ["Tool: cloudless-generate-sbom/1", "Organization: Cloudless"],
        },
        "documentDescribes": [item["SPDXID"] for item in packages if item["name"].startswith("cloudless-")],
        "packages": packages,
        "relationships": relationships,
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_suffix(output.suffix + ".tmp")
    temporary.write_text(json.dumps(document, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
    os.replace(temporary, output)
    print(f"SPDX SBOM ready: {output} ({len(packages)} packages/modules)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
