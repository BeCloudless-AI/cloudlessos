import assert from "node:assert/strict";
import test from "node:test";
import { catalogResponse, filterRecipes } from "../src/catalog.js";
import { signCatalog, verifyCatalog } from "../src/lib/signing.js";
import { bytesToBase64 } from "../src/lib/util.js";

const recipes = [
  { id: "one", name: "Qwen Chat", description: "Reasoning", category: "chat", tags: ["qwen"], runtime: { engine: "vllm" }, model: { id: "Qwen/Qwen" }, requirements: { platforms: ["dgx-spark"], architectures: ["arm64"], minNodes: 1, maxNodes: 1 } },
  { id: "two", name: "DeepSeek Cluster", description: "Two node coding", category: "coding", tags: ["deepseek"], runtime: { engine: "vllm-ray" }, model: { id: "deepseek/flash" }, requirements: { platforms: ["dgx-spark"], architectures: ["arm64"], minNodes: 2, maxNodes: 2 } },
  { id: "three", name: "GLM Vision", description: "Triple Spark", category: "vision", tags: ["glm"], runtime: { engine: "vllm-ray" }, model: { id: "glm/vision" }, requirements: { platforms: ["dgx-spark"], architectures: ["arm64"], minNodes: 3, maxNodes: 3 } }
];

test("filters the generated catalog by exact or compatible Spark count", () => {
  const exact = filterRecipes(recipes, new URL("https://recipes.example/v1/recipes?nodes=2"));
  assert.deepEqual(exact.items.map((recipe) => recipe.id), ["two"]);
  const compatible = filterRecipes(recipes, new URL("https://recipes.example/v1/recipes?nodes=2&compatible=true"));
  assert.deepEqual(compatible.items.map((recipe) => recipe.id), ["two"]);
  const search = filterRecipes(recipes, new URL("https://recipes.example/v1/recipes?q=vision&category=vision"));
  assert.deepEqual(search.items.map((recipe) => recipe.id), ["three"]);
});

test("signs and verifies a canonical catalog with Ed25519", async () => {
  const pair = await crypto.subtle.generateKey("Ed25519", true, ["sign", "verify"]);
  const [privateKey, publicKey] = await Promise.all([
    crypto.subtle.exportKey("pkcs8", pair.privateKey),
    crypto.subtle.exportKey("spki", pair.publicKey)
  ]);
  const config = {
    environment: "test",
    signingKeyID: "test-key",
    signingPrivateKey: bytesToBase64(new Uint8Array(privateKey))
  };
  const envelope = await signCatalog({ schema: "cloudless.recipe-catalog/v1", sequence: 1, recipes }, config);
  assert.equal(envelope.signature.algorithm, "Ed25519");
  assert.equal(await verifyCatalog(envelope, bytesToBase64(new Uint8Array(publicKey))), true);
  envelope.payload.sequence = 2;
  assert.equal(await verifyCatalog(envelope, bytesToBase64(new Uint8Array(publicKey))), false);
});

test("refuses unsigned production catalogs", async () => {
  await assert.rejects(() => signCatalog({ recipes: [] }, { environment: "production", signingKeyID: "prod", signingPrivateKey: "" }), /required in production/);
});

test("serves the buffered catalog after parsing without reusing a consumed R2 stream", async () => {
  const catalog = {
    body: JSON.stringify({ payload: { recipes: [] } }),
    object: {
      httpEtag: '"catalog-etag"',
      writeHttpMetadata(headers) { headers.set("cache-control", "public, max-age=300"); }
    }
  };
  const response = catalogResponse(catalog);
  assert.equal(response.headers.get("etag"), '"catalog-etag"');
  assert.equal(response.headers.get("cache-control"), "public, max-age=300");
  assert.deepEqual(await response.json(), { payload: { recipes: [] } });
});
