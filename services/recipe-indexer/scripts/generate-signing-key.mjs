import { generateKeyPairSync, randomBytes } from "node:crypto";

const { privateKey, publicKey } = generateKeyPairSync("ed25519");
const privateKeyBase64 = privateKey.export({ format: "der", type: "pkcs8" }).toString("base64");
const publicKeyBase64 = publicKey.export({ format: "der", type: "spki" }).toString("base64");
const adminToken = randomBytes(32).toString("base64url");

process.stdout.write([
  "Generated a new Recipe Indexer signing identity.",
  "",
  "Store these three values in Cloudflare Worker secrets; do not commit them:",
  `CATALOG_SIGNING_PRIVATE_KEY=${privateKeyBase64}`,
  `CATALOG_SIGNING_PUBLIC_KEY=${publicKeyBase64}`,
  `ADMIN_TOKEN=${adminToken}`,
  "",
  "The public key is safe to distribute to CloudlessOS clients. The private key and admin token are not."
].join("\n"));
