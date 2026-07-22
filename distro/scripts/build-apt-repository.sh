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
PACKAGES=(cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater)

if [ -z "$VERSION" ] || [[ "$VERSION" == *dev* ]]; then
    echo "Usage: $0 VERSION [stable|beta] (development versions cannot be published)" >&2
    exit 2
fi
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
for command in curl dpkg dpkg-deb gpg gpgv python3 reprepro sha256sum; do command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }; done
test -s "$KEY" || { echo "Initialize the archive signing key first." >&2; exit 1; }
test -s "$FINGERPRINT_FILE" || { echo "Missing archive fingerprint file." >&2; exit 1; }
test -s "$NOTES" || { echo "Missing release notes: $NOTES" >&2; exit 1; }
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

CLOUDLESS_VERSION="$VERSION" "$DISTRO/scripts/build-packages.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/baseline"

find_local_package() {
    local package="$1" best="" best_version="" candidate candidate_version
    while IFS= read -r candidate; do
        candidate_version="$(dpkg-deb -f "$candidate" Version)"
        if [ -z "$best_version" ] || dpkg --compare-versions "$candidate_version" gt "$best_version"; then
            best="$candidate"
            best_version="$candidate_version"
        fi
    done < <(find "$REPO/pool" -type f -name "${package}_*_amd64.deb" 2>/dev/null || true)
    printf '%s' "$best"
}

fetch_remote_baseline() {
    local inrelease="$work/InRelease" index="$work/Packages" index_hash index_size
    echo "==> Recovering current $CHANNEL package baseline from $BASE_URL"
    curl -fsS "$BASE_URL/dists/$CHANNEL/InRelease" -o "$inrelease" || return 1
    gpgv --keyring "$KEY" "$inrelease" >/dev/null 2>&1 || return 1
    curl -fsS "$BASE_URL/dists/$CHANNEL/main/binary-amd64/Packages" -o "$index" || return 1
    index_hash="$(sha256sum "$index" | awk '{print $1}')"
    index_size="$(wc -c < "$index" | tr -d '[:space:]')"
    grep -Eq "^ ${index_hash} +${index_size} +main/binary-amd64/Packages$" "$inrelease" || return 1

    local package paragraph filename checksum target
    for package in "${PACKAGES[@]}"; do
        paragraph="$(awk -v package="$package" 'BEGIN {RS=""} $0 ~ "(^|\\n)Package: " package "(\\n|$)" {print; exit}' "$index")"
        [ -n "$paragraph" ] || continue
        filename="$(printf '%s\n' "$paragraph" | awk '/^Filename: / {print $2; exit}')"
        checksum="$(printf '%s\n' "$paragraph" | awk '/^SHA256: / {print $2; exit}')"
        [ -n "$filename" ] && [ -n "$checksum" ] || return 1
        target="$work/baseline/$(basename "$filename")"
        curl -fsS "$BASE_URL/$filename" -o "$target" || return 1
        printf '%s  %s\n' "$checksum" "$target" | sha256sum --check --status || return 1
    done
}

need_remote=false
for package in "${PACKAGES[@]}"; do
    if [ -z "$(find_local_package "$package")" ]; then need_remote=true; break; fi
done
if $need_remote && ! fetch_remote_baseline; then
    echo "==> No verified remote baseline found; treating this as the first release"
    rm -f "$work/baseline"/*.deb
fi

declare -A PREVIOUS_DEB PREVIOUS_VERSION CHANGED
changed_count=0
for package in "${PACKAGES[@]}"; do
    previous="$(find_local_package "$package")"
    if [ -z "$previous" ]; then
        previous="$(find "$work/baseline" -type f -name "${package}_*_amd64.deb" -print -quit)"
    fi
    candidate="$DISTRO/out/packages/${package}_${VERSION}_amd64.deb"
    if [ -n "$previous" ]; then
        PREVIOUS_DEB[$package]="$previous"
        PREVIOUS_VERSION[$package]="$(dpkg-deb -f "$previous" Version)"
        if bash "$DISTRO/scripts/package-content-equal.sh" "$previous" "$candidate"; then
            echo "==> Unchanged: $package (${PREVIOUS_VERSION[$package]})"
            CHANGED[$package]=false
            continue
        fi
        dpkg --compare-versions "$VERSION" gt "${PREVIOUS_VERSION[$package]}" || {
            echo "$VERSION must be newer than ${PREVIOUS_VERSION[$package]} for $package" >&2
            exit 1
        }
    fi
    echo "==> Changed: $package -> $VERSION"
    CHANGED[$package]=true
    changed_count=$((changed_count + 1))
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
Architectures: amd64
Components: main
Description: Signed CloudlessOS $CHANNEL updates
SignWith: $fingerprint
EOF
cat > "$REPO/conf/options" <<'EOF'
verbose
ask-passphrase
EOF
if [ ! -s "$REPO/db/packages.db" ]; then
    for package in "${PACKAGES[@]}"; do
        if [ -n "${PREVIOUS_DEB[$package]:-}" ]; then
            reprepro --basedir "$REPO" includedeb "$CHANNEL" "${PREVIOUS_DEB[$package]}"
        fi
    done
fi
for package in "${PACKAGES[@]}"; do
    if ${CHANGED[$package]}; then
        reprepro --basedir "$REPO" includedeb "$CHANNEL" "$DISTRO/out/packages/${package}_${VERSION}_amd64.deb"
    fi
done

changes_file="$work/changed-packages.tsv"
for package in "${PACKAGES[@]}"; do
    if ${CHANGED[$package]}; then
        printf '%s\t%s\t%s\n' "$package" "${PREVIOUS_VERSION[$package]:-}" "$VERSION" >> "$changes_file"
    fi
done
install -d "$REPO/releases"
manifest="$REPO/releases/$VERSION.json"
python3 - "$NOTES" "$changes_file" "$manifest" "$VERSION" "$CHANNEL" "$(date -u +%FT%TZ)" <<'PY'
import json, sys
notes_path, changes_path, output, version, channel, published_at = sys.argv[1:]
with open(notes_path, encoding="utf-8") as handle:
    notes = json.load(handle)
packages = []
with open(changes_path, encoding="utf-8") as handle:
    for line in handle:
        name, previous, current = line.rstrip("\n").split("\t")
        packages.append({"name": name, "from": previous, "to": current})
manifest = {
    "version": version,
    "channel": channel,
    "publishedAt": published_at,
    "title": notes["title"].strip(),
    "summary": str(notes.get("summary", "")).strip(),
    "changes": [item.strip() for item in notes["changes"]],
    "packages": packages,
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
