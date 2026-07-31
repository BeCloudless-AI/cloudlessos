# CloudlessOS backup and incident response

CloudlessOS provides an encrypted control-plane backup for configuration, generated secrets,
recipes, API-key records, cluster identity, audit history and the installed update generation. It
does not copy model weights, Hugging Face caches, container images or Docker volumes. Back up those
large data sets separately when they contain irreplaceable user data.

## Create and verify a backup

Attach storage that is not permanently mounted to the appliance, then run:

```bash
sudo cloudless-backup create /media/backup/cloudless-$(date +%F).cloudless-backup
sudo cloudless-backup verify /media/backup/cloudless-$(date +%F).cloudless-backup
```

The command refuses to overlap another backup/restore or an active Cloudless system/NVIDIA update,
then briefly stops `cloudlessd` for a consistent control-plane snapshot and restarts it before
returning. The archive is published to its final filename only after the complete payload validates;
an interrupted attempt cannot replace that destination with a partial archive. It is encrypted with
GnuPG AES-256, decrypted and integrity-checked once before publication, mode 0600, and contains an inner
SHA-256 integrity manifest. No plaintext archive is left behind.

For scheduled backups, create a root-owned mode-0600 passphrase file on removable or protected
storage and point the command to it:

```bash
sudo install -m 0600 /dev/stdin /root/.cloudless-backup-passphrase
sudo CLOUDLESS_BACKUP_PASSPHRASE_FILE=/root/.cloudless-backup-passphrase \
  cloudless-backup create /media/backup/cloudless-control-plane.cloudless-backup
```

Keep the passphrase separately from the backup. Losing it makes the archive unrecoverable. Anyone
holding both can recover API identities, app secrets and the Spark cluster SSH identity.

## Restore

First isolate the machine from untrusted LAN/public networks. Verify, then restore:

```bash
sudo cloudless-backup verify /media/backup/cloudless-control-plane.cloudless-backup
sudo cloudless-backup restore /media/backup/cloudless-control-plane.cloudless-backup
sudo reboot
```

Restore decrypts and hashes the archive before changing the machine, rejects absolute/traversal or
non-Cloudless paths, rejects a backup made for a different CPU architecture, stops `cloudlessd`,
and restores an exact snapshot: a protected path absent
from the backup is removed rather than leaving newer stale configuration active. Replaced or
removed state is retained under
`/var/backups/cloudless/<UTC timestamp>`. A failure during replacement automatically puts the
previous state back and preserves the failed candidate for diagnosis.

After reboot:

1. Run `sudo cloudless-diagnostics` and save the support bundle.
2. Confirm the update channel/version and run **Check again**.
3. Confirm Engine and API Access settings before loading a model.
4. Re-check every LAN, public tunnel, Tailscale Serve/SSH and API-key setting.
5. For a Spark cluster, run cluster checks before starting distributed inference.

Restoring an old backup also restores old API-key records and cluster credentials. Rotate them when
the backup predates a suspected compromise.

## Incident playbooks

### Exposed or stolen API key

1. Disable LAN, public tunnel and Tailscale Serve exposure.
2. Revoke the affected key—or all keys when attribution is uncertain.
3. Export a support bundle and preserve `security-audit.json` before further cleanup.
4. Generate narrowly scoped replacement keys and distribute them through a different channel.
5. Review source addresses, scopes and denied/rate-limited requests in the security audit.

### Suspicious public application or Tailscale access

1. Disable that application's public/LAN exposure and Tailscale Serve/SSH.
2. Disconnect the network if active access continues.
3. Preserve a support bundle plus relevant container logs.
4. Reset application credentials; revoke API keys; log Tailscale out if its identity is suspect.
5. Re-enable one exposure at a time only after authentication and updates are verified.

### Recipe or container compromise

1. Abort/stop the recipe and unload its model. Do not delete its operation record yet.
2. Disable external exposure, preserve a support bundle, recipe source inventory and container logs.
3. Remove the affected container and immutable image only after evidence is retained.
4. Rotate Hugging Face, API and cluster credentials the recipe could have reached.
5. Restore a known-good control-plane backup if durable state changed, then re-check trust and
   permissions before running any recipe.

Unreviewed recipes use the constrained container adapter. Host-command recipes must exactly match a
Cloudless-signed profile; a locally edited copy is blocked. Treat any bypass of either invariant as
a release-blocking security defect.

### Update/signing concern

1. Do not run another update and disconnect public access.
2. Preserve `/var/lib/cloudless-updater`, package versions, APT metadata and a support bundle.
3. Compare the installed generation, SBOM, vulnerability report, release-gate attestation and
   detached signatures with the immutable published release.
4. If verification differs, quarantine the release channel. Recover from the prior signed package
   generation or reinstall from known-good media; rotate publication credentials offline.

### Spark cluster identity or peer compromise

1. Stop distributed inference and disconnect the cluster in CloudlessOS.
2. Disable Tailscale/public access and isolate the affected peer.
3. Preserve support bundles from every peer.
4. Remove the old cluster identity/known-host relationship by rebuilding the cluster from trusted
   nodes. Rotate any credentials used outside Cloudless.
5. Re-run cable, SSH, topology and inference checks before loading a distributed model.

## Required rehearsal

Before a release is promoted, run:

```bash
bash distro/scripts/test-incident-response.sh
```

The rehearsal creates an encrypted backup of a disposable machine root, verifies it, changes state,
restores the archive, proves secret/state recovery and rollback retention, and proves a corrupted
archive is rejected. Physical release qualification must additionally restore a fresh backup on one
VM and one Spark, reboot, run diagnostics, and retain the before/after bundles.
