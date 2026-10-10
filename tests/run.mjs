// Browser tests for the web UI and the notifier mini-mod.
//
//   node tests/run.mjs            (needs Go and Chrome/Chromium)
//   SCREENSHOTS=out node tests/run.mjs   (also saves screenshots)
//
// Everything runs against mocks: a fake GitHub (tests/fakegithub), a fake
// Docker socket and a fake CasaOS page that loads the notifier like CasaMOD.
import { spawn, execFileSync } from "node:child_process";
import { createServer } from "node:http";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, extname, join, normalize, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { launchChrome, sleep } from "./harness.mjs";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const WORK = mkdtempSync(join(tmpdir(), "casamod-store-test-"));
const SHOTS = process.env.SCREENSHOTS ? resolve(process.env.SCREENSHOTS) : "";
if (SHOTS) mkdirSync(SHOTS, { recursive: true });
const procs = [];
const servers = [];

function log(...a) { console.log("•", ...a); }

function listen(server, arg) {
  return new Promise((res) => server.listen(arg, () => res(server.address())));
}

async function freePort() {
  const s = createServer();
  const { port } = await listen(s, { port: 0, host: "127.0.0.1" });
  await new Promise((r) => s.close(r));
  return port;
}

async function waitFile(path, ms = 15000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (existsSync(path) && readFileSync(path, "utf8")) return readFileSync(path, "utf8").trim();
    await sleep(50);
  }
  throw new Error("timeout waiting for " + path);
}

// ------------------------------------------------------------------ setup
log("building");
const bin = join(WORK, "bin");
execFileSync("go", ["build", "-o", join(bin, "casamod-store"), "./cmd/casamod-store"], { cwd: ROOT, stdio: "inherit" });
execFileSync("go", ["build", "-o", join(bin, "fakegithub"), "./tests/fakegithub"], { cwd: ROOT, stdio: "inherit" });

const readyFile = join(WORK, "gh-ready");
procs.push(spawn(join(bin, "fakegithub"), ["-ready", readyFile], { stdio: "ignore" }));
const GH = await waitFile(readyFile);

// Fake Docker: records restart calls.
const restarts = [];
const dockerSock = join(WORK, "docker.sock");
const docker = createServer((req, res) => {
  if (req.method === "POST" && /\/containers\/[^/]+\/restart/.test(req.url)) {
    restarts.push(req.url);
    res.writeHead(204).end();
  } else {
    res.writeHead(404).end("{}");
  }
});
servers.push(docker);
await listen(docker, dockerSock);

// Data folders, with a manual mod and a manual wallpaper.
const casamodDir = join(WORK, "casamod");
const modDir = join(casamodDir, "mod");
const dataDir = join(WORK, "data");
mkdirSync(join(modDir, "handmade-mod"), { recursive: true });
writeFileSync(join(modDir, "handmade-mod", "mod.js"), "// mine");
mkdirSync(join(modDir, "wallpapers", "my-own-wallpaper"), { recursive: true });
mkdirSync(dataDir, { recursive: true });

const storePort = await freePort();
writeFileSync(join(dataDir, "config.json"), JSON.stringify({
  check_interval_hours: 6, catalogs: ["me/catalog"], casamod_container: "casamod",
  include_casamod_mods: true, notifier: true, ui_port: storePort,
}));

const store = spawn(join(bin, "casamod-store"), [], {
  env: {
    ...process.env,
    CASAMOD_STORE_ADDR: `127.0.0.1:${storePort}`,
    CASAMOD_STORE_DATA: dataDir,
    CASAMOD_DIR: casamodDir,
    DOCKER_SOCKET: dockerSock,
    CASAMOD_STORE_RESTART_DELAY: "0.3",
    CASAMOD_STORE_COMMIT: "1111111111111111111111111111111111111111",
    GITHUB_API_URL: GH + "/api",
    GITHUB_CODELOAD_URL: GH + "/codeload",
    GITHUB_RAW_URL: GH + "/raw",
  },
  stdio: ["ignore", "pipe", "pipe"],
});
procs.push(store);
let storeLog = "";
store.stdout.on("data", (d) => (storeLog += d));
store.stderr.on("data", (d) => (storeLog += d));
const STORE = `http://127.0.0.1:${storePort}`;
for (let i = 0; ; i++) {
  try { if ((await fetch(STORE + "/healthz")).ok) break; } catch {}
  if (i > 100) throw new Error("store did not start:\n" + storeLog);
  await sleep(100);
}

// Fake CasaOS: serves the mock index.html and /mod/ from the CasaMOD folder,
// like the symlink CasaMOD creates in /var/lib/casaos/www.
const TYPES = { ".js": "text/javascript", ".json": "application/json", ".css": "text/css", ".html": "text/html" };
const appUpdates = []; // PATCH /v2/app_management/compose/<id> calls (CasaOS "Check then update")
const casaos = createServer((req, res) => {
  const url = new URL(req.url, "http://x");
  if (req.method === "PATCH" && url.pathname.startsWith("/v2/app_management/compose/")) {
    appUpdates.push({ path: url.pathname, force: url.searchParams.get("force"), auth: req.headers.authorization });
    res.writeHead(200, { "Content-Type": "application/json" });
    return res.end(JSON.stringify({ message: "compose app is being updated asynchronously" }));
  }
  let file;
  if (url.pathname === "/") file = join(ROOT, "tests/casaos/index.html");
  else if (url.pathname.startsWith("/mod/")) {
    file = normalize(join(modDir, decodeURIComponent(url.pathname.slice(5))));
    if (!file.startsWith(modDir)) return res.writeHead(403).end();
  }
  if (!file || !existsSync(file) || statSync(file).isDirectory()) return res.writeHead(404).end("not found");
  res.writeHead(200, { "Content-Type": TYPES[extname(file)] || "application/octet-stream", "Cache-Control": "no-store" });
  res.end(readFileSync(file));
});
servers.push(casaos);
const { port: casaPort } = await listen(casaos, { port: 0, host: "127.0.0.1" });
const CASAOS = `http://127.0.0.1:${casaPort}`;

async function push(repo, files, message = "update") {
  const r = await fetch(GH + "/__push", { method: "POST", body: JSON.stringify({ repo, branch: "main", message, files }) });
  assert.ok(r.ok);
}

async function waitRestarts(n) {
  const end = Date.now() + 5000;
  while (Date.now() < end && restarts.length < n) await sleep(50);
  await sleep(500); // debounce window: make sure no extra restart follows
  assert.equal(restarts.length, n, "CasaMOD restarts");
}

// ------------------------------------------------------------------ tests
const results = [];
async function test(name, fn) {
  const t0 = Date.now();
  try {
    let timer;
    await Promise.race([fn(), new Promise((_, rej) => (timer = setTimeout(() => rej(new Error("test timed out")), 60000)))]);
    clearTimeout(timer);
    results.push([name, true]);
    console.log(`  ✓ ${name} (${Date.now() - t0} ms)`);
  } catch (e) {
    results.push([name, false]);
    try {
      for (const ep of ["state", "store"]) {
        const t1 = Date.now();
        const r = await Promise.race([fetch(STORE + "/api/" + ep).then((r) => r.status), sleep(5000).then(() => "hung")]);
        console.log(`    /api/${ep}: ${r} in ${Date.now() - t1} ms`);
      }
      console.log("    page: " + JSON.stringify((await page.eval("document.body.innerText")).slice(0, 400)));
    } catch (d) { console.log("    debug failed: " + d.message); }
    console.log(`  ✗ ${name}\n${e.stack}`);
  }
}

const browser = await launchChrome();
const page = await browser.newPage();
const shot = (name) => (SHOTS ? page.screenshot(join(SHOTS, name + ".png")) : null);

try {
  await test("notifier mod installed at start and CasaMOD restarted once", async () => {
    assert.ok(existsSync(join(modDir, "casamod-store-notifier", "mod.js")));
    assert.ok(existsSync(join(modDir, "casamod-store-notifier", "status.json")));
    await waitRestarts(1);
  });

  await test("store lists catalog and CasaMOD items", async () => {
    await page.goto(STORE + "/?lang=en");
    await page.waitFor(`document.querySelectorAll("#store-grid .card").length === 6`, { message: "6 cards" });
    const keys = await page.eval(`[...document.querySelectorAll("#store-grid .card")].map(c => c.dataset.key).sort().join(",")`);
    assert.equal(keys, "mod:live-wallpapers,mod:music-player,mod:show-dotfiles,mod:widget-sortable,wallpaper:plexus-wallpaper,wallpaper:star-wallpaper");
    // Filters.
    await page.click(`#type-filter [data-type="wallpaper"]`);
    assert.equal(await page.eval(`document.querySelectorAll("#store-grid .card").length`), 2);
    await page.click(`#type-filter [data-type=""]`);
    await page.eval(`(() => { const s = document.getElementById("search"); s.value = "music"; s.dispatchEvent(new Event("input")); })()`);
    assert.equal(await page.eval(`document.querySelectorAll("#store-grid .card").length`), 1);
    await page.eval(`(() => { const s = document.getElementById("search"); s.value = ""; s.dispatchEvent(new Event("input")); })()`);
    assert.deepEqual(await page.eval(`[...document.querySelectorAll(".credits a")].map(a => a.href)`),
      ["https://github.com/UsuarioDeJava/CasaMOD-Store", "https://github.com/UsuarioDeJava"]);
    // Without a preview the owner's GitHub picture is tried, then the icon
    // (github.com is not reachable from the tests, so the icon remains).
    await page.waitFor(`[...document.querySelectorAll("#store-grid .preview")].every(p => p.querySelector("img") || /🧩|🖼/.test(p.textContent))`);
    await shot("01-store");
  });

  await test("install one mod: files on disk, restart, card shows installed", async () => {
    await page.click(`.card[data-key="mod:live-wallpapers"] .actions .btn`);
    await page.waitFor(`!!document.querySelector('.card[data-key="mod:live-wallpapers"] .badge.ok')`, { message: "installed badge" });
    assert.equal(readFileSync(join(modDir, "live-wallpapers", "mod.js"), "utf8"), "console.log('live v1')");
    await waitRestarts(2);
    const installed = JSON.parse(readFileSync(join(dataDir, "installed.json"), "utf8"));
    assert.equal(installed.items[0].id, "live-wallpapers");
    assert.equal(installed.items[0].policy, "notify");
    assert.equal(installed.items[0].source.branch, "main");
  });

  await test("batch install restarts CasaMOD only once; wallpapers never", async () => {
    await page.eval(`(() => {
      for (const k of ["mod:music-player", "mod:widget-sortable", "wallpaper:star-wallpaper"]) {
        const cb = document.querySelector('.card[data-key="' + k + '"] input.select');
        cb.checked = true; cb.dispatchEvent(new Event("change"));
      }
    })()`);
    assert.match(await page.text("#selection-text"), /3 selected/);
    await page.click("#btn-install-selected");
    await page.waitFor(`document.querySelectorAll("#store-grid .badge.ok").length === 4`, { message: "4 installed" });
    assert.ok(existsSync(join(modDir, "music-player", "mod.css")));
    assert.ok(existsSync(join(modDir, "widget-sortable", "mod.js")));
    assert.ok(existsSync(join(modDir, "wallpapers", "star-wallpaper", "index.html")));
    await waitRestarts(3);
  });

  await test("add from GitHub URL (wallpaper): no restart", async () => {
    await page.click("#btn-add-url");
    await page.eval(`document.getElementById("url-input").value = "https://github.com/me/walls/tree/main/extra-wallpaper"`);
    await page.click("#btn-resolve");
    await page.waitFor(`!document.getElementById("url-details").hidden`);
    assert.equal(await page.eval(`document.getElementById("url-type").value`), "wallpaper");
    assert.equal(await page.eval(`document.getElementById("url-id").value`), "extra-wallpaper");
    await page.eval(`document.getElementById("url-policy").value = "auto"`);
    await shot("02-add-url");
    await page.click("#btn-url-install");
    await page.waitFor(`[...document.querySelectorAll(".toast")].some(t => /Installed extra-wallpaper/.test(t.textContent))`);
    assert.ok(existsSync(join(modDir, "wallpapers", "extra-wallpaper", "index.html")));
    await waitRestarts(3);
  });

  await test("installed tab lists managed and manual items", async () => {
    await page.goto(STORE + "/?lang=en#installed");
    await page.waitFor(`document.querySelectorAll("#installed-list .row-item").length === 5`, { message: "5 installed rows" });
    const manual = await page.eval(`[...document.querySelectorAll("#manual-list .row-item")].map(r => r.dataset.key).join(",")`);
    assert.equal(manual, "mod:handmade-mod,wallpaper:my-own-wallpaper");
    assert.equal(await page.eval(`document.querySelector('#installed-list [data-key="wallpaper:extra-wallpaper"] select.policy').value`), "auto");
    await shot("03-installed");
  });

  await test("installing over a manual folder asks first", async () => {
    // Put "show-dotfiles" by hand, then install it from the store.
    mkdirSync(join(modDir, "show-dotfiles"));
    writeFileSync(join(modDir, "show-dotfiles", "mod.js"), "// hand made");
    await page.goto(STORE + "/?lang=en#store");
    await page.waitFor(`!!document.querySelector('.card[data-key="mod:show-dotfiles"] .badge.warn')`, { message: "manual badge" });
    page.acceptDialogs = false;
    page.dialogs.length = 0;
    await page.click(`.card[data-key="mod:show-dotfiles"] .actions .btn`);
    await page.waitFor(`true`);
    const end = Date.now() + 5000;
    while (!page.dialogs.length && Date.now() < end) await sleep(50);
    assert.match(page.dialogs[0] || "", /already exists/);
    await sleep(300);
    assert.equal(readFileSync(join(modDir, "show-dotfiles", "mod.js"), "utf8"), "// hand made");
    page.acceptDialogs = true;
    await page.click(`.card[data-key="mod:show-dotfiles"] .actions .btn`);
    await page.waitFor(`!!document.querySelector('.card[data-key="mod:show-dotfiles"] .badge.ok')`, { message: "replaced" });
    assert.equal(readFileSync(join(modDir, "show-dotfiles", "mod.js"), "utf8"), "y");
    await waitRestarts(3); // the folder already existed: no restart
  });

  await test("update check finds updates; auto policy applies them", async () => {
    await push("me/mods", { "casaos-live-wallpapers/live-wallpapers/mod.js": "console.log('live v2')" }, "live v2");
    await push("me/walls", { "extra-wallpaper/index.html": "<html>extra v2</html>" }, "extra v2");
    await page.goto(STORE + "/?lang=en#updates");
    await page.waitFor(`document.getElementById("updates-empty").hidden === false`);
    await page.click("#btn-check");
    await page.waitFor(`document.querySelectorAll("#updates-list .row-item").length === 1`, { message: "1 update row" });
    assert.equal(await page.eval(`document.querySelector("#updates-list .row-item").dataset.key`), "mod:live-wallpapers");
    assert.match(await page.text("#updates-list .sub"), /[0-9a-f]{7} → [0-9a-f]{7}/);
    assert.equal(await page.text("#count-updates"), "1");
    // notify: not applied yet; auto: applied.
    assert.equal(readFileSync(join(modDir, "live-wallpapers", "mod.js"), "utf8"), "console.log('live v1')");
    assert.equal(readFileSync(join(modDir, "wallpapers", "extra-wallpaper", "index.html"), "utf8"), "<html>extra v2</html>");
    const status = JSON.parse(readFileSync(join(modDir, "casamod-store-notifier", "status.json"), "utf8"));
    assert.deepEqual(status.items.map((i) => i.id), ["live-wallpapers"]);
    await shot("04-updates");
  });

  const casa = await browser.newPage();
  await test("notifier shows the update on the CasaOS desktop (pt_br)", async () => {
    await casa.goto(CASAOS + "/");
    await casa.eval(`localStorage.setItem("access_token", "x"); localStorage.setItem("lang", "pt_br"); localStorage.removeItem("casamod_store_notifier_dismissed")`);
    await casa.reload();
    await casa.waitFor(`!!document.getElementById("casamod-store-notifier")`, { message: "notifier" });
    assert.equal(await casa.text(".casamod-store-notifier-text"), "Atualizações disponíveis: 1");
    assert.equal(await casa.text(".casamod-store-notifier-open"), "Abrir loja");
    assert.equal(await casa.eval(`document.querySelector(".casamod-store-notifier-open").href`), `http://127.0.0.1:${storePort}/#updates`);
    if (SHOTS) await casa.screenshot(join(SHOTS, "05-notifier.png"));
  });

  await test("notifier: dismiss persists until the set of updates changes", async () => {
    await casa.click(".casamod-store-notifier-dismiss");
    assert.equal(await casa.eval(`!!document.getElementById("casamod-store-notifier")`), false);
    await casa.reload();
    await sleep(500);
    assert.equal(await casa.eval(`!!document.getElementById("casamod-store-notifier")`), false);
    await push("me/mods", { "casaos-music-player/music-player-widget/mod.js": "console.log('music v2')" }, "music v2");
    const r = await fetch(STORE + "/api/check", { method: "POST", headers: { "X-CasaMOD-Store": "1" } });
    assert.ok(r.ok);
    await casa.reload();
    await casa.waitFor(`!!document.getElementById("casamod-store-notifier")`, { message: "notifier back" });
    assert.equal(await casa.text(".casamod-store-notifier-text"), "Atualizações disponíveis: 2");
  });

  await test("notifier: other languages and logged out", async () => {
    await casa.eval(`localStorage.setItem("lang", "zh_cn")`);
    await casa.reload();
    await casa.waitFor(`(document.querySelector(".casamod-store-notifier-text") || {}).textContent === "可用更新：2"`);
    await casa.eval(`localStorage.setItem("lang", "xx_yy")`);
    await casa.reload();
    await casa.waitFor(`(document.querySelector(".casamod-store-notifier-text") || {}).textContent === "Updates available: 2"`);
    await casa.eval(`localStorage.removeItem("access_token")`);
    await casa.reload();
    await sleep(500);
    assert.equal(await casa.eval(`!!document.getElementById("casamod-store-notifier")`), false);
    await casa.eval(`localStorage.setItem("access_token", "x")`);
  });

  await test("update all from the UI; no restart; notifier clears", async () => {
    await page.goto(STORE + "/?lang=pt#updates");
    await page.waitFor(`document.querySelectorAll("#updates-list .row-item").length === 2`);
    assert.equal(await page.text("#btn-update-all"), "Atualizar tudo");
    await page.click("#btn-update-all");
    await page.waitFor(`document.getElementById("updates-empty").hidden === false`, { message: "no updates left" });
    assert.equal(readFileSync(join(modDir, "live-wallpapers", "mod.js"), "utf8"), "console.log('live v2')");
    assert.equal(readFileSync(join(modDir, "music-player", "mod.js"), "utf8"), "console.log('music v2')");
    await waitRestarts(3);
    await casa.reload();
    await sleep(500);
    assert.equal(await casa.eval(`!!document.getElementById("casamod-store-notifier")`), false);
  });

  await test("store announces a new version of itself (UI and notifier)", async () => {
    await page.goto(STORE + "/?lang=pt#updates");
    await page.waitFor(`document.getElementById("updates-empty").hidden === false`);
    assert.equal(await page.eval(`!!document.querySelector('[data-key="store:casamod-store"]')`), false);
    const r = await fetch(GH + "/__run", { method: "POST", body: JSON.stringify({
      repo: "UsuarioDeJava/CasaMOD-Store", workflow: "docker-publish.yml", branch: "main", sha: "2222222222222222222222222222222222222222" }) });
    assert.ok(r.ok);
    await page.click("#btn-check");
    await page.waitFor(`!!document.querySelector('[data-key="store:casamod-store"]')`, { message: "self row" });
    assert.equal(await page.text('#updates-list [data-key="store:casamod-store"] .sub'), "1111111 → 2222222");
    assert.equal(await page.text("#count-updates"), "1");
    await shot("05b-self-update");
    await casa.reload();
    await casa.waitFor(`!!document.getElementById("casamod-store-notifier")`, { message: "notifier for self update" });
    assert.equal(await casa.text(".casamod-store-notifier-text"), "Updates available: 1");
    assert.match(await casa.eval(`document.getElementById("casamod-store-notifier").title`), /CasaMOD Store/);
  });

  await test("store set to update itself: the notice asks CasaOS to update it, once", async () => {
    await page.goto(STORE + "/?lang=pt#updates");
    const sel = '#updates-list [data-key="store:casamod-store"] select.policy';
    await page.waitFor(`!!document.querySelector('${sel}')`);
    await page.eval(`(() => { const s = document.querySelector('${sel}'); s.value = "auto"; s.dispatchEvent(new Event("change")); })()`);
    const statusFile = join(modDir, "casamod-store-notifier", "status.json");
    const end = Date.now() + 5000;
    while (!JSON.parse(readFileSync(statusFile, "utf8")).self_update && Date.now() < end) await sleep(50);
    const status = JSON.parse(readFileSync(statusFile, "utf8"));
    assert.deepEqual(status.self_update, { app_id: "casamod-store", latest: "2222222222222222222222222222222222222222" });
    assert.ok(!status.items.some((i) => i.type === "store"), "store still in the notice");
    await page.goto(STORE + "/?lang=pt#settings");
    await page.waitFor(`document.getElementById("set-self-policy").value === "auto"`);

    await casa.eval(`localStorage.setItem("access_token", "tok-123"); localStorage.removeItem("casamod_store_self_update")`);
    await casa.reload();
    const end2 = Date.now() + 5000;
    while (!appUpdates.length && Date.now() < end2) await sleep(50);
    assert.deepEqual(appUpdates, [{ path: "/v2/app_management/compose/casamod-store", force: "true", auth: "tok-123" }]);
    await casa.reload();
    await sleep(800);
    assert.equal(appUpdates.length, 1, "asked CasaOS twice for the same image");
    await casa.eval(`localStorage.setItem("access_token", "x")`);
  });

  await test("remove a mod restarts CasaMOD; manual folders stay", async () => {
    await page.goto(STORE + "/?lang=en#installed");
    await page.waitFor(`!!document.querySelector('#installed-list [data-key="mod:music-player"]')`);
    page.acceptDialogs = true;
    await page.click(`#installed-list [data-key="mod:music-player"] .btn.danger`);
    await page.waitFor(`!document.querySelector('#installed-list [data-key="mod:music-player"]')`, { message: "row gone" });
    assert.equal(existsSync(join(modDir, "music-player")), false);
    assert.ok(existsSync(join(modDir, "handmade-mod", "mod.js")));
    await waitRestarts(4);
  });

  await test("policy can be changed per item", async () => {
    await page.eval(`(() => { const s = document.querySelector('#installed-list [data-key="mod:live-wallpapers"] select.policy'); s.value = "auto"; s.dispatchEvent(new Event("change")); })()`);
    await sleep(500);
    const installed = JSON.parse(readFileSync(join(dataDir, "installed.json"), "utf8"));
    assert.equal(installed.items.find((i) => i.id === "live-wallpapers").policy, "auto");
  });

  await test("settings: token is saved but never sent back", async () => {
    await page.goto(STORE + "/?lang=en#settings");
    await page.waitFor(`document.getElementById("set-port").value === "${storePort}"`);
    assert.equal(await page.eval(`document.getElementById("set-token").placeholder`), "ghp_…");
    await page.eval(`(() => {
      document.getElementById("set-token").value = "ghp_secret123";
      document.getElementById("set-interval").value = "12";
      document.getElementById("settings-form").dispatchEvent(new Event("input"));
    })()`);
    await page.click("#btn-save-settings");
    await page.waitFor(`document.getElementById("set-token").placeholder === "Token saved"`);
    const state = await (await fetch(STORE + "/api/state")).text();
    assert.ok(!state.includes("ghp_secret123"), "token leaked");
    const cfg = JSON.parse(readFileSync(join(dataDir, "config.json"), "utf8"));
    assert.equal(cfg.github_token, "ghp_secret123");
    assert.equal(cfg.check_interval_hours, 12);
    await shot("06-settings");
    await page.click("#btn-clear-token");
    await page.waitFor(`document.getElementById("set-token").placeholder === "ghp_…"`);
  });

  await test("installing a wallpaper without Live Wallpapers offers it", async () => {
    const post = (path, body) => fetch(STORE + "/api/" + path, {
      method: "POST", headers: { "X-CasaMOD-Store": "1", "Content-Type": "application/json" }, body: JSON.stringify(body),
    }).then((r) => r.json());
    await post("remove", { keys: ["mod:live-wallpapers"] });
    const before = restarts.length;
    await page.goto(STORE + "/?lang=pt#store");
    const plexus = '.card[data-key="wallpaper:plexus-wallpaper"] .actions .btn';
    await page.waitFor(`!!document.querySelector('${plexus}')`);

    // OK: only the wallpaper is installed.
    await page.click(plexus);
    await page.waitFor(`document.getElementById("dlg-lw").open`, { message: "dialog" });
    assert.equal(await page.text("#dlg-lw p"), "Os wallpapers precisam do mod Live Wallpapers, que não está instalado.");
    await shot("07c-live-wallpapers-dialog");
    await page.click("#btn-lw-ok");
    await page.waitFor(`!!document.querySelector('.card[data-key="wallpaper:plexus-wallpaper"] .badge.ok')`, { message: "wallpaper installed" });
    assert.equal(existsSync(join(modDir, "live-wallpapers")), false);

    // Install now: Live Wallpapers comes along, with one restart.
    await post("remove", { keys: ["wallpaper:plexus-wallpaper"] });
    await page.goto(STORE + "/?lang=pt#store");
    await page.waitFor(`!!document.querySelector('${plexus}')`);
    await page.click(plexus);
    await page.waitFor(`document.getElementById("dlg-lw").open`);
    await page.click("#btn-lw-install");
    await page.waitFor(`!!document.querySelector('.card[data-key="mod:live-wallpapers"] .badge.ok') && !!document.querySelector('.card[data-key="wallpaper:plexus-wallpaper"] .badge.ok')`, { message: "both installed" });
    assert.ok(existsSync(join(modDir, "live-wallpapers", "mod.js")));
    assert.ok(existsSync(join(modDir, "wallpapers", "plexus-wallpaper", "index.html")));
    await waitRestarts(before + 2); // one for the removal, one for the batch

    // With Live Wallpapers present there is no question.
    await post("remove", { keys: ["wallpaper:plexus-wallpaper"] });
    await page.goto(STORE + "/?lang=pt#store");
    await page.waitFor(`!!document.querySelector('${plexus}')`);
    await page.click(plexus);
    await page.waitFor(`!!document.querySelector('.card[data-key="wallpaper:plexus-wallpaper"] .badge.ok')`);
    assert.equal(await page.eval(`document.getElementById("dlg-lw").open`), false);
  });

  await test("missing Docker socket is shown", async () => {
    await new Promise((r) => docker.close(r));
    rmSync(dockerSock, { force: true });
    await page.goto(STORE + "/?lang=en#store");
    await page.waitFor(`!!document.querySelector('.card[data-key="mod:music-player"] .actions .btn')`);
    await page.click(`.card[data-key="mod:music-player"] .actions .btn`);
    await page.waitFor(`!!document.getElementById("banner-docker") && !document.getElementById("banner-restart")`, { message: "banners", timeout: 15000 });
    await shot("07-restart-banner");
  });

  await test("sources that break the folder rules are flagged", async () => {
    const r = await fetch(STORE + "/api/settings", {
      method: "POST", headers: { "X-CasaMOD-Store": "1", "Content-Type": "application/json" },
      body: JSON.stringify({ catalogs: ["me/catalog", "me/odd", "me/empty"] }),
    });
    assert.ok(r.ok);
    await page.goto(STORE + "/?lang=pt#store");
    await page.waitFor(`document.querySelectorAll(".source-warning").length === 3`, { message: "3 warnings" });
    const texts = await page.eval(`[...document.querySelectorAll(".source-warning")].map(w => w.dataset.kind + "|" + w.textContent)`);
    assert.deepEqual(texts, [
      "ignored|odd: star é um mod, então star/wallpaper-engine dentro dele é ignorado",
      'same_id|odd: nebula/wallpaper-engine, plexus/wallpaper-engine têm o mesmo nome "wallpaper-engine" e seriam instalados na mesma pasta',
      "empty|empty: nenhum mod ou wallpaper encontrado",
    ]);
    await shot("07b-source-warnings");
  });

  await test("phone width: no horizontal scroll", async () => {
    await page.viewport(390, 844, true);
    for (const tab of ["store", "installed", "updates", "settings"]) {
      await page.goto(STORE + "/?lang=pt#" + tab);
      await page.waitFor(`!document.getElementById("tab-${tab}").hidden`);
      await sleep(300);
      const over = await page.eval(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
      assert.ok(over <= 0, `${tab}: page is ${over}px wider than the screen`);
    }
    await shot("08-phone");
    await page.viewport(1280, 900);
  });

  await test("all 31 languages load; browser language is detected", async () => {
    const cases = [["de", "Einstellungen"], ["ja-JP", "設定"], ["zh-TW", "設定"], ["zh", "设置"], ["pt-PT", "Definições"],
      ["pt", "Configurações"], ["nb", "Innstillinger"], ["ar", "الإعدادات"], ["xx", "Settings"]];
    for (const [lang, settings] of cases) {
      await page.goto(STORE + "/?lang=" + lang + "#settings");
      await page.waitFor(`document.querySelector('[data-tab="settings"]').textContent === ${JSON.stringify(settings)}`, { message: lang });
    }
    assert.equal(await page.eval(`document.documentElement.dir`), "ltr");
    await page.goto(STORE + "/?lang=ar#store");
    await page.waitFor(`document.documentElement.dir === "rtl" && document.documentElement.lang === "ar-sa"`);
    await shot("09-arabic");
    // Without ?lang the browser's languages decide.
    const { userAgent } = await browser.send("Browser.getVersion");
    await page.send("Emulation.setUserAgentOverride", { userAgent, acceptLanguage: "fr-FR,fr" });
    await page.goto(STORE + "/#store");
    await page.waitFor(`document.querySelector('[data-tab="updates"]').textContent.trim().startsWith("Mises à jour")`, { message: "fr from browser" });
    await page.send("Emulation.setUserAgentOverride", { userAgent, acceptLanguage: "en-US,en" });
  });

  await test("long translations fit on a phone", async () => {
    await page.viewport(390, 844, true);
    for (const lang of ["de", "ru", "el", "fi" /* falls back to English */, "vi", "hu"]) {
      for (const tab of ["store", "installed", "updates", "settings"]) {
        await page.goto(STORE + "/?lang=" + lang + "#" + tab);
        await page.waitFor(`!document.getElementById("tab-${tab}").hidden`);
        const over = await page.eval(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
        assert.ok(over <= 0, `${lang}/${tab}: page is ${over}px wider than the screen`);
      }
    }
    await page.viewport(1280, 900);
  });

  await test("no JavaScript errors", async () => {
    assert.deepEqual(page.errors, []);
    assert.deepEqual(casa.errors, []);
  });
} finally {
  await browser.close();
  for (const p of procs) p.kill();
  for (const s of servers) s.close();
  rmSync(WORK, { recursive: true, force: true });
}

const failed = results.filter((r) => !r[1]);
console.log(`\n${results.length - failed.length}/${results.length} passed`);
if (failed.length) {
  console.log("\nstore log:\n" + storeLog);
  process.exit(1);
}
