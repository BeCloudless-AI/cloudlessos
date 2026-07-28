import { SUPPORTED_ENGINES } from "./normalize.js";

const SEVERITY = { info: 0, low: 1, medium: 2, high: 3, blocked: 4 };

function finding(severity, code, message) {
  return { severity, code, message };
}

function containsShellRisk(command) {
  const normalized = String(command || "").toLowerCase();
  return [
    [/(curl|wget)[^|\n]*\|\s*(sh|bash)/, "PIPE_TO_SHELL", "Downloads code and pipes it directly into a shell"],
    [/rm\s+-rf\s+\/(?:\s|$)/, "DELETE_ROOT", "Attempts to recursively delete the host root filesystem"],
    [/(^|\s)(nsenter|chroot)\s/, "HOST_NAMESPACE", "Requests direct host namespace access"],
    [/--privileged(?:\s|$)/, "PRIVILEGED_FLAG", "Requests a privileged container from the command line"],
    [/\/var\/run\/docker\.sock/, "DOCKER_SOCKET", "Requests access to the host Docker socket"]
  ].filter(([pattern]) => pattern.test(normalized)).map(([, code, message]) => finding("blocked", code, message));
}

function mountFindings(mounts) {
  const result = [];
  for (const mount of mounts || []) {
    const host = String(mount).split(":", 1)[0].trim();
    if (["/", "/etc", "/boot", "/proc", "/sys", "/dev", "/var/run/docker.sock"].includes(host)) {
      result.push(finding("blocked", "SENSITIVE_HOST_MOUNT", `Mounts sensitive host path ${host}`));
    } else if (host.startsWith("/")) {
      result.push(finding("high", "HOST_MOUNT", `Mounts host path ${host}`));
    }
  }
  return result;
}

export function validateRecipe(recipe) {
  const findings = [];
  if (!SUPPORTED_ENGINES.has(recipe.runtime.engine)) findings.push(finding("high", "UNSUPPORTED_ENGINE", `Cloudless does not have a validated ${recipe.runtime.engine || "unknown"} execution adapter`));
  if (!/@sha256:[a-f0-9]{64}$/i.test(recipe.runtime.container)) findings.push(finding("medium", "UNPINNED_CONTAINER", "Container image is not pinned to an immutable digest"));
  if (!recipe.model.revision || ["main", "master", "latest"].includes(recipe.model.revision.toLowerCase())) findings.push(finding("medium", "UNPINNED_MODEL", "Model revision is mutable"));
  if (recipe.declaredPermissions.privileged) findings.push(finding("blocked", "PRIVILEGED_CONTAINER", "Recipe requests a privileged container"));
  findings.push(...mountFindings(recipe.declaredPermissions.hostMounts));
  findings.push(...containsShellRisk(recipe.runtime.command));
  if (recipe.declaredPermissions.hostCommands > 0) findings.push(finding("high", "HOST_COMMANDS", "Recipe runs commands directly on Spark hosts"));
  if (recipe.declaredPermissions.containerCommands > 0) findings.push(finding("medium", "LIFECYCLE_COMMANDS", "Recipe runs additional lifecycle commands inside containers"));
  if (recipe.declaredPermissions.devices.length > 0) findings.push(finding("medium", "EXPLICIT_DEVICES", "Recipe requests explicit host devices"));
  if (recipe.requirements.minNodes > 8) findings.push(finding("high", "LARGE_CLUSTER", "Recipe requires more than eight DGX Sparks and cannot be routinely validated"));
  if (recipe.upstream.unknownFields.length > 0) findings.push(finding("low", "UNKNOWN_FIELDS", `Preserved unknown fields: ${recipe.upstream.unknownFields.join(", ")}`));

  const highest = findings.reduce((current, item) => Math.max(current, SEVERITY[item.severity]), 0);
  const risk = Object.entries(SEVERITY).find(([, value]) => value === highest)?.[0] || "info";
  let status = "passed";
  if (highest === SEVERITY.medium) status = "warning";
  if (highest === SEVERITY.high) status = "review";
  if (highest === SEVERITY.blocked) status = "blocked";
  return {
    schema: "cloudless.recipe-validation/v1",
    status,
    risk,
    findings,
    staticChecks: {
      schema: "passed",
      sourcePinned: /^[a-f0-9]{40,64}$/i.test(recipe.source.revision) ? "passed" : "warning",
      containerPinned: /@sha256:[a-f0-9]{64}$/i.test(recipe.runtime.container) ? "passed" : "warning",
      supportedEngine: SUPPORTED_ENGINES.has(recipe.runtime.engine) ? "passed" : "review"
    },
    hardware: { status: "untested", testedPlatforms: [] }
  };
}

export function publicationVisibility(validation, autoPublish) {
  if (validation.status === "blocked") return "blocked";
  if (validation.status === "review" || !autoPublish) return "review";
  return "public";
}
