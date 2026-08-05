# CloudlessOS updates

Installed systems receive Cloudless-owned packages from the signed APT repository at
`https://updates.becloudless.ai/apt`. Ubuntu security updates remain managed by Ubuntu.
The Cloudless repository is scoped to its own key through `Signed-By`; never add the
archive key to the global APT trust store.

## Device flow

`cloudless-update-check.timer` checks the stable repository daily. The Settings UI can
also start a check or install explicitly through `cloudless-update-check.service` and
`cloudless-update-apply.service`. The update worker runs separately from `cloudlessd`,
so replacing the interface cannot terminate the active package transaction.

Before an install, the worker preserves the currently installed Cloudless `.deb` files.
It downloads and verifies the complete next package generation through APT, installs it,
restarts affected services, and checks `/api/health`. After the candidate passes and the
final status is saved, appliance installations synchronously restart the kiosk once so
even orchestrator-only UI updates become visible immediately. Package maintainer scripts
defer their own kiosk restart while the Update Center transaction is active. A failed
health check reinstalls the preserved generation. Branding and hardware updates request a
reboot. Side-by-side DGX installations never have their desktop session restarted.

The installer seeds `/var/lib/cloudless-updater/current` with its package generation,
which makes rollback available from the first OTA update onward.

## Initialize the production signing identity

Run this once on an offline or tightly controlled Linux machine. The destination must be
outside the repository:

```bash
sudo apt install gnupg
bash distro/scripts/initialize-update-signing.sh \
  /secure/offline-backup/cloudless-archive-secret.asc
```

Commit only these generated public files:

- `distro/release/keys/cloudless-archive-keyring.pgp`
- `distro/release/keys/cloudless-archive-fingerprint.txt`

Keep the encrypted secret backup and its passphrase offline. Import the secret key only
inside the protected release environment when signing.

## One-command production release

CloudlessOS uses Debian-native release revisions. The first package revision of an
upstream version is `0.2.7-1`, followed by `0.2.7-2`, `0.2.7-3`, and so on. The
next upstream feature version starts at `0.2.8-1`. Development trees use `~dev`
(for example `0.2.7-1~dev`), which Debian correctly sorts before `0.2.7-1`.
Development versions are intentionally rejected by production publishing.

Every release also requires curated notes at
`distro/release/notes/VERSION.json`. The file must contain a non-empty `title` and
`changes` array; `summary` is optional. Release creation stops before signing if the
notes are missing or invalid. The resulting manifest is hashed into the signed APT
metadata so devices can verify it before displaying the changelog.

Configure the bucket credentials and signing-key backup, commit the release,
and push the current branch. Then run exactly one production command:

```bash
bash distro/scripts/configure-release-env.sh # one time per release workstation
bash distro/scripts/release.sh 0.2.7-1 beta
```

The one-time setup stores these values in `~/.config/cloudless/release.env` with
mode `0600`. The release command loads only the supported settings from that
file without evaluating it as shell code. Set `CLOUDLESS_RELEASE_ENV` to use a
different private path. Never put this file in the repository.

To bind completed physical campaigns, set the optional non-secret
`CLOUDLESS_PHYSICAL_QUALIFICATION_DIR` entry to a private directory containing the sealed target
ZIPs produced by `cloudless-qualify export`. The release command verifies the complete set before
running expensive builders. Pre-1.0 releases without it are signed with an explicit
`not-qualified` descriptor; stable releases from 1.0 onward fail closed unless every matrix target
belongs to the exact version and source commit being released.

The command refuses dirty or unpushed source, including untracked files. Before starting a builder,
production preflight also requires the committed archive fingerprint, Cloudless update signing
identity, account-scoped Cloudflare endpoint, non-placeholder credential shapes and the exact
AMD64/ARM64/DGX validation contract and physical-evidence policy. It runs the Go, browser
JavaScript, AMD64, ARM64, package-content, signed-repository, and atomic
publication tests; asks for explicit confirmation; imports the signing key only
inside the disposable release container; signs the complete generation;
publishes it; and downloads it from the public domain for final verification.
Any failed platform or public artifact aborts the command.

The signed release records the exact Git commit. If networking fails during
publication, rerun the same `release.sh` command: it cryptographically verifies
and resumes that generation only when the version, commit, source artifacts,
and signatures still match.

If signing already completed and only the upload failed, skip all build gates
and resume the exact signed generation with:

```bash
bash distro/scripts/publish-signed-release.sh 0.2.2 stable
```

This command loads the same private environment file, validates its endpoint,
checks the requested version and channel, and lets the publisher verify every
local signature and hash before uploading. It never rebuilds or signs packages.

Release builds compare each candidate package with the currently published signed
baseline. Only packages whose installed contents changed receive the new version and are
added to the repository. For example, an interface-only `0.1.2` release updates
`cloudless-orchestrator` while the other packages remain at `0.1.1`. A build with no
package changes exits without creating an empty release. If the local repository cache is
missing, the builder verifies and recovers the baseline from `updates.becloudless.ai`.

Preview package selection without signing or modifying the repository:

```bash
CLOUDLESS_RELEASE_DRY_RUN=1 \
  bash distro/scripts/build-apt-repository.sh 0.1.2 stable
```

## Signed standalone artifacts and atomic promotion

Create an R2 bucket with the custom domain `updates.becloudless.ai`. Give the release
environment credentials limited to that bucket. Keep at least one known-good
package generation and the archive signing backup outside R2.

The publisher promotes releases in dependency order and never deletes the previous
generation. Package indexes are available through APT's immutable SHA-256 `by-hash`
paths; signed `InRelease` metadata is uploaded last as the commit point. Metadata is
published with `no-store`, while packages and hash-addressed indexes are immutable. The
command succeeds only after downloading the public repository, validating its archive
signature, and checking every published index and package against the signed hashes.

The changelog is also fetched through a signed SHA-256 `by-hash` path. The updater verifies
`InRelease`, extracts the manifest hash and size, and then fetches that immutable object. A release
test fails every R2 upload in turn and proves that the public commit always describes either the
complete previous generation or the complete new one.

`cloudless-release.json` uses the `cloudless.release.v2` schema. It inventories the complete six
package by two architecture generation with version, repository filename, SHA-256 and byte size,
plus retained per-package rollback objects. The embedded compatibility contract repeats the exact
validated generic AMD64, generic ARM64 and DGX Spark ARM64 targets and binds them to the release
matrix hash. Devices reject manifests for another channel, platform or architecture.

Each release also contains immutable, version-and-channel-scoped copies of
`install-dgx-spark.sh` and `cloudless-apps-manifest.json`. Their SHA-256 hashes
and detached-signature hashes are inside `cloudless-release.json`, which is
itself covered by signed APT metadata. The immutable files are uploaded before
`InRelease`; mutable convenience aliases are updated only after that commit.
The retained release manifest uses the same `<version>/<channel>` namespace, so
publishing stable metadata cannot overwrite beta qualification evidence or signatures
for the same version.
Cloudless devices verify the application manifest with the packaged archive
key and retain the last verified copy if the network or signature is invalid.
The DGX installation guide verifies the installer signature before execution.

`sign-release-interactive.sh` and `publish-apt-r2.sh` remain available for
diagnostics, but production releases should use `release.sh` so neither half of
the pipeline can be accidentally skipped.

A production `stable` release is now a promotion of the already-published `beta`
generation for the same version and full source commit. `release.sh` verifies the
beta `InRelease`, fetches the changelog through its signed by-hash path, verifies
all 12 package payloads and every detached-signed standalone artifact, and enforces
the seven-day beta soak from the by-hash object's public `Last-Modified` time rather
than its earlier signing time. The stable repository is built from those exact package
and common artifact bytes; only stable channel metadata, physical-qualification
identity and detached signatures are regenerated. A missing, young, incomplete,
tampered or source-mismatched beta candidate fails before the signing environment.

`publish-signed-release.sh` is a recovery-only resume path. It now runs the same production
preflight as `release.sh` and refuses to publish unless the signed manifest describes the exact
current clean commit, production signing identity, channel, version and destination.

Before installing packages, the device snapshots the currently ready inference engine and every
non-terminal durable recipe operation. After restarting `cloudlessd`, it requires the same engine
to remain ready and each operation to remain recoverable. A lost workload triggers package
rollback, followed by the same continuity verification against the restored generation.

Production release attestation includes a secret-hygiene gate. It inspects every tracked and
non-ignored source file for recognizable private token and key material without printing a matched
value. See [Secure release credentials](../docs/SECURE_RELEASE_CREDENTIALS.md) for rotation and
least-privilege requirements.

## Existing installations

An installation made before `cloudless-updater` existed needs a one-time bootstrap. Build
the updater package with the public archive key present, copy it to the computer, then run:

```bash
ARCH="$(dpkg --print-architecture)"
sudo apt install "./cloudless-updater_VERSION_${ARCH}.deb"
sudo systemctl start cloudless-update-check.service
```

All later updates arrive through the signed repository; reinstalling the ISO is not
required.

## DGX Spark ownership boundary

The same signed repository publishes ARM64 Cloudless packages. On DGX Spark,
Cloudless updates only Cloudless-owned packages. NVIDIA's DGX OS updater remains
the sole owner of the kernel, firmware, GPU driver, CUDA and container toolkit.
The Cloudless updater recognizes the platform and never substitutes Ubuntu's
generic driver path. Release signing and publication validate both `amd64` and
`arm64` indexes before the signed `InRelease` file is promoted.

## Local custom-engine ownership

Source-built inference images registered through Settings are machine-local operator assets. The
Cloudless updater does not upload, sign, pull, replace or delete them. Their registrations persist
across Cloudless package updates, while the managed engine remains available as a recovery path.
Operators are responsible for rebuilding and retagging a custom image when its source, CUDA stack
or dependencies change. See [Custom inference engines](../docs/CUSTOM_ENGINES.md) for the supported
build, registration and rollback workflow.
