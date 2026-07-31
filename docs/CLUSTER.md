# DGX Spark cluster operation

CloudlessOS supports one coordinator plus one-to-seven enrolled DGX Spark workers. Every Spark must
run a compatible signed Cloudless generation and NVIDIA DGX OS stack. Connect the supported
high-speed fabric cables, keep ordinary network connectivity available for discovery/administration,
and confirm time synchronization before enrollment.

In **Settings → DGX Spark → Spark cluster**:

1. Choose **Connect now** or **Manage**.
2. Discover the peer and verify its hostname/address instead of trusting display order.
3. Provide that peer's normal OS account only for enrollment. Cloudless creates its own cluster SSH
   identity and pins the host key; the password is not stored in the recipe or audit.
4. Run cable, route, SSH, GPU/runtime, storage and version preflight.
5. Select the intended nodes and connect. Wait for the operation to finish before loading a
   distributed model.

Repeat enrollment for additional workers. The selected topology, not merely every visible peer,
determines distributed inference placement. A disconnected or unselected peer must not inflate
usable memory in Model Manager.

Before a distributed load:

- require every selected node and accelerator telemetry to be reachable;
- verify model and immutable runtime compatibility for the exact node count;
- confirm enough storage on every node—the fabric does not make model downloads instantaneous;
- keep the stable Cloudless private model name and gateway port rather than exposing worker ports.

If a peer is lost, Cloudless marks the selected compute topology degraded. Abort the active
operation or unload the distributed model before changing membership. After reconnecting a cable or
rebooting a peer, run **Check connection** and require zero packet loss before retrying inference.

Disconnecting the cluster stops cluster-owned inference and returns the coordinator to a truthful
single-machine state. A model that only fits the cluster must remain unloaded; Cloudless must not
pretend it is still starting. Rebinding credentials or reversing coordinator/worker roles is an
explicit administration operation, never an automatic recovery side effect.

For support, save operation IDs and collect a separate redacted bundle from every reachable node
before deleting the cluster. The physical release matrix in
[RELEASE_QUALIFICATION.md](./RELEASE_QUALIFICATION.md) requires connect, selected-topology load,
peer loss, disconnect/reconnect and credential rebind for every two-to-eight-Spark size.
