import { signCatalog } from "./lib/signing.js";
import { nowISO, sha256Hex, stableStringify } from "./lib/util.js";

const CATALOG_KEY = "v1/catalog.json";

export async function publishCatalog(env, repository, config) {
  const recipes = await repository.listPublicRecipes();
  const previous = Number.parseInt(await repository.getState("catalog_sequence", "0"), 10) || 0;
  const sequence = previous + 1;
  const payload = {
    schema: "cloudless.recipe-catalog/v1",
    sequence,
    generatedAt: nowISO(),
    recipeCount: recipes.length,
    recipes
  };
  const envelope = await signCatalog(payload, config);
  const body = stableStringify(envelope);
  const digest = await sha256Hex(body);
  const cacheControl = "public, max-age=300, stale-while-revalidate=3600";
  await env.RECIPE_BUCKET.put(CATALOG_KEY, body, {
    httpMetadata: { contentType: "application/json; charset=utf-8", cacheControl },
    customMetadata: { digest, sequence: String(sequence), keyId: config.signingKeyID }
  });
  await env.RECIPE_BUCKET.put("v1/catalog.json.sig", JSON.stringify(envelope.signature), {
    httpMetadata: { contentType: "application/json; charset=utf-8", cacheControl },
    customMetadata: { catalogDigest: digest, keyId: config.signingKeyID }
  });
  if (config.signingPublicKey) {
    await env.RECIPE_BUCKET.put(`v1/keys/${config.signingKeyID}.json`, JSON.stringify({
      schema: "cloudless.recipe-signing-key/v1",
      keyId: config.signingKeyID,
      algorithm: "Ed25519",
      encoding: "spki-base64",
      publicKey: config.signingPublicKey
    }), { httpMetadata: { contentType: "application/json; charset=utf-8", cacheControl: "public, max-age=86400" } });
  }
  await repository.setState("catalog_sequence", sequence);
  await repository.setState("catalog_digest", digest);
  await repository.setState("catalog_dirty", "false");
  await repository.setState("catalog_published_at", payload.generatedAt);
  return { sequence, digest, recipeCount: recipes.length, envelope };
}

export async function publishIfDirty(env, repository, config) {
  if (await repository.getState("catalog_dirty", "true") !== "true") return null;
  return publishCatalog(env, repository, config);
}

export async function readCatalog(env) {
  const object = await env.RECIPE_BUCKET.get(CATALOG_KEY);
  if (!object) return null;
  const body = await object.text();
  return { object, body, envelope: JSON.parse(body) };
}

export function catalogResponse(catalog) {
  const headers = new Headers({ "content-type": "application/json; charset=utf-8", "access-control-allow-origin": "*" });
  catalog.object.writeHttpMetadata?.(headers);
  headers.set("content-type", headers.get("content-type") || "application/json; charset=utf-8");
  headers.set("etag", catalog.object.httpEtag || `"${catalog.object.customMetadata?.digest || ""}"`);
  return new Response(catalog.body, { headers });
}

export function filterRecipes(recipes, url) {
  const query = String(url.searchParams.get("q") || "").trim().toLowerCase();
  const platform = String(url.searchParams.get("platform") || "").trim().toLowerCase();
  const architecture = String(url.searchParams.get("architecture") || "").trim().toLowerCase();
  const category = String(url.searchParams.get("category") || "").trim().toLowerCase();
  const engine = String(url.searchParams.get("engine") || "").trim().toLowerCase();
  const nodes = Number.parseInt(url.searchParams.get("nodes") || "0", 10);
  const compatibleOnly = url.searchParams.get("compatible") === "true";
  const limit = Math.min(100, Math.max(1, Number.parseInt(url.searchParams.get("limit") || "30", 10)));
  const offset = Math.max(0, Number.parseInt(url.searchParams.get("offset") || "0", 10));
  const filtered = recipes.filter((recipe) => {
    if (query) {
      const text = [recipe.name, recipe.description, recipe.model?.id, recipe.runtime?.engine, ...(recipe.tags || [])].join(" ").toLowerCase();
      if (!text.includes(query)) return false;
    }
    if (platform && !(recipe.requirements?.platforms || []).includes(platform)) return false;
    if (architecture && !(recipe.requirements?.architectures || []).includes(architecture)) return false;
    if (category && recipe.category !== category) return false;
    if (engine && recipe.runtime?.engine !== engine) return false;
    if (nodes > 0) {
      const supported = nodes >= recipe.requirements.minNodes && nodes <= recipe.requirements.maxNodes;
      if ((compatibleOnly && !supported) || (!compatibleOnly && recipe.requirements.minNodes !== nodes)) return false;
    }
    return true;
  });
  return { total: filtered.length, offset, limit, items: filtered.slice(offset, offset + limit) };
}

export { CATALOG_KEY };
