#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
export GNUPGHOME="$work/gnupg"
server_pid=""
trap 'test -z "$server_pid" || kill "$server_pid" 2>/dev/null || true; rm -rf "$work"' EXIT
install -d -m 0700 "$GNUPGHOME"
public="$work/public"
version=1.2.3
commit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
artifact_dir="$public/artifacts/$version/beta"
package_dir="$public/apt/pool/main/c"
install -d "$artifact_dir" "$package_dir" "$public/apt/dists/beta/by-hash/SHA256"

gpg --batch --passphrase '' --quick-generate-key \
    'Cloudless Promotion Test <test@invalid>' ed25519 sign 1d >/dev/null 2>&1
fingerprint="$(gpg --batch --with-colons --list-keys 2>/dev/null | awk -F: '$1 == "fpr" {print $10; exit}')"
gpg --batch --export "$fingerprint" > "$work/keyring.pgp"

for arch in amd64 arm64; do
    for package in cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater; do
        install -d "$package_dir/$package"
        printf '%s/%s\n' "$package" "$arch" > "$package_dir/$package/${package}_${version}_${arch}.deb"
    done
done
for artifact in install-dgx-spark.sh cloudless-apps-manifest.json cloudless-models.json cloudless-diffusion.json cloudless-trust-inventory.json cloudless-physical-qualification.json "cloudless-$version.spdx.json"; do
    printf 'payload/%s\n' "$artifact" > "$artifact_dir/$artifact"
    gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign \
        --output "$artifact_dir/$artifact.asc" "$artifact_dir/$artifact"
done

python3 - "$public" "$version" "$commit" <<'PY'
import hashlib, json, pathlib, sys
root, version, commit = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
def record(path):
    data = path.read_bytes()
    return hashlib.sha256(data).hexdigest(), len(data)
packages = []
for path in sorted((root / "apt/pool/main/c").glob("*/*.deb")):
    name, package_version, architecture = path.name[:-4].rsplit("_", 2)
    sha, size = record(path)
    packages.append({"name": name, "architecture": architecture, "version": package_version,
                     "changed": True, "filename": path.relative_to(root / "apt").as_posix(),
                     "sha256": sha, "size": size, "rollback": None})
artifact_root = root / f"artifacts/{version}/beta"
artifacts = []
for path in sorted(item for item in artifact_root.iterdir() if not item.name.endswith(".asc")):
    sha, size = record(path)
    signature_sha, signature_size = record(path.with_name(path.name + ".asc"))
    relative = path.relative_to(root).as_posix()
    artifacts.append({"name": path.name, "path": relative, "sha256": sha, "size": size,
                      "signature": relative + ".asc", "signatureSha256": signature_sha,
                      "signatureSize": signature_size})
physical = {"schema": "cloudless.physical-release.v1", "status": "not-qualified", "required": False,
            "version": version, "channel": "beta", "sourceCommit": commit}
targets = [{"platform": "generic", "architecture": "amd64"},
           {"platform": "generic", "architecture": "arm64"},
           {"platform": "dgx-spark", "architecture": "arm64"}]
gates = ["go-tests", "go-vet", "web-javascript", "app-manifest-v2", "backup-recovery",
         "installer-preflight", "platform-matrix", "package-architecture", "package-contents",
         "package-lifecycle", "release-isolation", "release-preflight", "secret-hygiene",
         "service-hardening", "sbom", "trust-inventory", "vulnerability-scan",
         "updater-workload-continuity", "atomic-repository"]
validation = {"schema": "cloudless.release-gates.v1", "version": version, "channel": "beta",
              "sourceCommit": commit, "matrixSha256": "b" * 64, "passedGates": gates,
              "targets": targets, "physicalQualification": physical}
release = {"schema": "cloudless.release.v2", "version": version, "channel": "beta",
           "sourceCommit": commit, "publishedAt": "2026-01-01T00:00:00Z", "packages": packages,
           "rollbackPackages": [], "artifacts": artifacts, "validation": validation,
           "compatibility": {"schema": "cloudless.compatibility.v1", "matrixSha256": "b" * 64,
                             "targets": targets}, "physicalQualification": physical}
manifest = root / "apt/dists/beta/cloudless-release.json"
manifest.parent.mkdir(parents=True, exist_ok=True)
manifest.write_text(json.dumps(release, separators=(",", ":")) + "\n", encoding="utf-8")
sha, size = record(manifest)
(manifest.parent / "by-hash/SHA256" / sha).write_bytes(manifest.read_bytes())
(manifest.parent / "Release").write_text(
    "Origin: Cloudless\nCodename: beta\nArchitectures: amd64 arm64\nSHA256:\n"
    f" {sha} {size} cloudless-release.json\n", encoding="utf-8")
PY
gpg --batch --yes --local-user "$fingerprint" --clearsign \
    --output "$public/apt/dists/beta/InRelease" "$public/apt/dists/beta/Release"

port_file="$work/port"
python3 - "$public" "$port_file" 2>"$work/http.log" <<'PY' &
import functools, http.server, pathlib, socketserver, sys
root, port_file = sys.argv[1:]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=root)
with socketserver.TCPServer(("127.0.0.1", 0), handler) as server:
    pathlib.Path(port_file).write_text(str(server.server_address[1]), encoding="ascii")
    server.serve_forever()
PY
server_pid=$!
for _ in {1..50}; do test -s "$port_file" && break; sleep 0.1; done
test -s "$port_file" || { echo "Test HTTP server did not start." >&2; exit 1; }
port="$(cat "$port_file")"

CLOUDLESS_APT_PUBLIC_URL="http://127.0.0.1:$port/apt" \
CLOUDLESS_PUBLIC_ROOT_URL="http://127.0.0.1:$port" \
CLOUDLESS_ARCHIVE_KEY="$work/keyring.pgp" \
CLOUDLESS_PROMOTION_MIN_AGE_SECONDS=0 \
    bash "$ROOT/distro/scripts/prepare-beta-promotion.sh" "$version" "$commit" "$work/output" >/dev/null 2>&1
test -s "$work/output/cloudless-beta-promotion.json"
test "$(find "$work/output/packages" -type f | wc -l | tr -d '[:space:]')" = 12
test "$(find "$work/output/artifacts" -type f | wc -l | tr -d '[:space:]')" = 14

printf 'tampered\n' >> "$artifact_dir/cloudless-models.json"
if CLOUDLESS_APT_PUBLIC_URL="http://127.0.0.1:$port/apt" \
   CLOUDLESS_PUBLIC_ROOT_URL="http://127.0.0.1:$port" \
   CLOUDLESS_ARCHIVE_KEY="$work/keyring.pgp" \
   CLOUDLESS_PROMOTION_MIN_AGE_SECONDS=0 \
       bash "$ROOT/distro/scripts/prepare-beta-promotion.sh" "$version" "$commit" "$work/tampered" >/dev/null 2>&1; then
    echo "Promotion downloader accepted a tampered signed artifact." >&2
    exit 1
fi

echo "Signed beta promotion download checks passed."
