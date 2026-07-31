# Support and diagnostics

Run **Settings → Machine → Run checks** first. It performs read-only checks for Docker, GPU access,
storage, memory, required services, model promotion and Spark state. **Save support bundle** creates
a ZIP locally; CloudlessOS never uploads it automatically.

From a terminal:

```bash
sudo cloudless-diagnostics
systemctl status cloudlessd docker lightdm --no-pager
journalctl -b -u cloudlessd -u lightdm --no-pager -n 200
```

Before sharing a bundle:

1. Open the ZIP and review every file.
2. Remove application content or identifiers you do not want to disclose.
3. Never add passwords, API tokens, backup passphrases, cluster private keys or Tailscale login
   URLs.
4. Record the Cloudless version, architecture, operation/job ID and exact reproduction steps.

For a boot/display failure, retain `/var/lib/cloudless/boot-health.json`, the validation report and
the bundle created after the failure. For a recipe/model failure, retain its operation ID and
diagnostics before deleting or retrying it. For a cluster issue, collect a separate bundle from
every reachable Spark and label it by node.

`cloudless-repair --repair` is for graphical boot recovery. Do not use it as a generic reset.
Encrypted state recovery and incident response are documented in
[BACKUP_AND_INCIDENT_RESPONSE.md](./BACKUP_AND_INCIDENT_RESPONSE.md).
