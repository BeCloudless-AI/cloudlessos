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
restarts affected services, and checks `/api/health`. A failed health check reinstalls the
preserved generation. Branding and hardware updates request a reboot.

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

## Build a signed release

Package versions must increase according to Debian version ordering. Development
versions containing `dev` are intentionally rejected.

Every release also requires curated notes at
`distro/release/notes/VERSION.json`. The file must contain a non-empty `title` and
`changes` array; `summary` is optional. Release creation stops before signing if the
notes are missing or invalid. The resulting manifest is hashed into the signed APT
metadata so devices can verify it before displaying the changelog.

```bash
sudo apt install reprepro
gpg --import /secure/offline-backup/cloudless-archive-secret.asc
bash distro/scripts/build-apt-repository.sh 0.1.1 stable
```

This builds candidate Cloudless packages, signs the repository metadata, and writes the
static repository to `distro/out/apt-repository`.

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

## Publish to Cloudflare R2

Create an R2 bucket with the custom domain `updates.becloudless.ai`. Give the release
environment credentials limited to that bucket, install the AWS CLI, and set:

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export CLOUDLESS_R2_ENDPOINT=https://ACCOUNT_ID.r2.cloudflarestorage.com
export CLOUDLESS_R2_BUCKET=cloudless-updates
bash distro/scripts/publish-apt-r2.sh
```

Before promoting a release, verify
`https://updates.becloudless.ai/apt/dists/stable/InRelease` and update a beta machine
first. Keep at least one known-good package generation and the archive signing backup
outside R2.

The publisher promotes releases in dependency order and never deletes the previous
generation. Package indexes are available through APT's immutable SHA-256 `by-hash`
paths; signed `InRelease` metadata is uploaded last as the commit point. Metadata is
published with `no-store`, while packages and hash-addressed indexes are immutable. The
command succeeds only after downloading the public repository, validating its archive
signature, and checking every published index and package against the signed hashes.

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
