import { catalogResponse, filterRecipes, publishCatalog, publishIfDirty, readCatalog } from "./catalog.js";
import { getConfig } from "./config.js";
import { errorResponse, jsonResponse, normalizeError, sha256Hex } from "./lib/util.js";
import { processQueueBatch, scheduleDiscovery, scheduledTick } from "./pipeline.js";
import { RecipeRepository } from "./repository.js";

async function authorized(request, config) {
  if (!config.adminToken) return false;
  const supplied = request.headers.get("authorization")?.replace(/^Bearer\s+/i, "") || "";
  if (!supplied) return false;
  const [expectedDigest, suppliedDigest] = await Promise.all([sha256Hex(config.adminToken), sha256Hex(supplied)]);
  return expectedDigest === suppliedDigest;
}

async function serveR2(object, fallbackType = "application/octet-stream") {
  if (!object) return errorResponse(404, "NOT_FOUND", "The requested recipe artifact does not exist.");
  const headers = new Headers();
  object.writeHttpMetadata?.(headers);
  headers.set("content-type", headers.get("content-type") || fallbackType);
  headers.set("etag", object.httpEtag || `"${object.customMetadata?.digest || ""}"`);
  headers.set("access-control-allow-origin", "*");
  return new Response(object.body, { headers });
}

async function handleAPI(request, env) {
  const config = getConfig(env);
  const repository = new RecipeRepository(env.RECIPE_DB);
  const url = new URL(request.url);
  if (request.method === "OPTIONS") return new Response(null, { status: 204, headers: { "access-control-allow-origin": "*", "access-control-allow-methods": "GET,HEAD,OPTIONS", "access-control-allow-headers": "authorization,content-type" } });
  if (url.pathname === "/health" && request.method === "GET") {
    const sequence = await repository.getState("catalog_sequence", "0");
    return jsonResponse({ status: "ok", service: "cloudless-recipe-indexer", catalogSequence: Number(sequence) });
  }
  if (url.pathname === "/v1/catalog" && request.method === "GET") {
    let catalog = await readCatalog(env);
    if (!catalog) {
      await publishCatalog(env, repository, config);
      catalog = await readCatalog(env);
    }
    return catalogResponse(catalog);
  }
  if (url.pathname === "/v1/catalog.sig" && request.method === "GET") return serveR2(await env.RECIPE_BUCKET.get("v1/catalog.json.sig"), "application/json; charset=utf-8");
  if (url.pathname.startsWith("/v1/keys/") && request.method === "GET") return serveR2(await env.RECIPE_BUCKET.get(url.pathname.slice(1)), "application/json; charset=utf-8");
  if (url.pathname === "/v1/recipes" && request.method === "GET") {
    let catalog = await readCatalog(env);
    if (!catalog) {
      await publishIfDirty(env, repository, config);
      catalog = await readCatalog(env);
    }
    const payload = catalog?.envelope?.payload || { sequence: 0, recipes: [] };
    return jsonResponse({ schema: "cloudless.recipe-search/v1", catalogSequence: payload.sequence, ...filterRecipes(payload.recipes || [], url) });
  }
  const recipeMatch = url.pathname.match(/^\/v1\/recipes\/([a-z0-9-]{3,100})$/);
  if (recipeMatch && request.method === "GET") {
    const recipe = await repository.getRecipe(recipeMatch[1]);
    return recipe ? jsonResponse(recipe) : errorResponse(404, "RECIPE_NOT_FOUND", "This recipe is not published.");
  }
  const artifactMatch = url.pathname.match(/^\/v1\/artifacts\/([a-z0-9-]{3,100})\/([a-f0-9]{64})\.(yaml|json)$/);
  if (artifactMatch && request.method === "GET") return serveR2(await env.RECIPE_BUCKET.get(url.pathname.slice(1)), artifactMatch[3] === "json" ? "application/json" : "application/yaml");
  if (url.pathname === "/internal/discover" && request.method === "POST") {
    if (!(await authorized(request, config))) return errorResponse(401, "UNAUTHORIZED", "A valid indexer administration token is required.");
    return jsonResponse(await scheduleDiscovery(env, "manual"), 202);
  }
  if (url.pathname === "/internal/publish" && request.method === "POST") {
    if (!(await authorized(request, config))) return errorResponse(401, "UNAUTHORIZED", "A valid indexer administration token is required.");
    return jsonResponse(await publishCatalog(env, repository, config), 202);
  }
  const scanMatch = url.pathname.match(/^\/internal\/scans\/([a-f0-9-]{36})$/);
  if (scanMatch && request.method === "GET") {
    if (!(await authorized(request, config))) return errorResponse(401, "UNAUTHORIZED", "A valid indexer administration token is required.");
    const scan = await repository.getScan(scanMatch[1]);
    return scan ? jsonResponse(scan) : errorResponse(404, "SCAN_NOT_FOUND", "The requested scan does not exist.");
  }
  return errorResponse(404, "NOT_FOUND", "No Recipe Indexer route matches this request.");
}

export default {
  async fetch(request, env) {
    try {
      return await handleAPI(request, env);
    } catch (error) {
      console.error("recipe-indexer request failed", { message: normalizeError(error) });
      return errorResponse(500, "INTERNAL_ERROR", "The Recipe Indexer could not complete this request.");
    }
  },
  async scheduled(controller, env, ctx) {
    ctx.waitUntil(scheduledTick(controller, env).catch((error) => console.error("recipe-indexer schedule failed", { message: normalizeError(error) })));
  },
  async queue(batch, env) {
    await processQueueBatch(batch, env);
  }
};

export { handleAPI };
