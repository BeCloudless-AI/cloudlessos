# CloudlessOS user guide

This guide describes the current CloudlessOS interface on generic NVIDIA computers and NVIDIA
DGX Spark. Features that only apply to Spark are hidden on other systems by server-provided
capabilities; they are not separate copies of the interface.

## Desktop and navigation

The top bar contains the Cloudless logo, the clock, NVIDIA identification on DGX Spark, and the
power control. The power control opens a confirmation window with separate **Restart** and
**Shutdown** actions. Application shortcuts belong in the left launcher rather than being repeated
in the top bar.

The bottom desktop area combines the selected-model state and its metrics. The desktop metrics
control appears only while a model is loaded; the permanent sidebar Metrics entry remains
available. During model loading, Cloudless shows one durable operation with its phase, per-node
detail, percentage and estimated remaining time. The operation continues if Model Manager closes.

**Places** opens the Models, Outputs, Workspace and Downloads directories. The launcher separates
these fixed system locations from user-pinned applications. Only installed applications can be
pinned. Installed applications open inside a Cloudless-managed window so the kiosk remains the
desktop experience.

## Appearance and display

Settings provides themes, animation, interface scale and display resolution. Cloudless selects an
initial scale from the detected display and allows accessibility scaling up to 300%. Resolution
changes use a confirmation timer and revert if they are not confirmed. DGX Spark defaults to
reduced animation because large translucent animations can contend with appliance graphics;
animations can be enabled with the warning shown in Settings.

The Cloudless launch animation plays once per OS boot, not once per browser reload. It fades to the
live desktop. The welcome tour uses one blurred overlay and only permits the highlighted action;
assistant input is locked during demonstration steps. The mouse pointer remains visible and stable;
Cloudless disables Ubuntu's idle `unclutter` process because noisy mice made foreground windows
appear to blink.

## Models and engines

Model Manager separates language models, image models and recipes. The curated catalog provides
fit guidance based on the detected platform, architecture, memory and active cluster. On DGX Spark,
memory is unified memory rather than conventional dedicated VRAM. A cluster fit result only uses
selected, healthy nodes and a model/runtime profile that supports that topology.

Hugging Face search spans the Model Manager width. Connecting a Hugging Face account allows access
to repositories the account is authorized to download. Model downloads belong to the daemon, not
the modal: progress survives navigation, refresh and orchestrator restart, and partial data remains
available for a safe resume.

Settings exposes the shared **Engine** and **API access** views also used by Metrics; the views are
not duplicated implementations. Managed vLLM, SGLang and llama.cpp use signed platform-aware
definitions. Advanced users can register an already-built local vLLM or SGLang container under
**Engine > Custom engine builds**. Cloudless never treats a local image as part of its signed update
channel.

Cloudless keeps one stable internal inference contract regardless of engine or recipe. The private
model identity is `cloudless`; clients use the editable authenticated identity and port shown in
**Settings > API access**.

## Cloudless Assistant and Hermes Agent

The desktop assistant is powered by Hermes Agent and the model selected through Cloudless Model
Manager. Hermes is a core, non-removable platform service. Resetting Hermes restores the
Cloudless-managed model/provider block; it does not select an unrelated external provider.

Questions about model compatibility and installation are answered through Cloudless hardware,
catalog and Model Manager data. Hermes must not invent installation skills, run package-manager
workarounds or bypass Cloudless lifecycle policy. Streaming updates one message in place to avoid
text blinking.

## Applications

The App Launcher shows installed applications and optional applications supported on the current
architecture. Applications that are not installed are not pinnable. Install, uninstall and
confirmation flows remain inside the Cloudless interface rather than using browser prompts.

Application settings only list installed applications and non-removable platform capabilities.
External application dashboards and links open in the Cloudless window/browser model instead of
escaping the kiosk. Application availability, container images and update comparisons come from
the signed, platform-aware Cloudless manifest.

## Browser, terminal, keyboard and remote access

Cloudless Browser is a persistent foreground window. Minimizing it creates a launcher item, and
clicking the kiosk or Terminal does not bury the Browser behind the desktop. Browser downloads use
the normal Downloads place and the browser profile survives restarts.

Cloudless Terminal is an unrestricted authenticated host terminal for advanced users. It supports
floating/full-screen presentation and up to eight persistent tabs. Hiding or reopening Terminal
does not reset its sessions. The optional `cloudless-developer-tools` command installs the source
build toolchain without making experimental builds part of managed Cloudless state.

The Cloudless virtual keyboard handles native Cloudless fields and uses the authenticated desktop
input bridge for embedded application and terminal frames. Cross-origin browser pages do not expose
their input DOM to Cloudless JavaScript; system-level typing is used where the graphical session
permits it rather than installing a second on-screen keyboard.

Tailscale can be installed and connected from Settings. Cloudless exposes explicit identity,
login, serve and logout states and keeps Terminal available as the recovery path. Public or LAN
exposure always requires a user action.

## Updates

Settings shows the installed version, channel, available version and verified changelog. The
overall progress bar appears only after an update begins. The package worker runs outside the
interface process, preserves the previous Cloudless generation, installs the complete signed
generation, restarts affected services, verifies health and rolls back on failure. A restart banner
clears only after the installed generation and boot state agree.

The same signed repository carries AMD64 and ARM64 Cloudless packages. On DGX Spark, NVIDIA remains
the owner of DGX OS, the kernel, firmware, CUDA and drivers. Cloudless does not replace them with a
generic Ubuntu driver update.

## DGX Spark and clusters

Spark Settings adds the DGX Dashboard and cluster management. **Connect now** opens the guided
cluster window; a configured cluster shows **Manage**. The wizard discovers peers, verifies
credentials and host keys, explains the required cable/fabric, tests both routes and applies the
network configuration with a durable operation and rollback.

The desktop reports every selected Spark separately, including unified-memory, utilization and
transfer/loading progress. One coordinator plus one worker is the supported distributed target;
three through eight systems are preview and require a switched RoCE fabric and operator
qualification. Disconnect first stops and verifies cluster-owned inference. An unreachable peer
remains a visible cleanup obligation rather than being reported as successfully stopped.

## Recipes

The Recipe Library has two explicit collections:

- **Runnable** contains executable compatibility profiles admitted by the installed Cloudless
  package, plus constrained declarative container recipes that pass policy.
- **Drafts** contains editable local definitions. Drafts can be inspected, edited and removed but
  arbitrary host or Docker commands are not executed.

“Cloudless reviewed” does not mean a remote service is reviewing a recipe in the background. It
means the exact recipe definition was inspected, tested, added to Cloudless source and delivered
inside the installed package authenticated by the Cloudless archive signature. Editing an
executable field breaks that exact match and turns the saved copy into a draft.

A runnable recipe follows this sequence:

1. Install the recipe's exact model revision in Model Manager.
2. Select **Validate** to test this recipe revision on the current Spark topology without replacing
   the active model.
3. Select **Run recipe** after validation passes.

Preparation runs in the background and reports coordinator/worker phases, byte progress and ETA.
Abort stops the operation while retaining verified reusable downloads. Cloudless promotes a recipe
only after the stable `/v1/models` endpoint returns the required internal `cloudless` identity.

## Cloudless Doctor

Run **Settings > Cloudless Doctor** when the interface, model lifecycle or cluster state looks
wrong. Doctor performs read-only service, Docker, accelerator, storage, promotion and cluster
checks. It also compares declared inference state with running recipe containers.

If Cloudless says inference is unloaded while a known recipe container still runs, Doctor shows
**Orphaned inference runtime** and offers **Stop orphaned runtime**. The repair removes only exact
containers matched to that saved recipe on the coordinator and selected workers. It does not run
the unreviewed recipe, delete model weights or issue arbitrary Docker commands. A repair remains in
an attention state until every reachable node proves the targeted runtime is absent.

**Save support bundle** creates a redacted ZIP locally and never uploads it automatically. Review
the ZIP before sharing it.

## More information

- [Current validated status](./STATUS.md)
- [DGX Spark cluster operation](./CLUSTER.md)
- [Local inference recipes](./LOCAL_RECIPES.md)
- [Support and diagnostics](./SUPPORT_AND_DIAGNOSTICS.md)
- [Updates and rollback](../distro/UPDATES.md)

