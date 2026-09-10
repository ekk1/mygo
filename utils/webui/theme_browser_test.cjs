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

test("candlestick bodies have no wick stroke through their interior", async () => {
  const browser = await chromium.launch({ headless: true, executablePath: "/usr/bin/chromium", chromiumSandbox: true });
  try {
    const page = await browser.newPage();
    await page.goto(base + "/components?bars=20");
    for (const selector of [".chart-up", ".chart-down"]) {
      const bodies = await page.locator(selector).evaluate(path => {
        const result = [];
        for (const match of path.getAttribute("d").matchAll(/M([\d.]+) ([\d.]+)H([\d.]+)V([\d.]+)H[\d.]+Z/g)) {
          const [, left, top, right, bottom] = match.map(Number);
          if (bottom - top < 3) continue;
          const center = new DOMPoint((left + right) / 2, (top + bottom) / 2);
          result.push({ filled: path.isPointInFill(center), stroked: path.isPointInStroke(center) });
        }
        return result;
      });
      assert.ok(bodies.length > 0, selector + " must have testable bodies");
      for (const body of bodies) {
        assert.equal(body.filled, true, selector + " body must be filled");
        assert.equal(body.stroked, false, selector + " wick crosses body interior");
      }
    }
  } finally {
    await browser.close();
  }
});

test("arrow keys pan one or five candles without moving focus or reloading", async () => {
  const browser = await chromium.launch({ headless: true, executablePath: "/usr/bin/chromium", chromiumSandbox: true });
  try {
    const page = await browser.newPage();
    await page.goto(base + "/components?bars=20");
    await page.evaluate(() => window.panDocument = true);
    const hit = page.locator(".chart-hit").nth(5);
    await hit.focus();
    const lineBefore = await page.locator(".chart-close").getAttribute("d");
    await page.keyboard.press("ArrowLeft");
    await page.waitForURL(url => url.searchParams.get("start") === "2026-04-10" && url.searchParams.get("end") === "2026-04-29");
    assert.equal(await page.locator(".chart-hit").count(), 20);
    assert.equal(await hit.evaluate(el => document.activeElement === el), true);
    assert.notEqual(await page.locator(".chart-close").getAttribute("d"), lineBefore);
    await page.keyboard.press("ArrowLeft");
    await page.waitForURL(url => url.searchParams.get("start") === "2026-04-09" && url.searchParams.get("end") === "2026-04-28");
    await page.keyboard.press("ArrowRight");
    await page.waitForURL(url => url.searchParams.get("start") === "2026-04-10" && url.searchParams.get("end") === "2026-04-29");
    await page.keyboard.press("ArrowRight");
    await page.waitForURL(url => url.searchParams.get("start") === "2026-04-11" && url.searchParams.get("end") === "2026-04-30");
    await page.keyboard.press("Shift+ArrowLeft");
    await page.waitForURL(url => url.searchParams.get("start") === "2026-04-06" && url.searchParams.get("end") === "2026-04-25");
    assert.equal(await page.locator(".chart-hit").count(), 20);
    assert.equal(await hit.evaluate(el => document.activeElement === el), true);
    await page.keyboard.press("ArrowRight");
    await page.waitForURL(url => url.searchParams.get("start") === "2026-04-07" && url.searchParams.get("end") === "2026-04-26");
    await page.keyboard.press("Shift+ArrowRight");
    await page.waitForURL(url => url.searchParams.get("start") === "2026-04-11" && url.searchParams.get("end") === "2026-04-30");
    assert.equal(await page.locator(".chart-hit").count(), 20);
    let requests = 0;
    page.on("request", request => { if (request.url().includes("/components/chart")) requests++; });
    await page.keyboard.press("Shift+ArrowRight");
    await page.keyboard.press("ArrowRight");
    await page.getByLabel("开始日期", { exact: true }).focus();
    await page.keyboard.press("ArrowLeft");
    await page.keyboard.press("Shift+ArrowLeft");
    await page.waitForTimeout(150);
    assert.equal(requests, 0, "boundary and date editing must not request a pan");
    assert.equal(await page.evaluate(() => window.panDocument), true);
  } finally { await browser.close(); }
});

test("chart date inputs and window movement work with and without JavaScript", async () => {
  const browser = await chromium.launch({ headless: true, executablePath: "/usr/bin/chromium", chromiumSandbox: true });
  try {
    for (const javaScriptEnabled of [true, false]) {
      const context = await browser.newContext({ javaScriptEnabled });
      const page = await context.newPage();
      await page.goto(base + "/components");
      let documentID;
      if (javaScriptEnabled) documentID = await page.evaluate(() => window.chartDocumentID = Math.random());
      const start = page.getByLabel("开始日期", { exact: true });
      const end = page.getByLabel("结束日期", { exact: true });
      const previous = page.getByRole("button", { name: "← 前一段", exact: true });
      const next = page.getByRole("button", { name: "后一段 →", exact: true });
      const apply = page.getByRole("button", { name: "应用日期", exact: true });
      const navigate = async (button, first, last) => {
        await Promise.all([page.waitForURL(url => url.searchParams.get("start") === first && url.searchParams.get("end") === last && !url.searchParams.has("move")), button.click()]);
        assert.equal(await start.inputValue(), first);
        assert.equal(await end.inputValue(), last);
        if (javaScriptEnabled) assert.equal(await page.evaluate(() => window.chartDocumentID), documentID, "chart interaction reloaded the document");
      };
      await start.fill("2026-02-01"); await end.fill("2026-02-10");
      await navigate(apply, "2026-02-01", "2026-02-10");
      assert.equal(await page.locator(".chart-hit").count(), 10);
      await navigate(previous, "2026-01-22", "2026-01-31");
      assert.equal(await page.locator(".chart-hit").count(), 10);
      await page.reload();
      if (javaScriptEnabled) documentID = await page.evaluate(() => window.chartDocumentID = Math.random());
      assert.equal(await start.inputValue(), "2026-01-22");
      await navigate(next, "2026-02-01", "2026-02-10");
      await start.fill("2026-01-01"); await end.fill("2026-01-10");
      await navigate(apply, "2026-01-01", "2026-01-10");
      assert.equal(await previous.isDisabled(), true);
      await Promise.all([page.waitForURL("**/components?bars=20"), page.getByRole("button", { name: "最近 20 根", exact: true }).click()]);
      assert.equal(await start.inputValue(), "2026-04-11");
      assert.equal(await end.inputValue(), "2026-04-30");
      assert.equal(await next.isDisabled(), true);
      if (javaScriptEnabled) {
        const svgBefore = await page.locator("svg").innerHTML();
        const urlBefore = page.url();
        await start.fill("2026-03-10"); await end.fill("2026-03-01");
        await apply.click();
        await page.locator("#chart-update-status").filter({ hasText: "开始日期不能晚于" }).waitFor();
        assert.equal(await page.locator("svg").innerHTML(), svgBefore);
        assert.equal(page.url(), urlBefore);
        assert.equal(await start.inputValue(), "2026-03-10");
        await page.route("**/components/chart?**", route => route.fulfill({ status: 503, body: "Unavailable" }));
        await page.getByRole("button", { name: "最近 60 根", exact: true }).click();
        await page.locator("#chart-update-status").filter({ hasText: "更新失败" }).waitFor();
        assert.equal(await page.locator("svg").innerHTML(), svgBefore);
        await page.unroute("**/components/chart?**");
        let release;
        const delayed = new Promise(resolve => { release = resolve; });
        await page.route("**/components/chart?bars=60", async route => {
          const response = await route.fetch();
          await delayed;
          await route.fulfill({ response });
        });
        const requested = page.waitForRequest("**/components/chart?bars=60");
        await page.getByRole("button", { name: "最近 60 根", exact: true }).click();
        await requested;
        assert.equal(await page.locator("#chart-update-status").isVisible(), false, "loading must not show layout-shifting status");
        await page.getByRole("button", { name: "最近 120 根", exact: true }).click();
        await page.waitForURL("**/components?bars=120");
        release();
        await page.unroute("**/components/chart?bars=60");
        assert.equal(await page.locator(".chart-hit").count(), 120);
        await page.goBack();
        await page.waitForFunction(() => document.querySelectorAll(".chart-hit").length === 20 && !document.querySelector("#chart-panel").hasAttribute("aria-busy"));
        await page.goForward();
        await page.waitForFunction(() => document.querySelectorAll(".chart-hit").length === 120 && !document.querySelector("#chart-panel").hasAttribute("aria-busy"));
        await page.route("**/components/chart?**", route => route.fulfill({ status: 503, body: "Unavailable" }));
        await page.goBack();
        await page.locator("#chart-update-status").filter({ hasText: "更新失败" }).waitFor();
        assert.equal(new URL(page.url()).searchParams.get("bars"), "120", "failed history request must restore the displayed chart URL");
        assert.equal(await page.locator(".chart-hit").count(), 120);
        await page.unroute("**/components/chart?**");

        await page.getByRole("button", { name: "最近 20 根", exact: true }).click();
        await page.waitForURL("**/components?bars=20");
        await page.getByRole("button", { name: "最近 120 根", exact: true }).click();
        await page.waitForURL("**/components?bars=120");
        let releaseBack, finishBack;
        const delayedBack = new Promise(resolve => { releaseBack = resolve; });
        const finishedBack = new Promise(resolve => { finishBack = resolve; });
        await page.route("**/components/chart?bars=20", async route => {
          try {
            const response = await route.fetch();
            await delayedBack;
            await route.fulfill({ response });
          } finally { finishBack(); }
        });
        const backRequested = page.waitForRequest("**/components/chart?bars=20");
        await page.goBack();
        await backRequested;
        await start.fill("2026-03-01");
        assert.equal(new URL(page.url()).searchParams.get("bars"), "120", "editing during history request must restore the displayed chart URL");
        releaseBack();
        await finishedBack;
        await page.unroute("**/components/chart?bars=20");
        assert.equal(await page.locator(".chart-hit").count(), 120);
        assert.equal(await start.inputValue(), "2026-03-01");
        assert.equal(await page.locator("#chart-panel").getAttribute("aria-busy"), null);
        assert.equal(await page.evaluate(() => window.chartDocumentID), documentID);
      }
      await page.setViewportSize({ width: 390, height: 844 });
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await context.close();
    }
  } finally {
    await browser.close();
  }
});

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

test("manual theme changes interpolate colors and respect reduced motion", async () => {
  const browser = await chromium.launch({ headless: true, executablePath: "/usr/bin/chromium", chromiumSandbox: true });
  try {
    const page = await browser.newPage({ reducedMotion: "no-preference" });
    await page.goto(base + "/components");
    const before = await page.evaluate(() => {
      window.themeFrames = [];
      document.body.addEventListener("transitionrun", event => {
        if (event.target === document.body && event.propertyName === "background-color") {
          window.themeFrames.push({ color: getComputedStyle(document.body).backgroundColor,
            duration: document.body.getAnimations().find(animation => animation.transitionProperty === "background-color")?.effect.getTiming().duration });
        }
      });
      return getComputedStyle(document.body).backgroundColor;
    });
    await page.getByRole("button", { name: "暮色", exact: true }).click();
    await page.waitForFunction(() => window.themeFrames.length > 0);
    await page.waitForFunction(() => !document.documentElement.classList.contains("webui-theme-transition"));
    const result = await page.evaluate(() => ({ frames: window.themeFrames, after: getComputedStyle(document.body).backgroundColor }));
    assert.notEqual(before, result.after);
    assert.ok(result.frames.some(frame => frame.color !== result.after && frame.duration === 200));
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.evaluate(() => { window.themeFrames = []; });
    await page.getByRole("button", { name: "燕麦", exact: true }).click();
    await page.waitForFunction(() => document.querySelector("[data-webui-theme-sheet]").dataset.webuiThemeSheet === "sand");
    assert.equal(await page.evaluate(() => getComputedStyle(document.body).transitionDuration), "0s");
    assert.deepEqual(await page.evaluate(() => window.themeFrames), []);
    await page.reload();
    await page.waitForFunction(() => document.querySelector("[data-webui-theme-sheet]").dataset.webuiThemeSheet === "sand");
    assert.equal(await page.evaluate(() => document.documentElement.classList.contains("webui-theme-transition")), false);
  } finally { await browser.close(); }
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

test("manual video position callbacks and server-rendered chart ranges", async () => {
  const browser = await chromium.launch({ headless: true, executablePath: "/usr/bin/chromium", chromiumSandbox: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", e => errors.push(e.message));
    let requests = 0;
    page.on("request", r => { if (r.url().includes("/components/position")) requests++; });
    await page.goto(base + "/components");
    assert.equal(requests, 0, "loading must not save or restore");
    const controls = page.locator("[data-webui-video]");
    const status = controls.getByRole("status");
    const save = controls.getByRole("button", { name: "保存位置" });
    const restore = controls.getByRole("button", { name: "恢复位置" });
    await save.click();
    await status.filter({ hasText: "请先加载视频" }).waitFor();
    assert.equal(requests, 0);
    await restore.click();
    await status.filter({ hasText: "尚未保存位置" }).waitFor();

    // A local PCM WAV exercises the real HTMLVideoElement clock and seeking.
    // No external media, executable, codec package, or network download is needed.
    const wav = Buffer.alloc(44 + 8000 * 2 * 30);
    wav.write("RIFF", 0); wav.writeUInt32LE(wav.length - 8, 4); wav.write("WAVEfmt ", 8);
    wav.writeUInt32LE(16, 16); wav.writeUInt16LE(1, 20); wav.writeUInt16LE(1, 22);
    wav.writeUInt32LE(8000, 24); wav.writeUInt32LE(16000, 28); wav.writeUInt16LE(2, 32); wav.writeUInt16LE(16, 34);
    wav.write("data", 36); wav.writeUInt32LE(wav.length - 44, 40);
    const load = async () => {
      await page.locator("#video-file").setInputFiles({ name: "test.wav", mimeType: "audio/wav", buffer: wav });
      await page.waitForFunction(() => document.querySelector("video").readyState > 0);
    };
    const seek = async seconds => {
      await page.locator("video").evaluate((v, seconds) => { v.currentTime = seconds; }, seconds);
      await page.waitForFunction(() => !document.querySelector("video").seeking);
    };
    await load();
    await seek(12.5);
    const before = requests;
    await save.focus(); await page.keyboard.press("Enter");
    await status.filter({ hasText: "位置已保存" }).waitFor();
    assert.equal(requests, before + 1);
    assert.equal(await save.evaluate(el => el === document.activeElement), true);
    await page.reload(); await load();
    assert.equal(await page.locator("video").evaluate(v => v.currentTime), 0, "reload must not restore automatically");
    await restore.click();
    await status.filter({ hasText: "位置已恢复" }).waitFor();
    assert.ok(Math.abs(await page.locator("video").evaluate(v => v.currentTime) - 12.5) < .05);
    assert.equal(await page.locator("video").evaluate(v => v.paused), true);

    let release;
    const gate = new Promise(resolve => { release = resolve; });
    await page.route("**/components/position", async route => { await gate; await route.fulfill({ status: 503, body: "Unavailable" }); });
    const count = requests;
    await save.click();
    await page.waitForFunction(() => document.querySelector("[data-webui-video]").getAttribute("aria-busy") === "true");
    await restore.evaluate(el => el.click());
    assert.equal(requests, count + 1, "paired buttons must suppress concurrent callbacks");
    release();
    await status.filter({ hasText: "操作失败" }).waitFor();
    assert.equal(await save.getAttribute("aria-disabled"), null);
    await page.unroute("**/components/position");
    await save.click(); await status.filter({ hasText: "位置已保存" }).waitFor();

    for (const response of [{ seconds: -1 }, { seconds: "3" }, {}, { seconds: null }]) {
      await page.route("**/components/position", route => route.fulfill({ json: response }));
      await restore.click(); await status.filter({ hasText: "播放位置无效" }).waitFor();
      assert.ok(Math.abs(await page.locator("video").evaluate(v => v.currentTime) - 12.5) < .05);
      await page.unroute("**/components/position");
    }
    await page.route("**/components/position", route => route.fulfill({ json: { seconds: 999 } }));
    await restore.click(); await status.filter({ hasText: "位置已恢复" }).waitFor();
    assert.equal(await page.locator("video").evaluate(v => v.currentTime), 30);
    await page.unroute("**/components/position");

    // Restore may arrive before metadata: wait for the actual media to load.
    for (const rangeSupport of [false, true]) {
      let releaseMedia;
      const mediaGate = new Promise(resolve => { releaseMedia = resolve; });
      await page.route("**/video-fixture.wav", async route => {
        await mediaGate;
        const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers().range || "");
        if (rangeSupport && range) {
          const start = Number(range[1]), end = range[2] ? Number(range[2]) : wav.length - 1;
          await route.fulfill({ status: 206, contentType: "audio/wav", headers: { "Accept-Ranges": "bytes", "Content-Range": `bytes ${start}-${end}/${wav.length}` }, body: wav.subarray(start, end + 1) });
        } else {
          await route.fulfill({ contentType: "audio/wav", body: wav });
        }
      });
      const mediaRequest = page.waitForRequest("**/video-fixture.wav");
      await page.locator("video").evaluate(v => { v.src = "/video-fixture.wav"; v.load(); });
      await mediaRequest;
      const positionResponse = page.waitForResponse("**/components/position");
      await restore.click(); await positionResponse;
      releaseMedia();
      await status.filter({ hasText: rangeSupport ? "位置已恢复" : "无法定位" }).waitFor();
      if (rangeSupport) assert.ok(Math.abs(await page.locator("video").evaluate(v => v.currentTime) - 12.5) < .05);
      await page.unroute("**/video-fixture.wav");
    }

    // A late response must not be applied after selecting a different source.
    let releasePosition;
    const positionGate = new Promise(resolve => { releasePosition = resolve; });
    await page.route("**/components/position", async route => {
      await positionGate; await route.fulfill({ json: { seconds: 10 } });
    });
    const lateRequest = page.waitForRequest("**/components/position");
    await restore.click(); await lateRequest;
    await page.locator("video").evaluate(v => { v.removeAttribute("src"); v.load(); });
    releasePosition();
    await status.filter({ hasText: "视频已切换" }).waitFor({ timeout: 3000 });
    await page.unroute("**/components/position");

    const longPath = await page.locator(".chart-up").getAttribute("d");
    await load(); await seek(8);
    await page.locator("video").evaluate(async v => { window.originalChartVideo = v; v.muted = true; await v.play(); });
    await page.getByRole("button", { name: "最近 20 根" }).scrollIntoViewIfNeeded();
    const scrollBefore = await page.evaluate(() => scrollY);
    await Promise.all([page.waitForURL("**/components?bars=20"), page.getByRole("button", { name: "最近 20 根" }).click()]);
    assert.equal(await page.locator("video").evaluate(v => v === window.originalChartVideo && !v.paused && v.currentTime >= 8), true, "chart update interrupted video");
    assert.equal(await page.locator("#video-file").evaluate(el => el.files.length), 1);
    assert.ok(Math.abs(await page.evaluate(() => scrollY) - scrollBefore) < 2, "chart update moved scroll position");
    assert.ok((await page.locator(".chart-up").getAttribute("d")).length < longPath.length);
    assert.equal(await page.locator(".chart-line").count(), 1);
    assert.equal(await page.locator(".chart-volume").count(), 20);
    const tooltip = page.getByRole("tooltip");
    const lastCandle = page.locator(".chart-hit").last();
    assert.equal(await tooltip.isVisible(), false);
    await lastCandle.hover({ position: { x: 2, y: 2 } });
    await tooltip.waitFor();
    assert.match(await tooltip.innerText(), /^04-30\nO: [\d.]+\nH: [\d.]+\nL: [\d.]+\nC: [\d.]+\nV: 2903$/);
    await page.getByRole("heading", { name: "视频与 K 线", exact: true }).hover();
    assert.equal(await tooltip.isVisible(), false);
    await page.locator(".chart-hit").first().focus();
    assert.match(await tooltip.innerText(), /^04-11\n[\s\S]*V: 2700$/);
    await page.keyboard.press("Escape");
    assert.equal(await tooltip.isVisible(), false);
    await fs.mkdir("bin", { recursive: true });
    for (const [id, name] of [["rose", "玫瑰灰"], ["sand", "燕麦"], ["sage", "鼠尾草"], ["dusk", "暮色"]]) {
      await page.getByRole("button", { name, exact: true }).click();
      await page.waitForFunction(id => document.querySelector("[data-webui-theme-sheet]").dataset.webuiThemeSheet === id, id);
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 1000 });
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
        assert.equal(await page.locator(".chart-up").evaluate(el => getComputedStyle(el).fill), "rgb(255, 255, 255)");
        assert.equal(await page.locator(".chart-down").evaluate(el => getComputedStyle(el).fill), "rgb(0, 0, 0)");
        await lastCandle.hover();
        await tooltip.waitFor();
        const placement = await tooltip.evaluate(el => {
          const style = getComputedStyle(el);
          const tip = el.getBoundingClientRect(), chart = el.closest(".chart").getBoundingClientRect();
          return { position: style.position, left: tip.left - chart.left, top: tip.top - chart.top, background: style.backgroundColor, shadow: style.boxShadow };
        });
        assert.equal(placement.position, "absolute");
        assert.ok(placement.left >= 0 && placement.left <= 10 && placement.top >= 0 && placement.top <= 10);
        assert.match(placement.background, /(?:0\.65|65%)/);
        assert.equal(placement.shadow, "none");
        await page.screenshot({ path: `bin/webui-components-${id}-${width}.png`, fullPage: true });
      }
    }
    await page.emulateMedia({ colorScheme: "dark" });
    await page.getByRole("button", { name: "玫瑰灰", exact: true }).click();
    await page.waitForFunction(() => document.querySelector("[data-webui-theme-sheet]").dataset.webuiThemeSheet === "rose");
    assert.equal(await page.locator(".chart-up").evaluate(el => getComputedStyle(el).fill), "rgb(255, 255, 255)");
    assert.equal(await page.locator(".chart-down").evaluate(el => getComputedStyle(el).fill), "rgb(0, 0, 0)");
    await page.screenshot({ path: "bin/webui-components-rose-dark.png", fullPage: true });
    assert.deepEqual(errors, []);

    const nojs = await browser.newContext({ javaScriptEnabled: false });
    const native = await nojs.newPage();
    await native.goto(base + "/components");
    assert.equal(await native.locator("svg").isVisible(), true);
    assert.match(await native.locator(".chart-hit title").first().textContent(), /O: [\s\S]*\nV: /);

    const touch = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
    const mobile = await touch.newPage();
    await mobile.goto(base + "/components?bars=20");
    await mobile.locator(".chart-hit").last().tap();
    await mobile.getByRole("tooltip").waitFor();
    assert.match(await mobile.getByRole("tooltip").innerText(), /V: 2903$/);
    assert.equal(await native.locator("[data-webui-video]").isVisible(), false);
    await Promise.all([native.waitForURL("**/components?bars=120"), native.getByRole("button", { name: "最近 120 根" }).click()]);
    assert.equal(await native.locator("svg").isVisible(), true);
  } finally {
    await browser.close();
  }
});
