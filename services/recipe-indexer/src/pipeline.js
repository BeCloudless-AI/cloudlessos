import { discoverRegistry, discoverSearch, fetchManifest, GitHubError, inspectRepository } from "./connectors/github.js";
import { getConfig } from "./config.js";
import { publishCatalog, publishIfDirty } from "./catalog.js";
import { normalizeRecipe } from "./lib/normalize.js";
import { publicationVisibility, validateRecipe } from "./lib/security.js";
import { normalizeError, sha256Hex, sourceIdentity, stableStringify } from "./lib/util.js";
import { RecipeRepository } from "./repository.js";

function rootJobs(config, scanID) {
  return [
    ...config.registries.map((registry) => ({ type: "github.registry", scanId: scanID, registry })),
    ...config.searches.map((search) => ({ type: "github.search", scanId: scanID, search }))
  ];
}

async function sendMessages(queue, messages) {
  if (!messages.length) return;
  const payloads = messages.map((body) => ({ body }));
  if (typeof queue.sendBatch === "function") await queue.sendBatch(payloads);
  else for (const item of payloads) await queue.send(item.body);
}

export async function scheduleDiscovery(env, reason = "scheduled") {
  const config = getConfig(env);
  const repository = new RecipeRepository(env.RECIPE_DB);
  const scanID = crypto.randomUUID();
  const jobs = rootJobs(config, scanID);
  await repository.createScan(scanID, reason, jobs.length);
  try {
    await sendMessages(env.RECIPE_JOBS, jobs);
  } catch (error) {
    await repository.failScan(scanID, normalizeError(error));
    throw error;
  }
  return { scanId: scanID, jobs: jobs.length };
}

export async function prepareIndexedRecipe(bytes, source, config) {
  const { recipe } = await normalizeRecipe(bytes, source);
  const validation = validateRecipe(recipe);
  const visibility = publicationVisibility(validation, config.autoPublish);
  const extension = recipe.artifact.mediaType === "application/json" ? "json" : "yaml";
  const publicArtifactKey = `v1/artifacts/${recipe.id}/${recipe.source.digest}.${extension}`;
  const quarantineKey = `quarantine/${recipe.id}/${recipe.source.digest}.${extension}`;
  const artifactKey = visibility === "public" ? publicArtifactKey : quarantineKey;
  recipe.artifact.url = visibility === "public" ? `${config.publicBaseURL}/${publicArtifactKey}` : "";
  recipe.discoveredAt = new Date().toISOString();
  recipe.updatedAt = recipe.discoveredAt;
  const normalizedDigest = await sha256Hex(stableStringify(recipe));
  return { recipe, validation, visibility, artifactKey, normalizedDigest };
}

async function ingestManifest(env, repository, config, message) {
  const { bytes, source } = await fetchManifest(config, message);
  const sourceID = await sourceIdentity(source.repository, source.manifestPath);
  const sourceDigest = await sha256Hex(bytes);
  let prepared;
  try {
    prepared = await prepareIndexedRecipe(bytes, source, config);
  } catch (error) {
    await repository.upsertSource(source, sourceID, sourceDigest, "rejected", normalizeError(error));
    return { discovered: 1, rejected: 1 };
  }
  const { recipe, validation, visibility, artifactKey, normalizedDigest } = prepared;
  await env.RECIPE_BUCKET.put(artifactKey, bytes, {
    httpMetadata: { contentType: recipe.artifact.mediaType, cacheControl: visibility === "public" ? "public, max-age=31536000, immutable" : "private, no-store" },
    customMetadata: { sourceDigest, repository: source.repository, revision: source.revision }
  });
  const sourceStatus = visibility === "public" ? "published" : visibility === "review" ? "review" : "rejected";
  await repository.upsertSource(source, sourceID, sourceDigest, sourceStatus, "");
  await repository.upsertRecipe(recipe, sourceID, normalizedDigest, validation, visibility);
  if (visibility === "public") {
    const recipeDocument = stableStringify({ ...recipe, validation });
    await env.RECIPE_BUCKET.put(`v1/recipes/${recipe.id}.json`, recipeDocument, {
      httpMetadata: { contentType: "application/json; charset=utf-8", cacheControl: "public, max-age=300" },
      customMetadata: { normalizedDigest, sourceDigest }
    });
  } else await env.RECIPE_BUCKET.delete(`v1/recipes/${recipe.id}.json`);
  await repository.setState("catalog_dirty", "true");
  if (visibility === "public") return { discovered: 1, accepted: 1 };
  if (visibility === "review") return { discovered: 1, review: 1 };
  return { discovered: 1, rejected: 1 };
}

async function processBody(env, repository, config, body) {
  switch (body.type) {
    case "github.registry":
      return { children: (await discoverRegistry(config, body.registry)).map((child) => ({ ...child, scanId: body.scanId })) };
    case "github.search":
      return { children: (await discoverSearch(config, body.search)).map((child) => ({ ...child, scanId: body.scanId })) };
    case "github.repository":
      return { children: (await inspectRepository(config, body)).map((child) => ({ ...child, scanId: body.scanId })) };
    case "github.manifest":
      return { children: [], outcome: await ingestManifest(env, repository, config, body) };
    default:
      throw new Error(`unsupported recipe job ${body.type}`);
  }
}

export async function processQueueBatch(batch, env) {
  const config = getConfig(env);
  const repository = new RecipeRepository(env.RECIPE_DB);
  for (const message of batch.messages) {
    const body = message.body || {};
    try {
      const result = await processBody(env, repository, config, body);
      const children = result.children || [];
      if (children.length) {
        await repository.addPending(body.scanId, children.length);
        try {
          await sendMessages(env.RECIPE_JOBS, children);
        } catch (error) {
          await repository.addPending(body.scanId, -children.length);
          throw error;
        }
      }
      const scan = await repository.completeJob(body.scanId, result.outcome || {});
      if (scan?.pending_jobs === 0) await publishCatalog(env, repository, config);
      message.ack();
    } catch (error) {
      const permanent = error instanceof GitHubError && error.status >= 400 && error.status < 500 && ![403, 429].includes(error.status);
      const exhausted = Number(message.attempts || 1) >= 5;
      if (permanent || exhausted) {
        const scan = await repository.completeJob(body.scanId, { rejected: 1, error: normalizeError(error) });
        if (scan?.pending_jobs === 0) await publishIfDirty(env, repository, config);
        message.ack();
      } else {
        message.retry({ delaySeconds: Math.min(3600, error.retryAfter || 30 * Number(message.attempts || 1)) });
      }
    }
  }
}

export async function scheduledTick(controller, env) {
  const config = getConfig(env);
  const repository = new RecipeRepository(env.RECIPE_DB);
  await publishIfDirty(env, repository, config);
  const date = new Date(controller.scheduledTime || Date.now());
  if (date.getUTCMinutes() === 0 && date.getUTCHours() % 6 === 0) return scheduleDiscovery(env, "cron");
  return null;
}

export { ingestManifest, processBody, rootJobs, sendMessages };
