const fs = require("fs");
const vm = require("vm");

const source = fs.readFileSync(
  "orchestrator/internal/api/web/index.html",
  "utf8",
);
const scripts = [...source.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g)]
  .filter((match) => !match[0].includes("src="))
  .map((match) => match[1]);

if (scripts.length === 0) {
  throw new Error("No inline Cloudless interface scripts were found");
}
scripts.forEach((script, index) => {
  new vm.Script(script, { filename: `cloudless-inline-${index}.js` });
});
console.log(`Validated ${scripts.length} inline Cloudless interface scripts`);
