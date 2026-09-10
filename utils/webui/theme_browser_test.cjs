// Browser-only checks against the running demo. Playwright is a test tool only.
// Uses Debian's node-playwright and chromium; never downloads a browser.
// node --test utils/webui/theme_browser_test.cjs
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const { join } = require("node:path");
const { tmpdir } = require("node:os");
const { promisify } = require("node:util");
const bundle = require("playwright-core/lib/utilsBundle");
const { chromium } = require("playwright");
const base = process.env.WEBUI_TEST_URL || "http://127.0.0.1:18080";

// Debian 1.38.0+ds-3 ships callback-based rimraf, while fileUtils expects a Promise.
// Adapt only this known version in this test process; keep sync cleanup intact.
// Remove after the distro fixes the package. No system files are modified.
if (require("playwright-core/package.json").version === "1.38.0" && bundle.rimraf.length === 3) {
  const original = bundle.rimraf;
  bundle.rimraf = Object.assign(promisify(original), { sync: original.sync });
}

test("Playwright cleanup removes temporary directories with async and sync APIs", async t => {
  const root = await fs.mkdtemp(join(tmpdir(), "webui-cleanup-test-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const asyncDir = join(root, "async");
  await fs.mkdir(asyncDir);
  await fs.writeFile(join(asyncDir, "file"), "test");
  await bundle.rimraf(asyncDir, { maxRetries: 10 });
  await assert.rejects(fs.stat(asyncDir), { code: "ENOENT" });
  const syncDir = join(root, "sync");
  await fs.mkdir(syncDir);
  bundle.rimraf.sync(syncDir);
  await assert.rejects(fs.stat(syncDir), { code: "ENOENT" });
});

test("theme CSS switching, persistence, failures, and native forms", async () => {
  const browser = await chromium.launch({ headless: true, executablePath: "/usr/bin/chromium", chromiumSandbox: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, colorScheme: "light" });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.goto(base);
    const current = name => page.waitForFunction(name => {
      const sheet = document.querySelector("link[data-webui-theme-sheet]");
      return sheet.dataset.webuiThemeSheet === name && !document.querySelector("[data-webui-theme-controls]").matches('[aria-busy="true"]');
    }, name);
    for (const [id, name] of [["sand", "燕麦"], ["sage", "鼠尾草"], ["dusk", "暮色"], ["rose", "玫瑰灰"]]) {
      await page.getByRole("button", { name, exact: true }).click();
      await current(id);
      assert.equal(await page.locator("link[data-webui-theme-sheet]").count(), 1);
      assert.match(await page.locator("link[data-webui-theme-sheet]").getAttribute("href"), new RegExp("/themes/" + id + "\\.css$"));
      assert.equal(await page.getByRole("button", { name, exact: true }).getAttribute("aria-pressed"), "true");
      const fill = await page.locator("#title").evaluate(el => getComputedStyle(el).backgroundColor);
      assert.notEqual(fill, "rgb(255, 255, 255)");
    }
    await page.getByRole("button", { name: "燕麦", exact: true }).focus();
    await page.keyboard.press("Enter");
    await current("sand");
    assert.equal(await page.getByRole("button", { name: "燕麦", exact: true }).evaluate(el => el === document.activeElement), true);
    await page.reload();
    await current("sand");
    await page.getByLabel("标题", { exact: true }).fill("测试标题");
    await page.getByLabel("备注", { exact: true }).fill("\n保留换行");
    await Promise.all([page.waitForURL("**/preview?**"), page.getByRole("button", { name: "更新预览" }).click()]);
    await current("sand");
    assert.equal(await page.getByLabel("备注", { exact: true }).inputValue(), "\n保留换行");
    await page.route("**/themes/sage.css", route => route.abort());
    await page.getByRole("button", { name: "鼠尾草", exact: true }).click();
    await page.getByRole("status").filter({ hasText: "配色加载失败" }).waitFor();
    await current("sand");
    assert.equal(await page.getByRole("button", { name: "鼠尾草", exact: true }).isEnabled(), true);
    await page.unroute("**/themes/sage.css");
    await page.getByRole("button", { name: "鼠尾草", exact: true }).click();
    await current("sage");
    await page.setViewportSize({ width: 390, height: 844 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    await page.emulateMedia({ colorScheme: "dark" });
    assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme), "dark");
    assert.deepEqual(errors, []);

    const blocked = await browser.newContext();
    await blocked.addInitScript(() => Object.defineProperty(window, "localStorage", { get() { throw new Error("blocked"); } }));
    const blockedPage = await blocked.newPage();
    await blockedPage.goto(base);
    await blockedPage.getByRole("button", { name: "暮色", exact: true }).click();
    await blockedPage.waitForFunction(() => document.querySelector("link[data-webui-theme-sheet]").dataset.webuiThemeSheet === "dusk");

    const nojs = await browser.newContext({ javaScriptEnabled: false });
    const native = await nojs.newPage();
    await native.goto(base);
    assert.equal(await native.locator("[data-webui-theme-controls]").isVisible(), false);
    await native.getByLabel("标题", { exact: true }).fill("原生表单");
    await Promise.all([native.waitForURL("**/preview?**"), native.getByRole("button", { name: "更新预览" }).click()]);
    assert.equal(await native.locator("article h2").innerText(), "原生表单");
  } finally {
    await browser.close();
  }
});
