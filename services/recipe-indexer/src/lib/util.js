const encoder = new TextEncoder();

export function nowISO() {
  return new Date().toISOString();
}

export function stableValue(value) {
  if (Array.isArray(value)) return value.map(stableValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, stableValue(value[key])]));
  }
  return value;
}

export function stableStringify(value) {
  return JSON.stringify(stableValue(value));
}

export async function sha256Hex(value) {
  const bytes = typeof value === "string" ? encoder.encode(value) : value;
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

export function bytesToBase64(bytes) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

export function base64ToBytes(value) {
  const binary = atob(value.replace(/\s+/g, ""));
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

export function slugify(value) {
  return String(value || "recipe")
    .normalize("NFKD")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64) || "recipe";
}

export async function sourceIdentity(repository, manifestPath) {
  const canonical = `${repository.toLowerCase()}:${manifestPath.toLowerCase()}`;
  return `source_${(await sha256Hex(canonical)).slice(0, 24)}`;
}

export async function recipeIdentity(name, repository, manifestPath) {
  const digest = await sha256Hex(`${repository.toLowerCase()}:${manifestPath.toLowerCase()}`);
  return `${slugify(name)}-${digest.slice(0, 12)}`;
}

export function jsonResponse(value, status = 200, headers = {}) {
  return new Response(JSON.stringify(value), {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "access-control-allow-origin": "*",
      "cache-control": status === 200 ? "public, max-age=60" : "no-store",
      ...headers
    }
  });
}

export function errorResponse(status, code, message, details) {
  return jsonResponse({ error: { code, message, ...(details ? { details } : {}) } }, status, { "cache-control": "no-store" });
}

export function boundedString(value, max = 1000) {
  return String(value || "").trim().slice(0, max);
}

export function scalarObject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  return Object.fromEntries(Object.entries(value).filter(([, item]) => ["string", "number", "boolean"].includes(typeof item)).map(([key, item]) => [key, String(item)]));
}

export function asPositiveInteger(value, fallback, maximum = Number.MAX_SAFE_INTEGER) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) && parsed > 0 && parsed <= maximum ? parsed : fallback;
}

export function uniqueStrings(values, limit = 32) {
  return [...new Set((Array.isArray(values) ? values : []).map((value) => boundedString(value, 80).toLowerCase()).filter(Boolean))].slice(0, limit);
}

export function pathLooksLikeRecipe(path) {
  const lower = String(path || "").toLowerCase();
  const base = lower.split("/").pop();
  if (base.startsWith("readme.") || base.startsWith("license.") || base === "mkdocs.yml") return false;
  return base === "cloudless.recipe.yaml" || base === "cloudless.recipe.yml" || base === "cloudless.recipe.json" || base === "sparkrun.yaml" || base === "sparkrun.yml" ||
    ((lower.includes("/recipes/") || lower.startsWith("recipes/") || lower.includes("official-recipes/") || lower.includes("experimental-recipes/")) && (lower.endsWith(".yaml") || lower.endsWith(".yml")));
}

export function normalizeError(error) {
  if (error instanceof Error) return error.message.slice(0, 2000);
  return String(error).slice(0, 2000);
}
