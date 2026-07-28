import { nowISO } from "./lib/util.js";

export class RecipeRepository {
  constructor(database) {
    if (!database) throw new Error("RECIPE_DB binding is required");
    this.database = database;
  }

  async createScan(id, reason, pendingJobs) {
    const now = nowISO();
    await this.database.prepare(`INSERT INTO scan_runs (id, reason, status, pending_jobs, started_at) VALUES (?1, ?2, 'queued', ?3, ?4)`)
      .bind(id, reason, pendingJobs, now).run();
    return { id, reason, status: "queued", pendingJobs, startedAt: now };
  }

  async failScan(id, error) {
    await this.database.prepare(`UPDATE scan_runs SET status='failed', error=?2, finished_at=?3 WHERE id=?1`).bind(id, error, nowISO()).run();
  }

  async addPending(id, count) {
    if (!count) return;
    await this.database.prepare(`UPDATE scan_runs SET pending_jobs=pending_jobs + ?2, status='running' WHERE id=?1`).bind(id, count).run();
  }

  async completeJob(id, outcome = {}) {
    return this.database.prepare(`
      UPDATE scan_runs SET
        pending_jobs = CASE WHEN pending_jobs > 0 THEN pending_jobs - 1 ELSE 0 END,
        status = CASE WHEN pending_jobs <= 1 THEN 'completed' ELSE 'running' END,
        discovered_count = discovered_count + ?2,
        accepted_count = accepted_count + ?3,
        review_count = review_count + ?4,
        rejected_count = rejected_count + ?5,
        error = CASE WHEN ?6 = '' THEN error ELSE ?6 END,
        finished_at = CASE WHEN pending_jobs <= 1 THEN ?7 ELSE finished_at END
      WHERE id=?1
      RETURNING pending_jobs, status, discovered_count, accepted_count, review_count, rejected_count
    `).bind(id, outcome.discovered || 0, outcome.accepted || 0, outcome.review || 0, outcome.rejected || 0, outcome.error || "", nowISO()).first();
  }

  async upsertSource(source, sourceID, sourceDigest, status, error = "") {
    const now = nowISO();
    await this.database.prepare(`
      INSERT INTO sources (id, kind, repository, manifest_path, source_url, revision, source_digest, status, error, first_seen_at, last_seen_at)
      VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?10)
      ON CONFLICT(repository, manifest_path) DO UPDATE SET
        source_url=excluded.source_url, revision=excluded.revision, source_digest=excluded.source_digest,
        status=excluded.status, error=excluded.error, last_seen_at=excluded.last_seen_at
    `).bind(sourceID, source.kind, source.repository, source.manifestPath, source.url, source.revision, sourceDigest, status, error, now).run();
  }

  async upsertRecipe(recipe, sourceID, normalizedDigest, validation, visibility) {
    const now = nowISO();
    const recipeJSON = JSON.stringify(recipe);
    const validationJSON = JSON.stringify(validation);
    await this.database.prepare(`
      INSERT INTO recipes (
        id, source_id, slug, name, description, category, format, trust, risk, min_nodes, max_nodes,
        platform, architecture, model_id, engine, source_digest, normalized_digest, recipe_json,
        validation_json, visibility, discovered_at, updated_at, published_at
      ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, ?17, ?18, ?19, ?20, ?21, ?21, CASE WHEN ?20='public' THEN ?21 ELSE NULL END)
      ON CONFLICT(id) DO UPDATE SET
        source_id=excluded.source_id, slug=excluded.slug, name=excluded.name, description=excluded.description,
        category=excluded.category, format=excluded.format, trust=excluded.trust, risk=excluded.risk,
        min_nodes=excluded.min_nodes, max_nodes=excluded.max_nodes, platform=excluded.platform,
        architecture=excluded.architecture, model_id=excluded.model_id, engine=excluded.engine,
        source_digest=excluded.source_digest, normalized_digest=excluded.normalized_digest,
        recipe_json=excluded.recipe_json, validation_json=excluded.validation_json,
        visibility=excluded.visibility, updated_at=excluded.updated_at,
        published_at=CASE WHEN excluded.visibility='public' THEN excluded.updated_at ELSE recipes.published_at END
    `).bind(
      recipe.id, sourceID, recipe.slug, recipe.name, recipe.description, recipe.category, recipe.format,
      recipe.trust, validation.risk, recipe.requirements.minNodes, recipe.requirements.maxNodes,
      recipe.requirements.platforms.join(","), recipe.requirements.architectures.join(","),
      recipe.model.id, recipe.runtime.engine, recipe.source.digest, normalizedDigest, recipeJSON,
      validationJSON, visibility, now
    ).run();
  }

  async listPublicRecipes() {
    const result = await this.database.prepare(`SELECT recipe_json, validation_json, published_at FROM recipes WHERE visibility='public' ORDER BY updated_at DESC, id ASC`).all();
    return (result.results || []).map((row) => ({ ...JSON.parse(row.recipe_json), validation: JSON.parse(row.validation_json), publishedAt: row.published_at }));
  }

  async getRecipe(id) {
    const row = await this.database.prepare(`SELECT recipe_json, validation_json, visibility, published_at FROM recipes WHERE id=?1`).bind(id).first();
    if (!row || row.visibility !== "public") return null;
    return { ...JSON.parse(row.recipe_json), validation: JSON.parse(row.validation_json), publishedAt: row.published_at };
  }

  async getScan(id) {
    return this.database.prepare(`SELECT * FROM scan_runs WHERE id=?1`).bind(id).first();
  }

  async getState(key, fallback = "") {
    const row = await this.database.prepare(`SELECT value FROM service_state WHERE key=?1`).bind(key).first();
    return row ? row.value : fallback;
  }

  async setState(key, value) {
    await this.database.prepare(`INSERT INTO service_state (key, value, updated_at) VALUES (?1, ?2, ?3) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`)
      .bind(key, String(value), nowISO()).run();
  }
}
