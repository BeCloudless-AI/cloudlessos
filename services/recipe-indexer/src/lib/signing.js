import { base64ToBytes, bytesToBase64, sha256Hex, stableStringify } from "./util.js";

const encoder = new TextEncoder();

export async function signCatalog(payload, config) {
  if (!config.signingPrivateKey) {
    if (config.environment === "production") throw new Error("CATALOG_SIGNING_PRIVATE_KEY is required in production");
    return {
      schema: "cloudless.recipe-catalog-envelope/v1",
      payload,
      signature: { algorithm: "none", keyId: config.signingKeyID, canonicalization: "cloudless-json-v1", value: "" }
    };
  }
  const canonical = stableStringify(payload);
  const privateKey = await crypto.subtle.importKey("pkcs8", base64ToBytes(config.signingPrivateKey), { name: "Ed25519" }, false, ["sign"]);
  const signature = await crypto.subtle.sign("Ed25519", privateKey, encoder.encode(canonical));
  return {
    schema: "cloudless.recipe-catalog-envelope/v1",
    payload,
    signature: {
      algorithm: "Ed25519",
      keyId: config.signingKeyID,
      canonicalization: "cloudless-json-v1",
      payloadDigest: await sha256Hex(canonical),
      value: bytesToBase64(new Uint8Array(signature))
    }
  };
}

export async function verifyCatalog(envelope, publicKeyBase64) {
  if (envelope?.signature?.algorithm === "none") return false;
  const publicKey = await crypto.subtle.importKey("spki", base64ToBytes(publicKeyBase64), { name: "Ed25519" }, false, ["verify"]);
  return crypto.subtle.verify(
    "Ed25519",
    publicKey,
    base64ToBytes(envelope.signature.value),
    encoder.encode(stableStringify(envelope.payload))
  );
}
