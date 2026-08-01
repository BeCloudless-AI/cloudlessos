# Support and diagnostics

Run **Settings > Cloudless Doctor** first. It performs read-only checks for Docker, GPU access,
storage, memory, required services, model promotion, Spark state and disagreement between declared
inference state and running recipe containers. **Save support bundle** creates a ZIP locally;
CloudlessOS never uploads it automatically.

When Doctor finds **Orphaned inference runtime**, Cloudless says the engine is unloaded but an exact
container belonging to a saved recipe still runs. **Stop orphaned runtime** removes only those
matched containers from the coordinator and selected workers through fixed privileged actions. It
does not execute the recipe, delete model weights or accept arbitrary Docker arguments. A failed or
unreachable worker remains visible as attention rather than being reported as repaired.

From a terminal:

```bash
sudo cloudless-diagnostics
systemctl status cloudlessd docker lightdm --no-pager
journalctl -b -u cloudlessd -u lightdm --no-pager -n 200
sudo systemctl status cloudless-engine cloudless-privileged --no-pager
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
