# Cloudless Recipe Indexer

The Recipe Indexer discovers compatible recipes without requiring someone to maintain a
handwritten JSON catalog. It is the trust and distribution layer between public recipe sources
and CloudlessOS.

It currently discovers known SparkRun registries and GitHub repositories, pins every source to
an immutable commit, preserves the original recipe artifact, normalizes searchable metadata,
runs fail-closed static safety checks, and publishes a signed catalog. A future Cloudless social
service will submit additional sources and community metadata to this same pipeline; it does not
replace the indexer.

## Architecture

| Cloudflare service | Responsibility |
| --- | --- |
| Worker | Public catalog/search API, protected administration API, cron and queue consumer |
| Queue | Fan-out of repository discovery and manifest ingestion without long HTTP requests |
| D1 | Sources, normalized recipes, scan state and monotonically increasing catalog sequence |
| R2 | Immutable original artifacts, normalized recipe documents, catalog and signature |

The public API never exposes quarantined recipes. Production publication also fails if the
Ed25519 signing key is absent.

## Public API

- `GET /health`
- `GET /v1/catalog` and `GET /v1/catalog.sig`
- `GET /v1/keys/:key-id`
- `GET /v1/recipes?q=&platform=&architecture=&category=&engine=&nodes=&compatible=`
- `GET /v1/recipes/:id`
- `GET /v1/artifacts/:id/:sha256.yaml`

The protected API uses `Authorization: Bearer <ADMIN_TOKEN>`:

- `POST /internal/discover` starts a scan and returns its ID.
- `GET /internal/scans/:scan-id` reports scan progress and outcomes.
- `POST /internal/publish` rebuilds the signed catalog immediately.

## Local development

Node.js 22 or newer is required.

```bash
cd services/recipe-indexer
npm ci
npm run db:migrate:local
cp .dev.vars.example .dev.vars
npm run dev
```

Use a GitHub fine-grained token with read-only access to public repository metadata and contents.
Local development may omit signing keys; production deliberately may not. Run the verification
suite with:

```bash
npm test
npm run check
```

## One-command Cloudflare deployment

From the repository root, run:

```bash
bash services/recipe-indexer/scripts/deploy-indexer.sh
```

The first run opens Cloudflare authentication, creates any missing D1/R2/Queue resources,
generates and securely retains the signing identity under `~/.config/cloudless`, uploads Worker
secrets, migrates D1, verifies the code, deploys, and starts the first discovery scan. Later runs
of the exact same command reuse all resources and secrets and simply verify and deploy changes.
If WSL does not already have a current Node.js runtime, the command downloads a checksum-verified,
private Node.js 24 LTS runtime under `~/.local/share/cloudless`; it does not alter system packages.

To deliberately request a fresh discovery scan during a later deployment:

```bash
bash services/recipe-indexer/scripts/deploy-indexer.sh --discover
```

Cloudflare retains the secrets, while the ignored local recovery copy prevents accidental signing
key rotation. If that file is ever lost, restore it from backup. `--rotate-secrets` is available
for an intentional emergency rotation and is never performed by a normal redeploy.

## Manual Cloudflare deployment

Authenticate Wrangler with the Cloudflare account that owns `becloudless.ai`, then create the
resources once:

```bash
npx wrangler login
npx wrangler d1 create cloudless-recipes --location apac
npx wrangler r2 bucket create cloudless-recipes
npx wrangler queues create cloudless-recipe-indexer
npx wrangler queues create cloudless-recipe-indexer-dead
npm run keys:generate
```

Copy the generated values into Worker secrets one at a time, along with a GitHub token:

```bash
npx wrangler secret put CATALOG_SIGNING_PRIVATE_KEY
npx wrangler secret put CATALOG_SIGNING_PUBLIC_KEY
npx wrangler secret put ADMIN_TOKEN
npx wrangler secret put GITHUB_TOKEN
```

Never put those values in `wrangler.jsonc`, `.dev.vars.example`, Git, CI logs, or an OS package.
Copy the D1 UUID printed by Wrangler into the `RECIPE_DB` entry in `wrangler.jsonc`, then run:

```bash
npm run db:migrate:remote
npm test
npm run check
npm run deploy
```

Finally, trigger the first scan using the protected endpoint. Subsequent discovery runs every six
hours; the 15-minute cron also publishes any dirty catalog left by a delayed queue batch.

## Publication and trust rules

- A mutable branch or tag is resolved to an immutable Git commit before ingestion.
- Original YAML/JSON bytes are content-addressed by SHA-256 and retained unchanged.
- Privileged containers, Docker socket/host namespace access, sensitive host mounts,
  pipe-to-shell commands and destructive root operations are blocked.
- Unsupported engines and high-risk recipes enter manual review rather than the public catalog.
- Catalog JSON is canonicalized, signed with Ed25519 and assigned a monotonic D1 sequence.
- CloudlessOS should pin the catalog public key and reject invalid signatures or sequence rollback.

When accounts, ratings and comments are added, they should live in a separate social data model.
The indexer remains authoritative for executable recipe identity, validation status, compatibility,
artifact digest and catalog publication.
