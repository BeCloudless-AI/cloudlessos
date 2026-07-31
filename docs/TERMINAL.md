# Terminal and advanced local builds

Cloudless Terminal is an unrestricted authenticated Linux terminal. It is intentionally not a
sandbox: an administrator using `sudo` can replace packages, read local data, change networking,
break the kiosk, bypass recipe controls or alter the update trust chain.

Use the terminal for diagnostics and advanced local inference work, not for routine application
management already available in CloudlessOS. Terminal tabs and sessions remain alive when the
floating window is hidden; closing or rebooting the underlying shell still ends its processes.

Before changing the host:

1. Create and verify an encrypted `cloudless-backup`.
2. Record package, driver and Cloudless versions.
3. Keep source/build output outside package-owned paths.
4. Build a container image with an immutable digest.
5. Register it through **Settings → Engine → Register local build**.

Registration validates architecture and stores a managed compatibility profile. Cloudless still
owns the stable private model name and port, health contract, gateway and rollback behavior.
Do not replace `/usr/lib/cloudless/cloudlessd`, edit package files in place or bind a custom engine
directly to public interfaces.

The complete source-build and registration contract is in
[CUSTOM_ENGINES.md](./CUSTOM_ENGINES.md). Root changes are outside the Cloudless support boundary
until restored to a signed package state.
