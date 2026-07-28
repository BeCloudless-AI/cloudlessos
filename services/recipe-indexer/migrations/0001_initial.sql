PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS scan_runs (
  id TEXT PRIMARY KEY,
  reason TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed')),
  pending_jobs INTEGER NOT NULL DEFAULT 0 CHECK (pending_jobs >= 0),
  discovered_count INTEGER NOT NULL DEFAULT 0,
  accepted_count INTEGER NOT NULL DEFAULT 0,
  review_count INTEGER NOT NULL DEFAULT 0,
  rejected_count INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT
);

CREATE TABLE IF NOT EXISTS sources (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  repository TEXT NOT NULL,
  manifest_path TEXT NOT NULL,
  source_url TEXT NOT NULL,
  revision TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  etag TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('discovered', 'published', 'review', 'rejected')),
  error TEXT NOT NULL DEFAULT '',
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  UNIQUE(repository, manifest_path)
);

CREATE TABLE IF NOT EXISTS recipes (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  category TEXT NOT NULL,
  format TEXT NOT NULL,
  trust TEXT NOT NULL,
  risk TEXT NOT NULL,
  min_nodes INTEGER NOT NULL,
  max_nodes INTEGER NOT NULL,
  platform TEXT NOT NULL,
  architecture TEXT NOT NULL,
  model_id TEXT NOT NULL,
  engine TEXT NOT NULL,
  source_digest TEXT NOT NULL,
  normalized_digest TEXT NOT NULL,
  recipe_json TEXT NOT NULL,
  validation_json TEXT NOT NULL,
  visibility TEXT NOT NULL CHECK (visibility IN ('public', 'review', 'blocked')),
  discovered_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  published_at TEXT
);

CREATE INDEX IF NOT EXISTS recipes_visibility_updated
  ON recipes(visibility, updated_at DESC);
CREATE INDEX IF NOT EXISTS recipes_nodes
  ON recipes(min_nodes, max_nodes);
CREATE INDEX IF NOT EXISTS recipes_category
  ON recipes(category);
CREATE INDEX IF NOT EXISTS recipes_model
  ON recipes(model_id);

CREATE TABLE IF NOT EXISTS service_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
