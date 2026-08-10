# Cloudless Desktop portability roadmap

**Status:** Planned; implementation has not started

**Last updated:** 2026-08-08
**Estimate:** 6–10 weeks for one experienced full-time engineer; approximately 3–4 weeks to an
initial Windows/Linux beta with two engineers working in parallel.

This roadmap turns Cloudless from an appliance-only experience into software that can be installed
alongside an existing operating system. Users must not need to format their computer, replace their
desktop, or configure dual boot to run Cloudless.

CloudlessOS remains the dedicated-machine appliance edition. Cloudless Desktop reuses the same
interface, orchestrator, recipes, model cache, authenticated inference gateway, and signed release
discipline on existing Linux and Windows installations.

## Product outcome

| Edition | Host | Delivery | Replaces the OS |
|---|---|---|---:|
| CloudlessOS | Dedicated AMD64 NVIDIA machine | Bootable ISO | Yes |
| Cloudless for Linux | Existing supported NVIDIA Linux | Signed host package and managed containers | No |
| Cloudless Desktop for Windows | Supported NVIDIA Windows | Signed installer and managed WSL2 environment | No |

DGX Spark remains a signed, reversible package layer over NVIDIA's qualified DGX OS. macOS and
machines without a supported NVIDIA runtime are initially client-only: they may connect to a remote
Cloudless node, but are not local inference hosts.

## Product principles

1. **No destructive installation.** Do not repartition disks, replace bootloaders, change the
   desktop session, remove an existing runtime, or silently replace an NVIDIA driver.
2. **One Cloudless experience.** UI, recipes, accounts, jobs, gateway, diagnostics, and model
   management remain shared across editions.
3. **Reuse recipe containers.** A recipe is rebuilt only when its runtime itself lacks platform
   support, not merely because the host is native Linux or Windows/WSL2.
4. **A container is not the host authority.** Never give the application container an unrestricted
   Docker socket, arbitrary root shell, or broad host filesystem access.
5. **Host changes are explicit and reversible.** A small signed host bridge owns narrow privileged
   operations and provides tested install, update, repair, and uninstall paths.
6. **CloudlessOS remains first class.** Portability must not weaken appliance recovery, kiosk,
   signed updates, DGX Spark, or recipe security.

## Target architecture

```text
Native browser or lightweight Cloudless Desktop launcher
                         |
                         | loopback HTTP / SSE / WebSocket
                         v
              Portable Cloudless control plane
       UI + API + jobs + recipes + models + gateway
                         |
                         | authenticated typed host protocol
                         v
                 Signed Cloudless host bridge
       GPU facts | container actions | storage | networking
                         |
                         v
        Host container runtime + NVIDIA GPU integration
                         |
                         v
             Existing recipe and engine containers
```

The portable control plane may be shipped as pinned Cloudless containers. The bridge remains a
small native service because runtime startup, GPU inspection, storage, updates, and recovery must
work even when the application container is stopped.

### Portable control plane

- Embedded interface and local API.
- Accounts and community recipe interactions.
- Recipe validation and lifecycle state machines.
- Model catalog, downloads, cache inventory, fit guidance, and selected-model state.
- Authenticated OpenAI-compatible model and agent gateway.
- Application catalog, persistent jobs, progress, audit, diagnostics, and preferences.
- Stable inference identity and fail-closed runtime promotion.

### Signed host bridge

- Detect edition, architecture, GPU, driver, memory, storage, and runtime.
- Execute allowlisted container, network, volume, and image actions without exposing a raw socket.
- Start, stop, repair, and update the portable Cloudless stack.
- Expose only approved storage roots after ownership, symlink, mount, and capacity checks.
- Integrate optional Tailscale and NFS where the host supports them.
- Produce bounded diagnostics and coordinate signed updates and rollback.

### Appliance-only capabilities

These remain outside the first Desktop release unless the host offers a safe equivalent:

- LightDM/Openbox kiosk and graphical-session recovery.
- OS installation, bootloader, display, keyboard, suspend, restart, and power ownership.
- NVIDIA driver installation/replacement and whole-OS updates.
- Factory reset and appliance first-boot behavior.

The shared UI hides or explains unavailable features using backend capability flags. There must not
be separate Windows and Linux frontend forks.

## Installation and removal

### Existing Linux

The first supported host is Ubuntu 24.04 amd64 with a compatible NVIDIA GPU and driver.

1. User downloads a signed Cloudless host package.
2. Preflight checks GPU, driver, Docker, NVIDIA Container Toolkit, ports, storage, and conflicts.
3. Installer adds the restricted host bridge and desktop launcher.
4. Bridge pulls digest-pinned Cloudless application images and opens the loopback dashboard.
5. The normal welcome and recipe-first flow begins without replacing the desktop session.

Cloudless may reuse an existing compatible Docker installation. Driver or runtime installation must
be separately explained and approved, never a hidden side effect.

### Windows

Windows uses WSL2 so Cloudless does not maintain a second native CUDA and recipe runtime.

1. User runs a signed Windows installer.
2. Preflight checks Windows, virtualization, disk, WSL2, NVIDIA GPU, and CUDA-for-WSL driver support.
3. Cloudless requests permission before enabling or installing optional Windows components.
4. Installer imports a versioned Cloudless-managed Linux environment without changing the user's
   default WSL distribution.
5. It starts the bridge and portable control plane and adds Start Menu shortcuts.
6. Cloudless opens in the default browser or a lightweight native launcher.

The managed distribution is an implementation detail. Windows commands must provide status,
repair, update, storage move, export, and uninstall. Model data defaults to the WSL ext4 disk for
performance; slow Windows-mounted paths require measured warnings.

### Uninstallation contract

- **Remove Cloudless, keep data:** remove launchers, services, and managed containers while keeping
  an exportable model/configuration location or WSL data disk.
- **Remove Cloudless and managed data:** also remove Cloudless-owned images, volumes, caches, state,
  and the managed distribution after showing the exact reclaimed size.

Neither option may remove user images, unrelated Docker resources, another WSL distribution, the
NVIDIA driver, or a shared host runtime without proof that Cloudless owns it exclusively.

## Recipe compatibility contract

Platform differences use capabilities rather than copied recipes. Before beta, define a
backward-compatible compatibility block or equivalent derived metadata covering:

- edition: `cloudless-os`, `linux-desktop`, `windows-wsl2`, `dgx-spark`;
- `amd64` or `arm64`, GPU architecture, and minimum driver/runtime;
- local/distributed topology and engine runtime;
- networking, NFS, Tailscale, IPC, shared-memory, and storage requirements;
- required appliance-only integrations.

Cloudless explains why a recipe is runnable, unavailable, or unqualified. Advanced mode cannot
bypass a physically impossible CPU, GPU, or topology requirement.

## Security invariants

- No unrestricted Docker/Podman socket reaches the control plane.
- Privileged requests use a typed allowlist with validated values and timeouts.
- Control and inference listeners remain loopback-only until exposure is explicitly enabled.
- Host paths are denied by default and admitted only through owned storage contracts.
- Existing digest, network, mount, capability, credential, and endpoint policies remain enforced.
- Windows credentials never enter WSL or recipe containers.
- Installers, packages, images, WSL filesystems, and updates are signed in one release manifest.
- Failed updates retain the previous runnable control plane and deterministic repair path.
- Support bundles redact Windows usernames, Linux home paths, identity, tokens, and credentials.

## Execution plan

### Phase 0 — Architecture proof (2–3 days)

- [ ] Record the control-plane/host-bridge architecture decision.
- [ ] Inventory every direct host, Docker, systemd, kiosk, storage, updater, terminal, browser,
  Tailscale, NFS, and DGX dependency.
- [ ] Classify each as portable, bridge-owned, appliance-only, or deferred.
- [ ] Define the authenticated bridge protocol and persistent-data ownership.
- [ ] Threat-model compromised control-plane and malicious-recipe scenarios.

**Exit:** The reviewed boundary requires neither a raw Docker socket nor arbitrary root execution,
and host recovery survives a stopped/replaced control plane.

### Phase 1 — Portable runtime (about 1 week)

- [ ] Add a host-capability interface and move direct host assumptions behind it.
- [ ] Package reproducible, digest-pinned multi-architecture control-plane images.
- [ ] Implement the Linux bridge using existing restricted broker/admission code where possible.
- [ ] Gate appliance-only UI through server capabilities.
- [ ] Define data migration and add a complete developer launcher.

**Exit:** On Ubuntu without kiosk services, one reviewed recipe starts, reaches the stable gateway,
stops, and survives control-plane restart through the real bridge and a tested fake bridge.

### Phase 2 — Linux developer preview (about 1 week)

- [ ] Build signed Ubuntu host packaging and full hardware/runtime preflight.
- [ ] Implement install, status, repair, logs, update, rollback, and both uninstall modes.
- [ ] Reuse recipes, cache, apps, API, Tailscale, and NFS only when capabilities pass.
- [ ] Add a desktop launcher without replacing the session.
- [ ] Validate backup/restore and migration between Desktop installations.

**Exit:** A clean Ubuntu host can install, run a representative recipe, reboot, update, roll back,
export diagnostics, and uninstall without altering unrelated runtime resources or drivers.

### Phase 3 — Public Linux beta (1–2 additional weeks)

- [ ] Test supported GPUs/drivers and common pre-existing Docker states.
- [ ] Exercise interrupted install/pull/download/update, disk full, conflicts, crashes, and reboot.
- [ ] Add clear unsupported-host and unavailable-appliance UX.
- [ ] Publish installation, storage, firewall, backup, repair, and uninstall guides.
- [ ] Bind host packages and images into signed dual-architecture release evidence.

**Exit:** Linux passes clean install, upgrade, rollback, recovery, recipes/API, security,
diagnostics, and non-destructive uninstall with no known host-ownership or data-loss defect.

### Phase 4 — Windows/WSL2 prototype (1–2 additional weeks)

- [ ] Build a signed Windows bootstrapper and lightweight launcher.
- [ ] Safely detect/enable WSL2 and import a versioned managed distribution.
- [ ] Prove GPU containers without requiring Docker Desktop when the managed runtime owns Docker.
- [ ] Implement Windows-to-WSL health, launch, stop, logs, repair, update, and URL opening.
- [ ] Define firewall/loopback behavior and storage sizing, move, export, and removal.

**Exit:** Windows installs without dual boot, runs a reviewed recipe on NVIDIA, survives reboot, and
removes only Cloudless-owned WSL and application artifacts.

### Phase 5 — Windows beta (1–2 additional weeks)

- [ ] Test supported Windows/WSL/driver versions, sleep, disk pressure, antivirus, and updates.
- [ ] Cover no-WSL, existing distros, Docker Desktop, VPN, port conflict, custom storage, and
  interrupted-install cases.
- [ ] Add repair flows for virtualization, WSL kernel, GPU passthrough, disk, and container failure.
- [ ] Add signed launcher/WSL updates and rollback plus public operations documentation.

**Exit:** The Windows matrix proves installation, GPU inference, recipes, API, update/rollback,
recovery, backup/export, and non-destructive uninstall.

### Phase 6 — Production hardening (2–4 additional weeks)

- [ ] Complete external review of installer, bridge, updates, WSL, admission, and local exposure.
- [ ] Run long model, recipe, restart, update, and storage-pressure soaks.
- [ ] Prove rollback and emergency hotfixes independently for each delivery mode.
- [ ] Define supported versions, lifecycle, deprecation, and support ownership.
- [ ] Complete accessibility, localization, installer UX, and failure-message review.

**Exit:** Windows and Linux meet the signed-release, recovery, security, lifecycle, and support
standards advertised for their capabilities, with retained edition-specific evidence.

## Minimum qualification matrix

Every applicable edition must retain evidence for clean install/first launch; NVIDIA runtime;
managed and advanced recipes; signed community admission; model resume/cache reuse; authenticated
gateway; explicit network exposure; reboot/crash recovery; update/rollback/interruption;
backup/restore; support redaction; and preservation of unrelated host resources. Desktop editions
also require tested keep-data and delete-data uninstall paths.

Representative coverage includes a managed container, an advanced immutable container, a large
resumable download, launch failure with rollback, and a successful stable OpenAI request.

## Release strategy

- Use one product version and source commit across appliance packages, Linux packages, Windows
  launcher, WSL image, and portable images released together.
- Sign every artifact and record digest, size, architecture, commit, and compatibility.
- Pin Cloudless images by digest; mutable tags are never the signed authority.
- Keep the prior stack until the replacement passes the stable health contract.
- Cloudless Desktop never replaces the host OS updater or NVIDIA driver authority.

## Major risks

| Risk | Mitigation |
|---|---|
| Docker socket turns compromise into host root | Typed bridge; never mount the socket |
| WSL GPU/driver mismatch | Fail-fast preflight and explicit compatibility guidance |
| Slow Windows-mounted models | Default ext4 data disk; benchmarked warnings and move flow |
| Existing Docker/WSL conflicts | Inventory first; namespace ownership; refuse unsafe takeover |
| Edition behavior diverges | Capability backend, one UI, shared contract tests |
| Partial multi-artifact update | One signed manifest, handshake, atomic promotion, rollback |
| Uninstall deletes user data | Ownership markers, exact deletion plan, keep-data default |
| Appliance regressions | Mandatory CloudlessOS regression matrix for every release |

## Deferred

- Native Windows CUDA inference outside WSL2.
- macOS local inference and non-NVIDIA accelerators.
- Kubernetes, multi-tenant orchestration, and generic remote Docker endpoints.
- Rootless-runtime claims before full GPU/network/storage parity.
- Automatic NVIDIA driver replacement on a user-managed OS.
- Appliance display/power/OS controls without safe host equivalents.
- Linux distributions beyond Ubuntu before Ubuntu lifecycle evidence is complete.

## Resume checklist

1. Create the Phase 0 architecture decision and dependency inventory.
2. Mark every direct host call in `cloudlessd`, brokers, package scripts, and UI.
3. Define the smallest bridge contract needed for one reviewed recipe on existing Ubuntu.
4. Prototype through the restricted broker; do not mount Docker into the control plane.
5. Prove Linux recipe launch and stable gateway before starting the Windows installer.
6. Update this roadmap's checkboxes and implementation notes with each completed item.

The first deliverable is the reviewed host-boundary contract—not a monolithic container or Windows
installer. That boundary is what allows Linux and managed WSL2 to reuse one safe runtime.
