const fs = require("fs");
const vm = require("vm");

const sources = [
  "orchestrator/internal/api/web/index.html",
  "distro/packages/cloudless-shell/recovery.html",
];
const scripts = sources.flatMap((path) => {
  const source = fs.readFileSync(path, "utf8");
  return [...source.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g)]
    .filter((match) => !match[0].includes("src="))
    .map((match) => ({ path, script: match[1] }));
});

if (scripts.length === 0) {
  throw new Error("No inline Cloudless interface scripts were found");
}
scripts.forEach(({ path, script }, index) => {
  new vm.Script(script, { filename: `${path}-inline-${index}.js` });
});
console.log(`Validated ${scripts.length} inline Cloudless interface scripts`);
