import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { normalizeRecipe } from "../src/lib/normalize.js";
import { publicationVisibility, validateRecipe } from "../src/lib/security.js";
import { prepareIndexedRecipe } from "../src/pipeline.js";

const source = {
  kind: "github",
  repository: "mfellner/sparkrun-recipes",
  manifestPath: "recipes/glm-5.2/sparkrun.yaml",
  revision: "1234567890abcdef1234567890abcdef12345678",
  trust: "community",
  registry: "github-search",
  url: "https://github.com/mfellner/sparkrun-recipes/blob/1234567890abcdef1234567890abcdef12345678/recipes/glm-5.2/sparkrun.yaml"
};

test("normalizes a SparkRun recipe without translating its source artifact", async () => {
  const bytes = new Uint8Array(await readFile(new URL("fixtures/sparkrun.yaml", import.meta.url)));
  const { recipe } = await normalizeRecipe(bytes, source);
  assert.equal(recipe.schema, "cloudless.indexed-recipe/v1");
  assert.equal(recipe.format, "sparkrun/v2");
  assert.equal(recipe.name, "GLM 5.2 Triple Spark");
  assert.equal(recipe.model.id, "jarrelscy/GLM-5.2-NVFP4-AQLM-hybrid");
  assert.equal(recipe.model.context, 348160);
  assert.equal(recipe.requirements.minNodes, 3);
  assert.equal(recipe.requirements.maxNodes, 3);
  assert.equal(recipe.provider.preservesOriginalDocument, true);
  assert.match(recipe.id, /^glm-5-2-triple-spark-[a-f0-9]{12}$/);
});

test("publishes a statically safe pinned recipe and addresses the immutable artifact", async () => {
  const bytes = new Uint8Array(await readFile(new URL("fixtures/sparkrun.yaml", import.meta.url)));
  const prepared = await prepareIndexedRecipe(bytes, source, { autoPublish: true, publicBaseURL: "https://recipes.becloudless.ai" });
  assert.equal(prepared.visibility, "public");
  assert.equal(prepared.validation.status, "passed");
  assert.match(prepared.artifactKey, /^v1\/artifacts\/.+\/[a-f0-9]{64}\.yaml$/);
  assert.equal(prepared.recipe.artifact.url, `https://recipes.becloudless.ai/${prepared.artifactKey}`);
});

test("quarantines privileged and pipe-to-shell recipes", async () => {
  const bytes = new Uint8Array(await readFile(new URL("fixtures/blocked.yaml", import.meta.url)));
  const blockedSource = { ...source, manifestPath: "cloudless.recipe.yaml" };
  const { recipe } = await normalizeRecipe(bytes, blockedSource);
  const validation = validateRecipe(recipe);
  assert.equal(validation.status, "blocked");
  assert.equal(publicationVisibility(validation, true), "blocked");
  assert.ok(validation.findings.some((item) => item.code === "PRIVILEGED_CONTAINER"));
  assert.ok(validation.findings.some((item) => item.code === "PIPE_TO_SHELL"));
  assert.ok(validation.findings.some((item) => item.code === "SENSITIVE_HOST_MOUNT"));
});
