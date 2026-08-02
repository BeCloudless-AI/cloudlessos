# Publish recipes to the Cloudless Recipe Manager

Cloudless community recipes let an author describe a reproducible model setup, publish an
immutable version, and make that version discoverable in **Model Manager -> Recipes -> Discover**.
Recipe authoring is intentionally a developer workflow rather than an OS form. Write a
`cloudless.recipe/v1` manifest in YAML or JSON in your normal development environment, validate it
with the `cloudless recipes` command, and publish it with a scoped publisher API key. This is the
same workflow whether the CLI runs in the CloudlessOS terminal, Linux, macOS, or Windows.

The CloudlessOS interface is the consumer experience: it discovers, installs, validates, runs,
updates, and removes recipes. It does not try to reproduce container builds or inference-engine
configuration in a graphical form.

## Follow authors and see new releases

Open the account menu and choose **Community**. The **Explore** feed is built from exact recipe
revisions that successfully reached the published state. Choose an author's name to open their
profile, see their public recipes and follower counts, and follow them. **Following** then shows
new recipe publications from only those authors. Unfollowing does not uninstall recipes or remove
model weights from the machine; it only changes the activity feed.

The feed is intentionally about verifiable catalog activity, not arbitrary status posts. A recipe
appears only after validation and publication complete, and the recipe card opens the same signed
detail and installation flow used by Model Manager.

## What can be published today

Community publication accepts two immutable-image container adapters:

- `managed-container-v1` under `cloudless.recipe/v1` is the command-free safe default. Cloudless
  synthesizes the vLLM command and keeps the container unprivileged and read-only.
- `advanced-container-v1` under `cloudless.recipe/v2` lets the signed recipe define its complete
  in-container entry point and command, arbitrary engine arguments and environment, additional
  pinned model revisions, writable-root behavior, container user, IPC, shared memory, ulimits,
  tmpfs, process limit, and Linux capabilities. It is intended for SM-specific kernels,
  speculative decoders such as DFlash, custom attention backends, and other engine-owned stacks.

Both adapters retain the platform-level invariants needed by Cloudless:

- the container image must be pinned by SHA-256 digest;
- every Hugging Face model, including draft/speculator models, must be pinned to an exact 40- or
  64-character commit;
- the recipe uses one local node and one-way tensor/pipeline parallelism;
- the public model identity is always `cloudless`;
- the private recipe server uses port `8890` and `/v1`;
- no host commands, arbitrary host mounts, Docker socket, or arbitrary source repository are
  accepted. Advanced permissions apply inside the pinned container and are shown before launch.

This keeps host ownership with Cloudless without forcing every inference stack into one generic
vLLM launch shape. Advanced recipes are not sandbox-equivalent to managed recipes: their complete
command and requested container permissions must be reviewed before launch.

## Publish with the Cloudless CLI

### 1. Create a publisher API key

In CloudlessOS:

1. Sign in.
2. Open the account menu from your username/profile picture.
3. Open **API Keys**.
4. Give the key a recognizable name, select an expiry, and choose **Create publisher key**.
5. Copy the `cld_alpha_...` secret immediately. It is displayed once; Cloudless stores only a
   hash and cannot reveal it later.

The generated key is limited to recipe read, write, and publish operations. It cannot change your
profile, password, or account. Revoke a lost key from the same screen.

During the current beta, recipe publishing and publisher-key creation may be restricted to
accounts enabled for the test program. Public discovery does not require an account.

### 2. Get the CLI

The `cloudless` command is already installed in the CloudlessOS terminal. Until standalone CLI
downloads are published, another development machine can build it from this repository with Go:

```bash
cd orchestrator
go build -o ./cloudless ./cmd/cloudless
```

On Windows, use `-o ./cloudless.exe`.

### 3. Publish your custom container image

The recipe service stores and signs the YAML manifest; it does **not** upload the container image.
Before publishing a recipe for a modified vLLM build, push the image to a public OCI registry and
copy its immutable digest into the manifest. GitHub Container Registry (GHCR) is the recommended
starting point because public container packages are currently free and anonymously pullable.
GitHub Actions are not required.

#### Push to GitHub Container Registry

1. Create a GitHub personal access token (classic) with the `write:packages` scope. GitHub provides
   a direct token-creation page at
   [github.com/settings/tokens/new?scopes=write:packages](https://github.com/settings/tokens/new?scopes=write:packages).
   If an organization requires SSO, authorize the token for that organization.
2. Sign Docker into GHCR. Replace `YOUR_GITHUB_USERNAME` and paste the token when prompted:

   ```bash
   read -rsp "GitHub package token: " CR_PAT && echo
   echo "$CR_PAT" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
   unset CR_PAT
   ```

3. Build the custom image. Image names should be lowercase:

   ```bash
   docker build \
     -t ghcr.io/your_github_username/cloudless-vllm:1.0.0 \
     .
   ```

4. Push it:

   ```bash
   docker push ghcr.io/your_github_username/cloudless-vllm:1.0.0
   ```

5. A command-line push creates a private GHCR package by default. In GitHub, open your profile or
   organization, select **Packages**, open the new container package, open **Package settings**, and
   change its visibility to **Public**. Cloudless validation and CloudlessOS users must be able to
   pull a community image without receiving the author's GitHub credentials.
6. Pull the published tag once and read the digest:

   ```bash
   docker pull ghcr.io/your_github_username/cloudless-vllm:1.0.0
   docker inspect --format='{{index .RepoDigests 0}}' \
     ghcr.io/your_github_username/cloudless-vllm:1.0.0
   ```

   The result has this form:

   ```text
   ghcr.io/your_github_username/cloudless-vllm@sha256:0123456789abcdef...
   ```

7. Put that complete `name@sha256:digest` value in `recipe.engine.image`. Do not put the mutable
   `:1.0.0` tag, the GitHub token, or registry credentials in the recipe.

The GitHub package token and the Cloudless publisher key are different credentials: the GitHub
token uploads the image to GHCR, while the `cld_alpha_...` key publishes the recipe metadata to
Cloudless. Neither secret belongs in YAML or source control.

Another public Docker/OCI registry can be used instead. It must allow anonymous pulls, preserve the
referenced digest, and be reachable by both the Cloudless validation service and end-user systems.
Private registry credentials are not currently distributed with community recipes.

Automatic community publication currently accepts custom **vLLM** managed images. SGLang images
can be registered and used locally, but the current community admission policy rejects them.

### 4. Create a manifest

Copy [`examples/community-managed-container.yaml`](./examples/community-managed-container.yaml)
for a standard vLLM model. For a custom optimized stack, use
[`examples/community-laguna-s-2.1-dgx-spark.yaml`](./examples/community-laguna-s-2.1-dgx-spark.yaml)
as the `cloudless.recipe/v2` advanced-container example. YAML and JSON are accepted up to 2 MiB.

Important distinctions:

- `metadata.version` is the recipe version, not the model version.
- `recipe.model.revision` is an exact Hugging Face commit, not `main` or a branch name.
- `recipe.engine.image` must contain `@sha256:<64 hexadecimal characters>`, not a mutable tag.
- Published versions are immutable. To change anything later, bump the semantic version and
  publish a new revision.

### 5. Validate locally

```bash
cloudless recipes validate ./cloudless-recipe.yaml
```

A successful result prints `Container recipe is structurally valid for submission` and a manifest
digest. This is a fast offline structure/policy check. It does not prove that the image starts or
that the model fits every machine; the service performs the authoritative validation after
submission.

The authoritative service also scans the pinned container image with Trivy. The admission policy
is intentionally more precise than "any finding fails":

- every HIGH and CRITICAL finding participates in the admission decision; exact totals plus a
  bounded finding sample are retained in the exact-revision validation evidence;
- HIGH findings are published as warnings;
- CRITICAL findings without a known fixed package version are published as warnings, because the
  recipe author cannot resolve them by selecting a newer package;
- a CRITICAL finding with an available fixed package version blocks publication until the image is
  rebuilt with that fix;
- an exact image digest already curated and shipped by CloudlessOS may be admitted as a
  `curated-cloudless-runtime`; its findings remain visible warnings. This is a code-reviewed,
  digest-specific exception, not a registry-name or publisher bypass;
- a missing, interrupted, or incomplete image scan always blocks publication and is retried. The
  service never treats a scanner outage as a clean image.

The submission status includes the policy name, counts, warnings, and any blocking reason. A
community signature means that this policy passed for the exact image digest; it does not mean the
image contains zero known vulnerabilities.

### 6. Publish

On Bash, load the secret without putting it in shell history:

```bash
read -rsp "Cloudless publisher key: " CLOUDLESS_API_KEY && echo
export CLOUDLESS_API_KEY
cloudless recipes publish ./cloudless-recipe.yaml
```

On PowerShell:

```powershell
$env:CLOUDLESS_API_KEY = Read-Host "Cloudless publisher key"
.\cloudless.exe recipes publish .\cloudless-recipe.yaml
```

The CLI defaults to `https://becloudless.ai/api/v1/recipes`. A development service can be selected
with `--api URL` or `CLOUDLESS_COMMUNITY_API`.

The command creates the recipe if necessary, creates an immutable revision, and submits it. It
prints a submission ID. Save that ID.

### 7. Check the submission

```bash
cloudless recipes status <submission-id>
```

Typical states are:

- `submitted` or `validating`: queued or being checked;
- `published`: accepted, signed, and visible in Discover;
- `rejected`: validation failed; the response includes the reason;
- `withdrawn`: the author withdrew that revision.

To publish an update, change the manifest, bump `metadata.version` (for example from `1.0.0` to
`1.0.1`), validate again, and run the same publish command. The CLI reuses the recipe owned by your
account and adds a new immutable revision.

### Use the REST API directly

Automations may call the same API without the CLI. Use
`https://becloudless.ai/api` as the server, send the publisher key as
`Authorization: Bearer cld_alpha_...`, and follow this sequence:

1. `POST /v1/recipes` creates the owned recipe record.
2. `POST /v1/recipes/{recipeId}/revisions` creates one immutable manifest version.
3. `POST /v1/recipes/{recipeId}/revisions/{revisionId}/submit` queues validation.
4. `GET /v1/recipes/submissions/{revisionId}` returns its current state.

Mutation requests may include an `Idempotency-Key` header so a safe retry cannot accidentally
create duplicates. The CLI generates these keys automatically. Request and response schemas are in
the backend's `openapi.yaml`; using the CLI is recommended unless an external application needs a
native API integration.

## What the trust labels mean

- **Local draft:** stored only on this machine; editable and not community signed.
- **Community signed:** the Cloudless service validated the constrained policy and signed the exact
  published bytes. The author is identified, and tampering can be detected.
- **Cloudless verified:** the Cloudless project performed the additional review represented by
  this label.
- **Official:** maintained as an official Cloudless recipe.
- **Suspended or revoked:** no longer trusted for new installation. An exact signed revocation is
  enforced by clients.

Community signed does not mean Cloudless wrote the recipe or guarantees its quality. Always read
the model, image, permissions, compatibility, author, and version details before installing.

## Install a published recipe

1. Open **Model Manager -> Recipes -> Discover**.
2. Search by recipe name, author, model, category, or platform.
3. Open the recipe and select an exact published version.
4. Review its trust label, compatibility, pinned image/model revisions, and requested resources.
5. Select **Install**. Cloudless verifies the signature and revocation state before changing the
   local recipe library.
6. Install the model weights if they are not already present, select **Validate**, and then **Run**.

## Common errors

- **Sign in required:** the in-Cloudless flow needs an active Cloudless account session.
- **Publisher key required:** set `CLOUDLESS_API_KEY` to the complete `cld_alpha_...` secret.
- **Forbidden:** the account is not currently allowed to publish, or the key lacks/has lost the
  required scope. Create a new publisher key from an enabled account.
- **Slug already exists but is not owned by this account:** choose a different recipe title/slug.
- **Version already exists:** published versions cannot be overwritten; bump the version.
- **Recipe policy rejected:** use `managed-container-v1` for a command-free vLLM profile or
  `advanced-container-v1` with `cloudless.recipe/v2` for a custom in-container runtime. Host
  commands, arbitrary host mounts, mutable images/models, and distributed community containers
  remain invalid.
- **Validation rejected:** inspect the returned reason. Common causes are a nonexistent digest,
  inaccessible model revision, incomplete image scan, actionable CRITICAL image vulnerability in
  a non-curated image, or
  incompatible metadata. After fixing it, bump `metadata.version`; rejected versions remain
  immutable evidence and are not overwritten.
- **Key stopped working:** it expired or was revoked. Create a replacement; never commit keys to a
  recipe or source repository.

For the exact REST contract used by the UI and CLI, see the backend `openapi.yaml`. The security and
signature model is documented in [`COMMUNITY_RECIPE_CONTRACT.md`](./COMMUNITY_RECIPE_CONTRACT.md),
and implementation work is tracked separately in
[`COMMUNITY_RECIPES_ROADMAP.md`](./COMMUNITY_RECIPES_ROADMAP.md).
