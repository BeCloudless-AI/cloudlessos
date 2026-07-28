import { pathLooksLikeRecipe } from "../lib/util.js";

const GITHUB_API = "https://api.github.com";

export class GitHubError extends Error {
  constructor(message, status, retryAfter = 0) {
    super(message);
    this.name = "GitHubError";
    this.status = status;
    this.retryAfter = retryAfter;
  }
}

async function githubRequest(config, pathname, parameters = {}) {
  if (!config.githubToken) throw new Error("GITHUB_TOKEN is required for recipe discovery");
  const url = new URL(pathname, GITHUB_API);
  for (const [key, value] of Object.entries(parameters)) if (value !== undefined && value !== "") url.searchParams.set(key, String(value));
  const response = await fetch(url, {
    headers: {
      accept: "application/vnd.github+json",
      authorization: `Bearer ${config.githubToken}`,
      "user-agent": "Cloudless-Recipe-Indexer/1",
      "x-github-api-version": config.githubAPIVersion
    }
  });
  if (!response.ok) {
    const remaining = response.headers.get("x-ratelimit-remaining");
    const reset = Number(response.headers.get("x-ratelimit-reset") || 0);
    const retryAfter = Number(response.headers.get("retry-after") || 0) || (remaining === "0" && reset ? Math.max(60, reset - Math.floor(Date.now() / 1000)) : 0);
    const body = await response.text();
    throw new GitHubError(`GitHub ${response.status}: ${body.slice(0, 500)}`, response.status, retryAfter);
  }
  return response.json();
}

function splitRepository(repository) {
  const parts = String(repository || "").split("/");
  if (parts.length !== 2 || !parts[0] || !parts[1]) throw new Error(`invalid GitHub repository ${repository}`);
  return { owner: parts[0], repository: parts[1] };
}

export async function resolveRevision(config, repository, ref = "") {
  const { owner, repository: name } = splitRepository(repository);
  let wanted = ref;
  if (!wanted) {
    const metadata = await githubRequest(config, `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`);
    wanted = metadata.default_branch;
  }
  const commit = await githubRequest(config, `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/commits/${encodeURIComponent(wanted)}`);
  return commit.sha;
}

export async function discoverRegistry(config, registry) {
  const repository = `${registry.owner}/${registry.repository}`;
  const revision = await resolveRevision(config, repository, registry.ref);
  const tree = await githubRequest(config, `/repos/${encodeURIComponent(registry.owner)}/${encodeURIComponent(registry.repository)}/git/trees/${encodeURIComponent(revision)}`, { recursive: 1 });
  if (tree.truncated) throw new Error(`GitHub tree for ${repository} was truncated`);
  const prefix = String(registry.directory || "").replace(/^\/+|\/+$/g, "");
  return (tree.tree || [])
    .filter((entry) => entry.type === "blob" && (!prefix || entry.path.startsWith(`${prefix}/`)) && pathLooksLikeRecipe(entry.path))
    .map((entry) => ({
      type: "github.manifest",
      repository,
      manifestPath: entry.path,
      revision,
      trust: registry.trust || "community",
      registry: registry.name || ""
    }));
}

export async function inspectRepository(config, message) {
  const repository = message.repository;
  const revision = await resolveRevision(config, repository, message.ref || "");
  const { owner, repository: name } = splitRepository(repository);
  const tree = await githubRequest(config, `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/git/trees/${encodeURIComponent(revision)}`, { recursive: 1 });
  if (tree.truncated) throw new Error(`GitHub tree for ${repository} was truncated`);
  return (tree.tree || []).filter((entry) => entry.type === "blob" && pathLooksLikeRecipe(entry.path)).map((entry) => ({
    type: "github.manifest",
    repository,
    manifestPath: entry.path,
    revision,
    trust: message.trust || "discovered",
    registry: message.registry || ""
  }));
}

export async function discoverSearch(config, search) {
  const kind = search.kind === "repositories" ? "repositories" : "code";
  const result = await githubRequest(config, `/search/${kind}`, { q: search.query, per_page: Math.min(100, Number(search.perPage || 100)), page: Number(search.page || 1) });
  if (kind === "repositories") {
    return (result.items || []).map((item) => ({ type: "github.repository", repository: item.full_name, ref: item.default_branch || "", trust: "discovered", registry: "github-search" }));
  }
  return (result.items || []).filter((item) => pathLooksLikeRecipe(item.path)).map((item) => ({
    type: "github.manifest",
    repository: item.repository.full_name,
    manifestPath: item.path,
    revision: "",
    trust: "discovered",
    registry: "github-search"
  }));
}

export async function fetchManifest(config, message) {
  const { owner, repository: name } = splitRepository(message.repository);
  const revision = message.revision || await resolveRevision(config, message.repository, message.ref || "");
  const content = await githubRequest(config, `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/contents/${message.manifestPath.split("/").map(encodeURIComponent).join("/")}`, { ref: revision });
  if (content.type !== "file" || content.encoding !== "base64") throw new Error("GitHub recipe manifest is not a base64 file");
  const binary = atob(String(content.content || "").replace(/\s+/g, ""));
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  if (bytes.byteLength > 2 * 1024 * 1024) throw new Error("recipe manifest exceeds 2 MB");
  return {
    bytes,
    source: {
      kind: "github",
      repository: message.repository,
      manifestPath: message.manifestPath,
      revision,
      trust: message.trust || "discovered",
      registry: message.registry || "",
      url: `https://github.com/${message.repository}/blob/${revision}/${message.manifestPath}`
    }
  };
}

export { githubRequest };
