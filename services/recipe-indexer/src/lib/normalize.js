import YAML from "yaml";
import { SPARKRUN_PROVIDER_VERSION } from "../config.js";
import { asPositiveInteger, boundedString, recipeIdentity, scalarObject, sha256Hex, slugify, uniqueStrings } from "./util.js";

const SUPPORTED_ENGINES = new Set(["vllm", "vllm-distributed", "vllm-ray", "sglang", "llama-cpp", "trtllm"]);

function parseDocument(bytes, manifestPath) {
  const text = new TextDecoder().decode(bytes);
  let document;
  try {
    document = manifestPath.toLowerCase().endsWith(".json") ? JSON.parse(text) : YAML.parse(text);
  } catch (error) {
    throw new Error(`parse recipe document: ${error.message}`);
  }
  if (!document || typeof document !== "object" || Array.isArray(document)) throw new Error("recipe document must be an object");
  return { text, document };
}

function inferRuntime(runtime, command) {
  const declared = boundedString(runtime, 64).toLowerCase();
  if (declared) return declared;
  const normalized = boundedString(command, 4096).toLowerCase();
  if (normalized.startsWith("sglang serve") || normalized.includes("sglang.launch_server")) return "sglang";
  if (normalized.startsWith("llama-server")) return "llama-cpp";
  return "vllm";
}

function inferCategory(name, description, tags) {
  const text = `${name} ${description} ${(tags || []).join(" ")}`.toLowerCase();
  if (/vision|vlm|multimodal|image understanding/.test(text)) return "vision";
  if (/code|coder|programming/.test(text)) return "coding";
  if (/research|retrieval|rag|search/.test(text)) return "research";
  if (/image generation|diffusion|flux|stable diffusion/.test(text)) return "image-generation";
  if (/embedding|rerank/.test(text)) return "embeddings";
  return "chat";
}

function nodeRequirements(document, defaults) {
  let minNodes = asPositiveInteger(document.min_nodes, 1, 64);
  let maxNodes = asPositiveInteger(document.max_nodes, 64, 64);
  if (document.solo_only || String(document.mode || "").toLowerCase() === "solo") minNodes = maxNodes = 1;
  if (document.cluster_only || String(document.mode || "").toLowerCase() === "cluster") minNodes = Math.max(2, minNodes);
  const tensorParallel = asPositiveInteger(defaults.tensor_parallel, minNodes, 64);
  minNodes = Math.max(minNodes, tensorParallel);
  if (maxNodes < minNodes) throw new Error("max_nodes cannot be smaller than the required tensor parallel size");
  return { minNodes, maxNodes, tensorParallel };
}

function normalizeSparkRun(document, source, sourceDigest) {
  const defaults = scalarObject(document.defaults);
  const environment = scalarObject(document.env);
  const modelID = boundedString(document.model, 512);
  const container = boundedString(document.container, 512);
  const command = boundedString(document.command, 16_384);
  if (!modelID || !container || !command) throw new Error("SparkRun recipe requires model, container, and command");
  const runtime = inferRuntime(document.runtime, command);
  const { minNodes, maxNodes, tensorParallel } = nodeRequirements(document, defaults);
  const servedName = boundedString(defaults.served_model_name, 512) || modelID;
  const metadata = document.metadata && typeof document.metadata === "object" ? document.metadata : {};
  const name = boundedString(document.name || servedName.split("/").pop(), 100);
  const description = boundedString(metadata.description || document.description || "Discovered SparkRun inference recipe.", 2000);
  const tags = uniqueStrings([
    ...(Array.isArray(metadata.tags) ? metadata.tags : []),
    runtime,
    minNodes > 1 ? "multi-spark" : "single-spark",
    metadata.model_dtype,
    metadata.kv_dtype
  ]);
  const category = inferCategory(name, description, tags);
  return {
    schema: "cloudless.indexed-recipe/v1",
    id: "",
    slug: slugify(name),
    name,
    description,
    category,
    tags,
    format: "sparkrun/v2",
    trust: source.trust || "discovered",
    source: {
      kind: source.kind || "github",
      repository: source.repository,
      manifestPath: source.manifestPath,
      url: source.url,
      revision: source.revision,
      digest: sourceDigest,
      registry: source.registry || ""
    },
    artifact: { digest: sourceDigest, mediaType: "application/yaml", url: "" },
    provider: { name: "sparkrun", version: SPARKRUN_PROVIDER_VERSION, preservesOriginalDocument: true },
    runtime: { engine: runtime, container, command, environment },
    model: {
      id: modelID,
      revision: boundedString(document.model_revision || "main", 256),
      context: asPositiveInteger(defaults.max_model_len, 32768, 16_777_216),
      tensorParallel,
      pipelineParallel: asPositiveInteger(defaults.pipeline_parallel, 1, 64),
      quantization: boundedString(metadata.model_dtype || "", 64),
      kvCacheDtype: boundedString(metadata.kv_dtype || "", 64)
    },
    requirements: {
      platforms: ["dgx-spark"],
      architectures: ["arm64"],
      minNodes,
      maxNodes,
      memoryGB: asPositiveInteger(defaults.memory_gb, 0, 4096),
      storageGB: asPositiveInteger(defaults.storage_gb, 0, 65_536)
    },
    declaredPermissions: {
      network: "outbound",
      privileged: Boolean(document.executor_config?.privileged),
      hostMounts: Array.isArray(document.executor_config?.volumes) ? document.executor_config.volumes.map(String).slice(0, 64) : [],
      devices: Array.isArray(document.executor_config?.devices) ? document.executor_config.devices.map(String).slice(0, 64) : [],
      hostCommands: Array.isArray(document.post_commands) ? document.post_commands.length : 0,
      containerCommands: (Array.isArray(document.pre_exec) ? document.pre_exec.length : 0) + (Array.isArray(document.post_exec) ? document.post_exec.length : 0)
    },
    upstream: {
      recipeVersion: boundedString(document.recipe_version || "", 64),
      maintainer: boundedString(metadata.maintainer || "", 160),
      modelParameters: boundedString(metadata.model_params || "", 80),
      unknownFields: Object.keys(document).filter((key) => !SPARKRUN_FIELDS.has(key)).sort()
    }
  };
}

const SPARKRUN_FIELDS = new Set([
  "name", "description", "recipe_version", "model", "model_revision", "runtime", "container", "command", "min_nodes", "max_nodes", "mode", "solo_only", "cluster_only", "defaults", "env", "metadata", "mods", "builder", "executor_config", "pre_exec", "post_exec", "post_commands", "runtime_config", "benchmark", "distribution_config", "speculative_config"
]);

function normalizeCloudless(document, source, sourceDigest) {
  const metadata = document.metadata || {};
  const engine = document.engine || {};
  const model = document.model || {};
  const requirements = document.requirements || {};
  const permissions = document.permissions || {};
  const name = boundedString(metadata.name, 100);
  if (!name) throw new Error("Cloudless recipe metadata.name is required");
  if (!boundedString(model.id, 512) || !boundedString(engine.image, 512) || !boundedString(engine.command, 16_384)) {
    throw new Error("Cloudless recipe requires model.id, engine.image, and engine.command");
  }
  const tags = uniqueStrings(metadata.tags || []);
  return {
    schema: "cloudless.indexed-recipe/v1",
    id: "",
    slug: slugify(name),
    name,
    description: boundedString(metadata.description || "Cloudless inference recipe.", 2000),
    category: boundedString(metadata.category, 64) || inferCategory(name, metadata.description, tags),
    tags,
    format: "cloudless/v1",
    trust: source.trust || "discovered",
    source: { kind: source.kind || "github", repository: source.repository, manifestPath: source.manifestPath, url: source.url, revision: source.revision, digest: sourceDigest, registry: source.registry || "" },
    artifact: { digest: sourceDigest, mediaType: source.manifestPath.endsWith(".json") ? "application/json" : "application/yaml", url: "" },
    provider: { name: "cloudless", version: "1", preservesOriginalDocument: true },
    runtime: { engine: boundedString(engine.type, 64).toLowerCase(), container: boundedString(engine.image, 512), command: boundedString(engine.command, 16_384), environment: scalarObject(engine.environment) },
    model: { id: boundedString(model.id, 512), revision: boundedString(model.revision || "main", 256), context: asPositiveInteger(model.context, 32768, 16_777_216), tensorParallel: asPositiveInteger(model.tensorParallel, 1, 64), pipelineParallel: asPositiveInteger(model.pipelineParallel, 1, 64), quantization: boundedString(model.quantization, 64), kvCacheDtype: boundedString(model.kvCacheDtype, 64) },
    requirements: {
      platforms: uniqueStrings(requirements.platforms || ["dgx-spark"]),
      architectures: uniqueStrings(requirements.architectures || ["arm64"]),
      minNodes: asPositiveInteger(requirements.minNodes, 1, 64),
      maxNodes: asPositiveInteger(requirements.maxNodes, asPositiveInteger(requirements.minNodes, 1, 64), 64),
      memoryGB: asPositiveInteger(requirements.memoryGB, 0, 4096),
      storageGB: asPositiveInteger(requirements.storageGB, 0, 65_536)
    },
    declaredPermissions: { network: boundedString(permissions.network || "outbound", 32), privileged: Boolean(permissions.privileged), hostMounts: Array.isArray(permissions.hostMounts) ? permissions.hostMounts.map(String).slice(0, 64) : [], devices: Array.isArray(permissions.devices) ? permissions.devices.map(String).slice(0, 64) : [], hostCommands: 0, containerCommands: 0 },
    upstream: { recipeVersion: boundedString(metadata.version || "", 64), maintainer: boundedString(metadata.author || "", 160), modelParameters: "", unknownFields: Object.keys(document).filter((key) => !["schema", "metadata", "model", "engine", "requirements", "permissions"].includes(key)).sort() }
  };
}

export async function normalizeRecipe(bytes, source) {
  if (!(bytes instanceof Uint8Array) || bytes.byteLength === 0 || bytes.byteLength > 2 * 1024 * 1024) {
    throw new Error("recipe document must be between 1 byte and 2 MB");
  }
  const { document } = parseDocument(bytes, source.manifestPath);
  const sourceDigest = await sha256Hex(bytes);
  const cloudless = document.schema === "cloudless.recipe/v1" || document.kind === "CloudlessRecipe";
  const recipe = cloudless ? normalizeCloudless(document, source, sourceDigest) : normalizeSparkRun(document, source, sourceDigest);
  recipe.id = await recipeIdentity(recipe.name, source.repository, source.manifestPath);
  if (recipe.requirements.maxNodes < recipe.requirements.minNodes) throw new Error("recipe maxNodes cannot be smaller than minNodes");
  if (!SUPPORTED_ENGINES.has(recipe.runtime.engine)) recipe.tags = uniqueStrings([...recipe.tags, "unsupported-engine"]);
  return { recipe, document };
}

export { SUPPORTED_ENGINES };
