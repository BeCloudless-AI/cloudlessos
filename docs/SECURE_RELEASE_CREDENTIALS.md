# Secure release credentials

Release credentials are production infrastructure, not project configuration. Any token or key
shown in chat, screenshots, shell history, logs or a committed file must be treated as compromised
even when it still works.

## One-time rotation after exposure

1. In Cloudflare, create a new R2 token that can read and write objects only in the Cloudless update
   bucket. It does not need DNS, Workers, account administration or access to unrelated buckets.
2. Record its S3 access-key ID, secret-access key and account-scoped R2 endpoint directly in the
   private release environment described below.
3. Run the release preflight with the new credential. Do not publish until it passes.
4. Revoke the old R2 token and its S3 credentials, then confirm they can no longer list the bucket.
5. Revoke every GitHub token that has been exposed. The Cloudless package release pipeline does not
   require a GitHub personal access token. Create a replacement only for a separate service that
   demonstrably needs it, with repository-only and read-only access where possible.
6. Remove exposed values from terminal history and local notes. History cleanup does not make an
   exposed credential safe; revocation is the security boundary.

## Private release environment

Run `distro/scripts/configure-release-env.sh` once. It writes the operator's values to:

```text
~/.config/cloudless/release.env
```

The file must be owned by the release operator and mode `0600`. It is outside the repository and is
loaded by `distro/scripts/release.sh`; credentials should never be pasted into a command line,
checked into `.env`, or passed as script arguments. The script and preflight must never print their
values.

The file may also contain `CLOUDLESS_PHYSICAL_QUALIFICATION_DIR`, a non-secret path to the private
sealed hardware-evidence exports. The strict loader accepts the path as data and never evaluates it
as shell syntax. Evidence archives can contain operational detail, so keep that directory private
even though it contains no release credential.

For a 1.0+ stable release the file must also contain
`CLOUDLESS_SECURITY_CONTACT_ATTESTATION`, the path to a mode-0600 operational attestation:

```json
{
  "schema": "cloudless.security-contact-attestation.v1",
  "monitored": true,
  "securityContact": "mailto:security@becloudless.ai",
  "escalationOwner": "CloudlessOS release owner",
  "verifiedAt": "2026-07-31T12:00:00Z",
  "acknowledgementBusinessDays": 3
}
```

The timestamp must be no more than 30 days old. The release tooling validates the document
strictly, publishes its non-secret operational facts, binds them into the release gates and signed
manifest, and detached-signs the resulting descriptor. Pre-1.0 releases explicitly publish
`not-operational` when no attestation is supplied; a 1.0+ stable release fails closed.

The offline archive signing-key backup remains separate from the R2 credential. Keep it encrypted,
offline and readable only during signing. R2 compromise must not grant signing authority.

## Required checks

Before a production release:

- `release-preflight.sh` verifies the production archive fingerprint, signing UID, exact Cloudflare
  endpoint form, credential shape, clean canonical Git root and complete platform contract.
- `test-secret-hygiene.sh` scans every tracked and non-ignored source file for recognizable private
  token/key material while hiding any matched value.
- `release.sh` runs both checks before signing or publishing.
- R2 publication uses a bucket-scoped credential and publicly verifies the signed, immutable
  generation after upload.

Rotate the R2 credential on a fixed schedule and immediately after operator departure, suspected
machine compromise, accidental disclosure or unexpected publication activity.
