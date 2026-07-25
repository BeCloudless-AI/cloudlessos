#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
REPO="${CLOUDLESS_APT_REPO_OUT:-$DISTRO/out/apt-repository}"
BASE_URL="${CLOUDLESS_APT_BASE_URL:-https://updates.becloudless.ai/apt}"
KEY="${CLOUDLESS_ARCHIVE_KEY:-$DISTRO/release/keys/cloudless-archive-keyring.pgp}"
FINGERPRINT_FILE="${CLOUDLESS_ARCHIVE_FINGERPRINT_FILE:-$DISTRO/release/keys/cloudless-archive-fingerprint.txt}"
NOTES="${CLOUDLESS_RELEASE_NOTES:-$DISTRO/release/notes/$VERSION.json}"
APP_MANIFEST="${CLOUDLESS_APP_MANIFEST:-$DISTRO/release/manifests/cloudless-apps-manifest.json}"
DGX_INSTALLER="${CLOUDLESS_DGX_INSTALLER:-$DISTRO/scripts/install-dgx-spark.sh}"
SOURCE_COMMIT="${CLOUDLESS_SOURCE_COMMIT:-$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)}"
PACKAGES=(cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater)
read -r -a ARCHES <<< "${CLOUDLESS_ARCHES:-amd64 arm64}"

if [ -z "$VERSION" ] || [[ "$VERSION" == *dev* ]]; then
    echo "Usage: $0 VERSION [stable|beta] (development versions cannot be published)" >&2
    exit 2
fi
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
for arch in "${ARCHES[@]}"; do
    case "$arch" in amd64|arm64) ;; *) echo "Unsupported release architecture: $arch" >&2; exit 2 ;; esac
done
for command in curl dpkg dpkg-deb gpg gpgv python3 reprepro sha256sum; do command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }; done
test -s "$KEY" || { echo "Initialize the archive signing key first." >&2; exit 1; }
test -s "$FINGERPRINT_FILE" || { echo "Missing archive fingerprint file." >&2; exit 1; }
test -s "$NOTES" || { echo "Missing release notes: $NOTES" >&2; exit 1; }
test -s "$APP_MANIFEST" || { echo "Missing application manifest: $APP_MANIFEST" >&2; exit 1; }
test -s "$DGX_INSTALLER" || { echo "Missing DGX Spark installer: $DGX_INSTALLER" >&2; exit 1; }
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || {
    echo "A full Git source commit is required for a production release." >&2
    exit 1
}
python3 - "$NOTES" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    notes = json.load(handle)
if not isinstance(notes.get("title"), str) or not notes["title"].strip():
    raise SystemExit("Release notes require a non-empty title")
if not isinstance(notes.get("changes"), list) or not notes["changes"] or not all(isinstance(item, str) and item.strip() for item in notes["changes"]):
    raise SystemExit("Release notes require at least one non-empty change")
PY
fingerprint="$(tr -d '[:space:]' < "$FINGERPRINT_FILE")"
if [ "${CLOUDLESS_RELEASE_DRY_RUN:-0}" != "1" ]; then
    gpg --batch --list-secret-keys "$fingerprint" >/dev/null 2>&1 || {
        echo "The secret signing key is not loaded in GNUPGHOME." >&2
        exit 1
    }
fi

for arch in "${ARCHES[@]}"; do
    CLOUDLESS_VERSION="$VERSION" CLOUDLESS_ARCH="$arch" "$DISTRO/scripts/build-packages.sh"
done

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/baseline"

find_local_package() {
    local package="$1" arch="$2" best="" best_version="" candidate candidate_version
    while IFS= read -r candidate; do
        candidate_version="$(dpkg-deb -f "$candidate" Version)"
        if [ -z "$best_version" ] || dpkg --compare-versions "$candidate_version" gt "$best_version"; then
            best="$candidate"
            best_version="$candidate_version"
        fi
    done < <(find "$REPO/pool" -type f -name "${package}_*_${arch}.deb" 2>/dev/null || true)
    printf '%s' "$best"
}

fetch_remote_baseline() {
    local arch="$1" inrelease="$work/InRelease" verified="$work/InRelease.verified" index="$work/Packages-$1" index_hash index_size
    echo "==> Recovering current $CHANNEL/$arch package baseline from $BASE_URL"
    if [ ! -s "$verified" ]; then
        rm -f "$inrelease" "$verified"
        curl -fsS "$BASE_URL/dists/$CHANNEL/InRelease" -o "$inrelease" || return 1
        if ! gpgv --keyring "$KEY" "$inrelease" >/dev/null 2>&1; then
            rm -f "$inrelease"
            return 1
        fi
        printf '%s\n' "$fingerprint" > "$verified"
    fi
    curl -fsS "$BASE_URL/dists/$CHANNEL/main/binary-$arch/Packages" -o "$index" || return 1
    index_hash="$(sha256sum "$index" | awk '{print $1}')"
    index_size="$(wc -c < "$index" | tr -d '[:space:]')"
    grep -Eq "^ ${index_hash} +${index_size} +main/binary-${arch}/Packages$" "$inrelease" || return 1

    local package paragraph filename checksum target
    mkdir -p "$work/baseline/$arch"
    for package in "${PACKAGES[@]}"; do
        paragraph="$(awk -v package="$package" 'BEGIN {RS=""} $0 ~ "(^|\\n)Package: " package "(\\n|$)" {print; exit}' "$index")"
        [ -n "$paragraph" ] || continue
        filename="$(printf '%s\n' "$paragraph" | awk '/^Filename: / {print $2; exit}')"
        checksum="$(printf '%s\n' "$paragraph" | awk '/^SHA256: / {print $2; exit}')"
        [ -n "$filename" ] && [ -n "$checksum" ] || return 1
        target="$work/baseline/$arch/$(basename "$filename")"
        curl -fsS "$BASE_URL/$filename" -o "$target" || return 1
        printf '%s  %s\n' "$checksum" "$target" | sha256sum --check --status || return 1
    done
}

for arch in "${ARCHES[@]}"; do
    need_remote=false
    for package in "${PACKAGES[@]}"; do
        if [ -z "$(find_local_package "$package" "$arch")" ]; then need_remote=true; break; fi
    done
    if $need_remote && ! fetch_remote_baseline "$arch"; then
        echo "==> No verified remote $arch baseline found; treating it as a first architecture release"
        rm -rf "$work/baseline/$arch"
    fi
done

declare -A PREVIOUS_DEB PREVIOUS_VERSION PREVIOUS_REMOTE CHANGED
changed_count=0
for arch in "${ARCHES[@]}"; do
    for package in "${PACKAGES[@]}"; do
        key="$arch/$package"
        previous="$(find_local_package "$package" "$arch")"
        if [ -z "$previous" ]; then
            previous="$(find "$work/baseline/$arch" -type f -name "${package}_*_${arch}.deb" -print -quit 2>/dev/null || true)"
            if [ -n "$previous" ]; then PREVIOUS_REMOTE[$key]=true; fi
        fi
        candidate="$DISTRO/out/packages/${package}_${VERSION}_${arch}.deb"
        test -s "$candidate" || { echo "Missing candidate package: $candidate" >&2; exit 1; }
        if [ -n "$previous" ]; then
            PREVIOUS_DEB[$key]="$previous"
            PREVIOUS_VERSION[$key]="$(dpkg-deb -f "$previous" Version)"
            if bash "$DISTRO/scripts/package-content-equal.sh" "$previous" "$candidate"; then
                echo "==> Unchanged: $package/$arch (${PREVIOUS_VERSION[$key]})"
                CHANGED[$key]=false
                continue
            fi
            dpkg --compare-versions "$VERSION" gt "${PREVIOUS_VERSION[$key]}" || {
                echo "$VERSION must be newer than ${PREVIOUS_VERSION[$key]} for $package/$arch" >&2
                exit 1
            }
        fi
        echo "==> Changed: $package/$arch -> $VERSION"
        CHANGED[$key]=true
        changed_count=$((changed_count + 1))
    done
done

if [ "$changed_count" -eq 0 ]; then
    echo "No Cloudless package contents changed; no release is needed." >&2
    exit 3
fi
if [ "${CLOUDLESS_RELEASE_DRY_RUN:-0}" = "1" ]; then
    echo "Dry run complete: $changed_count package(s) would be released."
    exit 0
fi

install -d "$REPO/conf" "$REPO/releases"
cat > "$REPO/conf/distributions" <<EOF
Origin: Cloudless
Label: CloudlessOS
Codename: $CHANNEL
Suite: $CHANNEL
Architectures: ${ARCHES[*]}
Components: main
Description: Signed CloudlessOS $CHANNEL updates
SignWith: $fingerprint
EOF
cat > "$REPO/conf/options" <<'EOF'
verbose
ask-passphrase
EOF
if [ ! -s "$REPO/db/packages.db" ]; then
    for arch in "${ARCHES[@]}"; do
        for package in "${PACKAGES[@]}"; do
            key="$arch/$package"
            if [ -n "${PREVIOUS_DEB[$key]:-}" ]; then
                reprepro --basedir "$REPO" includedeb "$CHANNEL" "${PREVIOUS_DEB[$key]}"
            fi
        done
    done
else
    # A release can be built from an older local repository while the public
    # baseline already contains another architecture. Seed those verified
    # packages into the local database before applying this release's changes.
    for arch in "${ARCHES[@]}"; do
        for package in "${PACKAGES[@]}"; do
            key="$arch/$package"
            if ${PREVIOUS_REMOTE[$key]:-false}; then
                reprepro --basedir "$REPO" includedeb "$CHANNEL" "${PREVIOUS_DEB[$key]}"
            fi
        done
    done
fi
for arch in "${ARCHES[@]}"; do
    for package in "${PACKAGES[@]}"; do
        key="$arch/$package"
        if ${CHANGED[$key]}; then
            reprepro --basedir "$REPO" includedeb "$CHANNEL" "$DISTRO/out/packages/${package}_${VERSION}_${arch}.deb"
        fi
    done
done

changes_file="$work/changed-packages.tsv"
for arch in "${ARCHES[@]}"; do
    for package in "${PACKAGES[@]}"; do
        key="$arch/$package"
        if ${CHANGED[$key]}; then
            printf '%s\t%s\t%s\t%s\n' "$package" "$arch" "${PREVIOUS_VERSION[$key]:-}" "$VERSION" >> "$changes_file"
        fi
    done
done
install -d "$REPO/releases"
artifacts_dir="$REPO/artifacts/$VERSION"
rm -rf "$artifacts_dir"
install -d "$artifacts_dir"
install -m 0755 "$DGX_INSTALLER" "$artifacts_dir/install-dgx-spark.sh"
install -m 0644 "$APP_MANIFEST" "$artifacts_dir/cloudless-apps-manifest.json"
artifacts_file="$work/artifacts.tsv"
for artifact in install-dgx-spark.sh cloudless-apps-manifest.json; do
    file="$artifacts_dir/$artifact"
    signature="$file.asc"
    gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign \
        --output "$signature" "$file"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$artifact" \
        "artifacts/$VERSION/$artifact" \
        "$(sha256sum "$file" | awk '{print $1}')" \
        "$(wc -c < "$file" | tr -d '[:space:]')" \
        "$(sha256sum "$signature" | awk '{print $1}')" \
        "$(wc -c < "$signature" | tr -d '[:space:]')" \
        >> "$artifacts_file"
done
manifest="$REPO/releases/$VERSION.json"
python3 - "$NOTES" "$changes_file" "$artifacts_file" "$manifest" "$VERSION" "$CHANNEL" "$SOURCE_COMMIT" "$(date -u +%FT%TZ)" <<'PY'
import json, sys
notes_path, changes_path, artifacts_path, output, version, channel, source_commit, published_at = sys.argv[1:]
with open(notes_path, encoding="utf-8") as handle:
    notes = json.load(handle)
packages = []
with open(changes_path, encoding="utf-8") as handle:
    for line in handle:
        name, architecture, previous, current = line.rstrip("\n").split("\t")
        packages.append({"name": name, "architecture": architecture, "from": previous, "to": current})
artifacts = []
with open(artifacts_path, encoding="utf-8") as handle:
    for line in handle:
        name, path, sha256, size, signature_sha256, signature_size = line.rstrip("\n").split("\t")
        artifacts.append({
            "name": name,
            "path": path,
            "sha256": sha256,
            "size": int(size),
            "signature": path + ".asc",
            "signatureSha256": signature_sha256,
            "signatureSize": int(signature_size),
        })
manifest = {
    "version": version,
    "channel": channel,
    "sourceCommit": source_commit,
    "publishedAt": published_at,
    "title": notes["title"].strip(),
    "summary": str(notes.get("summary", "")).strip(),
    "changes": [item.strip() for item in notes["changes"]],
    "packages": packages,
    "artifacts": artifacts,
}
with open(output, "w", encoding="utf-8") as handle:
    json.dump(manifest, handle, ensure_ascii=False, separators=(",", ":"))
    handle.write("\n")
PY
cp "$manifest" "$REPO/dists/$CHANNEL/cloudless-release.json"

# APT's by-hash protocol makes index promotion race-free: clients fetch the
# immutable SHA-256 path named by signed metadata instead of a mutable
# Packages filename that may be changing during publication.
release="$REPO/dists/$CHANNEL/Release"
while IFS= read -r index; do
    hash="$(sha256sum "$index" | awk '{print $1}')"
    by_hash="$(dirname "$index")/by-hash/SHA256/$hash"
    install -Dm0644 "$index" "$by_hash"
done < <(find "$REPO/dists/$CHANNEL" -type f \( -name Packages -o -name Packages.gz \) ! -path '*/by-hash/*' -print)
if ! grep -Fqx 'Acquire-By-Hash: yes' "$release"; then
    sed -i '/^Suite:/a Acquire-By-Hash: yes' "$release"
fi
manifest_hash="$(sha256sum "$REPO/dists/$CHANNEL/cloudless-release.json" | awk '{print $1}')"
manifest_size="$(wc -c < "$REPO/dists/$CHANNEL/cloudless-release.json" | tr -d '[:space:]')"
sed -i '/ cloudless-release\.json$/d' "$release"
sed -i "/^SHA256:/a\\ $manifest_hash $manifest_size cloudless-release.json" "$release"
rm -f "$release.gpg" "$REPO/dists/$CHANNEL/InRelease"
gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign --output "$release.gpg" "$release"
gpg --batch --yes --local-user "$fingerprint" --clearsign --output "$REPO/dists/$CHANNEL/InRelease" "$release"

cp "$KEY" "$REPO/cloudless-archive-keyring.pgp"
echo "Signed APT repository ready at $REPO"
