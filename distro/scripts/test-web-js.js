const fs = require("fs");
const vm = require("vm");

const mainPagePath = "orchestrator/internal/api/web/index.html";
const mainPage = fs.readFileSync(mainPagePath, "utf8");

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

// Exercise the display-scale controller as code, rather than merely asserting
// that its source parses. This is the browser-owned half of clean-install
// display qualification: automatic sizing must be deterministic, a manual
// accessibility choice must survive in the persistent kiosk profile, invalid
// values must fail closed, and manual sizing must not change on resize.
const zoomStart = mainPage.indexOf("const UI_ZOOM_KEY = 'cloudless.uiZoom';");
const zoomEnd = mainPage.indexOf("/* ---- status + gpu ---- */", zoomStart);
if (zoomStart < 0 || zoomEnd < 0) {
  throw new Error("Cloudless UI zoom controller could not be isolated");
}
const zoomController = mainPage.slice(zoomStart, zoomEnd);

function zoomContext(width, height, saved = null) {
  const values = new Map(saved === null ? [] : [["cloudless.uiZoom", saved]]);
  const styleValues = new Map();
  const listeners = new Map();
  const root = {
    dataset: {},
    style: {
      zoom: "",
      setProperty(name, value) { styleValues.set(name, value); },
    },
  };
  const body = { style: {} };
  const context = {
    innerWidth: width,
    innerHeight: height,
    document: { documentElement: root, body, getElementById() { return null; } },
    localStorage: {
      getItem(key) { return values.has(key) ? values.get(key) : null; },
      setItem(key, value) { values.set(key, String(value)); },
    },
    addEventListener(name, callback) { listeners.set(name, callback); },
    clearTimeout() {},
    setTimeout(callback) { callback(); return 1; },
    settingsOpen() { return false; },
    renderDisplayPage() {},
  };
  vm.createContext(context);
  new vm.Script(zoomController, { filename: `${mainPagePath}-zoom-controller.js` }).runInContext(context);
  return { context, root, body, values, styleValues, listeners };
}

function assertEqual(actual, expected, message) {
  if (actual !== expected) {
    throw new Error(`${message}: got ${JSON.stringify(actual)}, want ${JSON.stringify(expected)}`);
  }
}

const compact = zoomContext(1280, 720);
assertEqual(compact.root.dataset.uiScale, "0.8", "720p automatic scale");
assertEqual(compact.body.style.width, "1600px", "720p compensated body width");

const standard = zoomContext(1920, 1080);
assertEqual(standard.root.dataset.uiScale, "1", "1080p automatic scale");

const ultraHD = zoomContext(3840, 2160);
assertEqual(ultraHD.root.dataset.uiScale, "1.35", "4K automatic scale");

const accessible = zoomContext(1920, 1080, "3");
assertEqual(accessible.root.dataset.uiScale, "3", "saved accessibility scale");
assertEqual(accessible.root.dataset.uiMagnified, "1", "accessibility magnification marker");
accessible.context.setUIZoomPreference("2.5");
assertEqual(accessible.values.get("cloudless.uiZoom"), "2.5", "manual scale persistence");
assertEqual(accessible.root.dataset.uiScale, "2.5", "manual scale application");
accessible.context.setUIZoomPreference("9");
assertEqual(accessible.values.get("cloudless.uiZoom"), "2.5", "invalid manual scale rejection");
accessible.context.innerWidth = 1280;
accessible.context.innerHeight = 720;
accessible.listeners.get("resize")();
assertEqual(accessible.root.dataset.uiScale, "2.5", "manual scale stability after resize");

const invalid = zoomContext(1920, 1080, "not-a-scale");
assertEqual(invalid.root.dataset.uiZoomPreference, "auto", "invalid saved scale fallback");
assertEqual(invalid.root.dataset.uiScale, "1", "invalid saved scale automatic sizing");

console.log(`Validated ${scripts.length} inline Cloudless interface scripts and responsive UI scaling`);
