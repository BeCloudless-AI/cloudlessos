import assert from "node:assert/strict";
import test from "node:test";
import { DEFAULT_GITHUB_SEARCHES, DEFAULT_REGISTRIES } from "../src/config.js";
import { pathLooksLikeRecipe } from "../src/lib/util.js";
import { rootJobs } from "../src/pipeline.js";

test("recognizes Cloudless, SparkRun, and registry recipe manifests", () => {
  for (const path of ["cloudless.recipe.yaml", "nested/cloudless.recipe.json", "sparkrun.yaml", "recipes/glm/config.yml", "official-recipes/qwen.yaml"]) {
    assert.equal(pathLooksLikeRecipe(path), true, path);
  }
  for (const path of ["README.md", "docker-compose.yml", "config.yaml", "recipes/README.yaml"]) {
    assert.equal(pathLooksLikeRecipe(path), false, path);
  }
});

test("a discovery scan fans out across registries and GitHub searches", () => {
  const jobs = rootJobs({ registries: DEFAULT_REGISTRIES, searches: DEFAULT_GITHUB_SEARCHES }, "scan-id");
  assert.equal(jobs.length, DEFAULT_REGISTRIES.length + DEFAULT_GITHUB_SEARCHES.length);
  assert.ok(jobs.some((job) => job.type === "github.registry" && job.registry.name === "official"));
  assert.ok(jobs.some((job) => job.type === "github.search" && job.search.query === "filename:sparkrun.yaml"));
  assert.ok(jobs.every((job) => job.scanId === "scan-id"));
});
