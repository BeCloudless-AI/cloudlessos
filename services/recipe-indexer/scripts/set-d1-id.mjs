import { readFileSync, writeFileSync } from "node:fs";

const databaseID = process.argv[2];
if (!/^[0-9a-f-]{36}$/i.test(databaseID || "")) {
  throw new Error("set-d1-id requires a D1 database UUID");
}

const configURL = new URL("../wrangler.jsonc", import.meta.url);
const config = JSON.parse(readFileSync(configURL, "utf8"));
const binding = config.d1_databases?.find((database) => database.binding === "RECIPE_DB");
if (!binding) throw new Error("RECIPE_DB is missing from wrangler.jsonc");
if (binding.database_id === databaseID) process.exit(0);
binding.database_id = databaseID;
writeFileSync(configURL, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
console.log(`Configured RECIPE_DB as ${databaseID}.`);
