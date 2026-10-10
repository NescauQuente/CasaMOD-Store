// Minimal Chrome DevTools Protocol harness: no npm dependencies, works with
// any Chrome/Chromium (set CHROME_BIN when it is not found automatically).
import { spawn, execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const CANDIDATES = [
  "/opt/pw-browsers/chromium/chrome-linux/chrome",
  "/usr/bin/google-chrome",
  "/usr/bin/google-chrome-stable",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
];

function findChrome() {
  if (process.env.CHROME_BIN) return process.env.CHROME_BIN;
  for (const c of CANDIDATES) if (existsSync(c)) return c;
  try {
    const dir = "/opt/pw-browsers";
    const found = execFileSync("sh", ["-c", `ls -d ${dir}/chromium-*/chrome-linux/chrome 2>/dev/null | head -1`]).toString().trim();
    if (found) return found;
  } catch {}
  throw new Error("Chrome not found: set CHROME_BIN");
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function launchChrome() {
  const profile = mkdtempSync(join(tmpdir(), "casamod-store-chrome-"));
  const proc = spawn(findChrome(), [
    "--headless=new", "--no-sandbox", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
    "--disable-extensions", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank",
  ], { stdio: "ignore" });
  const portFile = join(profile, "DevToolsActivePort");
  for (let i = 0; i < 200 && !existsSync(portFile); i++) await sleep(50);
  const [port, path] = readFileSync(portFile, "utf8").trim().split("\n");
  const ws = new WebSocket(`ws://127.0.0.1:${port}${path}`);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  const browser = new Browser(ws, proc, profile);
  return browser;
}

class Browser {
  constructor(ws, proc, profile) {
    this.ws = ws; this.proc = proc; this.profile = profile;
    this.id = 0; this.pending = new Map(); this.listeners = [];
    ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) reject(new Error(msg.error.message)); else resolve(msg.result);
      } else if (msg.method) {
        for (const l of this.listeners) l(msg);
      }
    };
  }
  send(method, params = {}, sessionId) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params, sessionId }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }
  async newPage({ width = 1280, height = 900 } = {}) {
    const { targetId } = await this.send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await this.send("Target.attachToTarget", { targetId, flatten: true });
    const page = new Page(this, sessionId);
    await page.init(width, height);
    return page;
  }
  async close() {
    try { await this.send("Browser.close"); } catch {}
    this.proc.kill();
    await sleep(200);
    try { rmSync(this.profile, { recursive: true, force: true }); } catch {}
  }
}

class Page {
  constructor(browser, sessionId) {
    this.b = browser; this.sid = sessionId;
    this.errors = []; this.dialogs = []; this.loaded = null;
    browser.listeners.push((msg) => {
      if (msg.sessionId !== sessionId) return;
      if (msg.method === "Runtime.exceptionThrown") {
        this.errors.push(msg.params.exceptionDetails.exception?.description || msg.params.exceptionDetails.text);
      } else if (msg.method === "Runtime.consoleAPICalled" && msg.params.type === "error") {
        this.errors.push(msg.params.args.map((a) => a.value ?? a.description).join(" "));
      } else if (msg.method === "Page.javascriptDialogOpening") {
        this.dialogs.push(msg.params.message);
        this.send("Page.handleJavaScriptDialog", { accept: this.acceptDialogs !== false });
      } else if (msg.method === "Page.loadEventFired" && this.loaded) {
        this.loaded();
      }
    });
  }
  send(method, params) { return this.b.send(method, params, this.sid); }
  async init(width, height) {
    await this.send("Page.enable");
    await this.send("Runtime.enable");
    await this.viewport(width, height);
  }
  viewport(width, height, mobile = false) {
    return this.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile });
  }
  async goto(url) {
    // Always load a fresh document, even when only the hash changes.
    for (const target of ["about:blank", url]) {
      const loaded = new Promise((r) => (this.loaded = r));
      await this.send("Page.navigate", { url: target });
      await loaded;
      this.loaded = null;
    }
  }
  async reload() {
    const loaded = new Promise((r) => (this.loaded = r));
    await this.send("Page.reload", { ignoreCache: true });
    await loaded;
    this.loaded = null;
  }
  async eval(expression) {
    const r = await this.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(`eval failed: ${r.exceptionDetails.exception?.description || r.exceptionDetails.text}\n  ${expression}`);
    return r.result.value;
  }
  async waitFor(expression, { timeout = 10000, message } = {}) {
    const end = Date.now() + timeout;
    let last;
    while (Date.now() < end) {
      try { last = await this.eval(expression); if (last) return last; } catch (e) { last = e.message; }
      await sleep(100);
    }
    throw new Error(`timeout waiting for ${message || expression} (last: ${JSON.stringify(last)})`);
  }
  click(selector) {
    return this.eval(`(() => { const e = document.querySelector(${JSON.stringify(selector)}); if (!e) throw new Error("no element ${selector.replace(/"/g, "'")}"); e.click(); return true; })()`);
  }
  text(selector) {
    return this.eval(`(document.querySelector(${JSON.stringify(selector)}) || {}).textContent || ""`);
  }
  async screenshot(path) {
    const { data } = await this.send("Page.captureScreenshot", { format: "png" });
    writeFileSync(path, Buffer.from(data, "base64"));
  }
}
