# CloudlessOS release qualification

The authoritative physical target list is
`distro/release/physical-validation-matrix.json`. A release candidate is not physically qualified
because it booted once or worked on one Spark. The retained evidence must cover VirtualBox, one
generic NVIDIA PC, one Spark, and every cluster size from two through eight Sparks.

Create an evidence directory named with the candidate version and source commit:

```text
qualification/<version>-<commit>/
  virtualbox-amd64/
  generic-nvidia-amd64/
  dgx-spark-arm64-1/
  ...
  dgx-spark-arm64-8/
```

For every target, retain:

- machine/VM configuration, firmware, kernel, NVIDIA driver and package versions;
- ten numbered cold-boot/restart results and a support bundle for every failed or repaired boot;
- screenshots for graphical login, display scaling, locale/timezone, virtual keyboard, browser,
  terminal and power controls;
- app install/open/restart/update/uninstall results;
- model load, switch, Abort and unload results with operation IDs;
- update, deliberate interrupted-update rollback, encrypted backup restore and post-reboot checks;
- the final redacted support bundle containing boot health and security audit evidence.

For every multi-Spark target, additionally retain discovery/enrollment, cable and SSH preflight,
selected topology, distributed load, peer loss, disconnect/reconnect and credential-rebind results.
The representative `dgx-spark-arm64-2` campaign additionally expands seven failure domains across
nine durable inference phases into checks named
`cluster-failure--<failure>--<phase>`. Record a healthy baseline before each fault, inject only the
named fault, capture the operation ID and node state, then prove either recovery or a truthful,
abortable degraded state. Other cluster sizes retain their topology and peer-loss checks without
repeating this 63-case representative matrix. Never put passwords, private keys, API
tokens, Tailscale authorization URLs or unredacted application databases in qualification
artifacts.

The `cloudless-firstboot` package installs the matrix and the `cloudless-qualify` evidence runner.
Start a campaign on the machine being tested:

```bash
sudo cloudless-qualify begin dgx-spark-arm64-2 0.2.7 \
  --operator "Release operator" \
  --output /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2
```

The runner refuses a target whose architecture or platform does not match the current machine.
The updater retains the exact source commit only after verifying it against the signed APT release
metadata. `begin` uses that identity only when its installed version exactly matches the requested
version. For a fresh ISO that has not yet established updater identity, append the full
40-character release commit after the version; abbreviated commits are never accepted.
After every distinct cold boot or restart, record the kernel-generated boot identity:

```bash
sudo cloudless-qualify boot /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2
```

Collect read-only machine details and the locally generated, redacted support ZIP:

```bash
sudo cloudless-qualify collect /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2
```

Record each observed check with a screenshot, exported operation result or log. Evidence is copied
into the campaign, mode `0600`, and SHA-256 bound to its result:

```bash
sudo cloudless-qualify record \
  /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2 \
  graphical-session pass \
  --note "Kiosk reached the Cloudless desktop without a TTY switch" \
  --evidence /home/cloudless/Downloads/graphical-session.png
```

`ten-boot-cycles` cannot be manually attested: it is calculated from ten different kernel boot IDs.
Every other applicable check requires at least one evidence file. Multi-Spark campaigns
automatically require the complete cluster check set. Credential-shaped notes and text/ZIP evidence
fail closed instead of being retained.

At any time, list missing or failed checks:

```bash
sudo cloudless-qualify status /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2
```

You do not need to memorize the checklist. Show the complete resumable plan or ask for the next
unfinished action:

```bash
sudo cloudless-qualify plan /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2
sudo cloudless-qualify next /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2
```

`next` explains the observation to make and prints the exact command shape for retaining its
evidence. A recorded failure stays visible; missing or digest-changed evidence is reported as
`CHECK`, never silently treated as complete. Add `--json` to either command for a future GUI or
external qualification controller.

Only a complete campaign receives `qualification-result.json`. The result binds the campaign,
check records, boot records and every evidence digest. A later failed validation removes the stale
result rather than leaving an invalid campaign looking qualified.

Once `status` reports `QUALIFIED`, seal the complete campaign into one atomic, self-verifying
archive. Write the archive outside the campaign directory so it cannot become part of its own
evidence inventory:

```bash
sudo cloudless-qualify export \
  /var/lib/cloudless/qualification/0.2.7-01234567/dgx-spark-arm64-2 \
  --output /var/lib/cloudless/qualification/0.2.7-01234567-dgx-spark-arm64-2.zip
sudo cloudless-qualify verify-export \
  /var/lib/cloudless/qualification/0.2.7-01234567-dgx-spark-arm64-2.zip
```

Export refuses incomplete or changed campaigns, unsafe paths, symlinks and oversized payloads. It
writes through a private temporary file, verifies every member against the export inventory and the
sealed campaign evidence root, and only then atomically publishes the final mode-0600 ZIP. Retain
that ZIP beside the CI artifacts for the exact release commit.

Run the tooling contract validator before and after a qualification campaign:

```bash
bash distro/scripts/test-qualification-contract.sh
```

The current workflow validates the matrix shape, but humans must still execute and review the
physical campaign. A release manager may reject incomplete evidence; an ordinary known-limitations
note cannot waive a failed security, rollback, boot or data-integrity check.

Candidate soak, promotion, rollback retention and support targets are defined in
[RELEASE_POLICY.md](./RELEASE_POLICY.md).
