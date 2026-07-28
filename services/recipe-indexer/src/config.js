export const SPARKRUN_PROVIDER_VERSION = "0.2.40";

export const DEFAULT_REGISTRIES = Object.freeze([
  { name: "official", owner: "spark-arena", repository: "recipe-registry", ref: "main", directory: "official-recipes", trust: "upstream-official" },
  { name: "experimental", owner: "spark-arena", repository: "recipe-registry", ref: "main", directory: "experimental-recipes", trust: "discovered" },
  { name: "community", owner: "spark-arena", repository: "community-recipe-registry", ref: "main", directory: "recipes", trust: "community" },
  { name: "eugr", owner: "eugr", repository: "spark-vllm-docker", ref: "main", directory: "recipes", trust: "community" },
  { name: "atlas", owner: "Avarok-Cybersecurity", repository: "atlas-recipes", ref: "main", directory: "recipes", trust: "community" },
  { name: "sparkrun-transitional", owner: "dbotwinick", repository: "sparkrun-recipe-registry", ref: "main", directory: "transitional/recipes", trust: "community" },
  { name: "sparkrun-testing", owner: "dbotwinick", repository: "sparkrun-recipe-registry", ref: "main", directory: "testing/recipes", trust: "discovered" }
]);

export const DEFAULT_GITHUB_SEARCHES = Object.freeze([
  { kind: "code", query: "filename:cloudless.recipe.yaml" },
  { kind: "code", query: "filename:cloudless.recipe.yml" },
  { kind: "code", query: "filename:cloudless.recipe.json" },
  { kind: "code", query: "filename:sparkrun.yaml" },
  { kind: "code", query: "filename:sparkrun.yml" },
  { kind: "repositories", query: "topic:cloudless-recipe" },
  { kind: "repositories", query: "topic:sparkrun-recipe" }
]);

function parseArray(value, fallback, label) {
  if (!value) return fallback.map((item) => ({ ...item }));
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch (error) {
    throw new Error(`${label} must contain a JSON array: ${error.message}`);
  }
  if (!Array.isArray(parsed)) throw new Error(`${label} must contain a JSON array`);
  return parsed;
}

export function getConfig(env) {
  return {
    environment: env.ENVIRONMENT || "development",
    publicBaseURL: String(env.PUBLIC_BASE_URL || "http://127.0.0.1:8787").replace(/\/$/, ""),
    githubToken: env.GITHUB_TOKEN || "",
    githubAPIVersion: env.GITHUB_API_VERSION || "2022-11-28",
    registries: parseArray(env.SPARKRUN_REGISTRIES_JSON, DEFAULT_REGISTRIES, "SPARKRUN_REGISTRIES_JSON"),
    searches: parseArray(env.GITHUB_SEARCH_QUERIES_JSON, DEFAULT_GITHUB_SEARCHES, "GITHUB_SEARCH_QUERIES_JSON"),
    autoPublish: String(env.AUTOPUBLISH_DISCOVERED || "true").toLowerCase() === "true",
    adminToken: env.ADMIN_TOKEN || "",
    signingKeyID: env.CATALOG_KEY_ID || "cloudless-recipes-development",
    signingPrivateKey: env.CATALOG_SIGNING_PRIVATE_KEY || "",
    signingPublicKey: env.CATALOG_SIGNING_PUBLIC_KEY || ""
  };
}
