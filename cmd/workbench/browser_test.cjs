// End-to-end checks. Uses only the preinstalled Go, Playwright, and Chromium.
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const http = require("node:http");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const { spawn, spawnSync } = require("node:child_process");
const { chromium } = require("playwright");

const repo = path.resolve(__dirname, "../..");
const go = process.env.WORKBENCH_GO || "go";
const chromiumPath = process.env.WORKBENCH_CHROMIUM || "/usr/bin/chromium";
let temp, binary, base, workbench, browser, provider;
let providerCalls = 0;

const listen = server => new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", () => resolve(server.address().port)); });
const close = server => new Promise(resolve => server?.close(resolve));
const freePort = async () => { const s = net.createServer(); const port = await listen(s); await close(s); return port; };
async function waitForServer(url) {
  let last;
  for (let i = 0; i < 100; i++) {
    try { const response = await fetch(url); if (response.ok) return; last = new Error(`HTTP ${response.status}`); } catch (error) { last = error; }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw last || new Error("workbench did not start");
}

before(async () => {
  temp = await fs.mkdtemp(path.join(os.tmpdir(), "workbench-browser-test-"));
  binary = path.join(temp, "workbench");
  const build = spawnSync(go, ["build", "-o", binary, "./cmd/workbench"], { cwd: repo, encoding: "utf8", env: { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off" } });
  assert.equal(build.status, 0, build.stderr || build.stdout);
  provider = http.createServer(async (request, response) => {
    const chunks = []; for await (const chunk of request) chunks.push(chunk);
    if (request.method === "GET" && request.url === "/v1/models") {
      response.setHeader("Content-Type", "application/json");
      response.end('{"object":"list","data":[{"id":"fake-chat-model","object":"model","created":1,"owned_by":"local-test"}]}'); return;
    }
    if (request.method === "POST" && request.url === "/v1/responses") {
      providerCalls++;
      assert.equal(JSON.parse(Buffer.concat(chunks).toString("utf8")).model, "fake-chat-model");
      response.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
      response.write('event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"来自假服务的 "}\n\n');
      response.write('event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"<b>安全文本</b>"}\n\n');
      response.end('event: response.completed\ndata: {"type":"response.completed","response":{"id":"resp_fake","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"来自假服务的 <b>安全文本</b>"}]}]}}\n\n'); return;
    }
    if (request.method === "POST" && request.url === "/v1/files") {
      assert.match(request.headers["content-type"], /multipart\/form-data/);
      assert.match(Buffer.concat(chunks).toString("utf8"), /browser-upload-content/);
      response.writeHead(200, {"Content-Type":"application/json"}); response.end('{"id":"file_browser","filename":"sample.txt","purpose":"assistants"}'); return;
    }
    if (request.method === "GET" && request.url === "/v1/files/file_browser/content") {
      response.writeHead(200, {"Content-Type":"application/octet-stream"}); response.end("browser-download-content"); return;
    }
    if (request.method === "POST" && request.url === "/v1/containers") {
      const body = JSON.parse(Buffer.concat(chunks)); assert.equal(body.memory_limit, "4g");
      response.writeHead(200, {"Content-Type":"application/json"}); response.end('{"id":"cntr_browser","name":"browser-container"}'); return;
    }
    if (request.method === "POST" && request.url === "/v1/batches") {
      const body = JSON.parse(Buffer.concat(chunks)); assert.equal(body.input_file_id, "file_browser");
      response.writeHead(200, {"Content-Type":"application/json"}); response.end('{"id":"batch_browser","status":"validating"}'); return;
    }
    response.writeHead(404, { "Content-Type": "application/json" }); response.end('{"error":{"message":"fake endpoint missing"}}');
  });
  provider.baseURL = `http://127.0.0.1:${await listen(provider)}/v1`;
  const port = await freePort(); base = `http://127.0.0.1:${port}`;
  workbench = spawn(binary, ["-addr", `127.0.0.1:${port}`, "-data-dir", path.join(temp, "data")], { cwd: repo, stdio: ["ignore", "pipe", "pipe"] });
  let stderr = ""; workbench.stderr.on("data", chunk => { stderr += chunk; }); workbench.once("exit", code => { if (code && stderr) process.stderr.write(stderr); });
  await waitForServer(base + "/settings");
  if (process.env.WORKBENCH_BROWSER_LIBS) process.env.LD_LIBRARY_PATH = process.env.WORKBENCH_BROWSER_LIBS + (process.env.LD_LIBRARY_PATH ? ":" + process.env.LD_LIBRARY_PATH : "");
  browser = await chromium.launch({ headless: true, executablePath: chromiumPath, chromiumSandbox: true });
});

after(async () => {
  if (browser) await browser.close();
  if (workbench && workbench.exitCode === null && workbench.signalCode === null) {
    workbench.kill("SIGTERM");
    await Promise.race([new Promise(resolve => workbench.once("exit", resolve)), new Promise(resolve => setTimeout(resolve, 3000))]);
    if (workbench.exitCode === null && workbench.signalCode === null) {
      workbench.kill("SIGKILL");
      await Promise.race([new Promise(resolve => workbench.once("exit", resolve)), new Promise((_, reject) => setTimeout(() => reject(new Error("workbench process did not exit after SIGKILL")), 3000))]);
    }
  }
  await close(provider); if (temp) await fs.rm(temp, { recursive: true, force: true });
});

test("provider discovery and logical mapping are configured through the real UI", async () => {
  const page = await browser.newPage();
  try {
    await page.goto(base + "/settings");
    await page.getByRole("button", { name: "添加服务商" }).click();
    let card = page.locator("[data-provider]").last();
    await card.getByLabel("服务商名称").fill("本地假服务");
    await card.getByLabel("API 前缀").fill(provider.baseURL);
    await card.getByLabel("API key").fill("test-secret-never-rendered");
    await page.getByRole("button", { name: "保存全部设置" }).click(); await page.getByText("设置已保存。").waitFor();
    card = page.locator("[data-provider]").last();
    assert.equal(await card.getByLabel("API key").inputValue(), "", "GET must never repopulate a saved key");
    await card.getByRole("button", { name: "保存并发现模型" }).click(); await page.getByText("fake-chat-model", { exact: true }).waitFor();
    await page.getByRole("button", { name: "添加逻辑模型" }).click();
    const model = page.locator("[data-model]").last();
    await model.getByLabel("显示名称").fill("本地精选模型"); await model.getByRole("button", { name: "添加线路" }).click();
    const route = model.locator("[data-route-row]");
    await route.getByLabel("服务商").selectOption({ label: "本地假服务" }); await route.getByLabel("实际模型").fill("fake-chat-model");
    await page.getByRole("button", { name: "添加逻辑模型" }).click();
    const chatModel = page.locator("[data-model]").last(); await chatModel.getByLabel("显示名称").fill("本地 Chat 模型"); await chatModel.getByRole("button", { name: "添加线路" }).click();
    const chatRoute = chatModel.locator("[data-route-row]"); await chatRoute.getByLabel("服务商").selectOption({ label: "本地假服务" }); await chatRoute.getByLabel("实际模型").fill("fake-chat-model"); await chatRoute.getByLabel("协议").selectOption("chat");
    await page.getByRole("button", { name: "添加逻辑模型" }).click();
    const alternate = page.locator("[data-model]").last(); await alternate.getByLabel("显示名称").fill("备用精选模型"); await alternate.getByRole("button", { name: "添加线路" }).click();
    const alternateRoute = alternate.locator("[data-route-row]"); await alternateRoute.getByLabel("服务商").selectOption({ label: "本地假服务" }); await alternateRoute.getByLabel("实际模型").fill("fake-chat-model");
    await page.getByRole("button", { name: "保存全部设置" }).click(); await page.getByText("设置已保存。").waitFor();
  } finally { await page.close(); }
});

test("configuration draft survives a rejected save", async () => {
  const page = await browser.newPage();
  try {
    await page.goto(base + "/settings"); await page.getByRole("button", { name: "添加服务商" }).click();
    await page.getByLabel("服务商名称").last().fill("草稿服务商");
    await page.route("**/api/config", route => route.request().method() === "PUT" ? route.fulfill({ status: 409, contentType: "application/json", body: '{"error":"配置已被更新"}' }) : route.continue());
    await page.getByRole("button", { name: "保存全部设置" }).click(); await page.getByRole("alert").filter({ hasText: "配置已被更新" }).waitFor();
    assert.equal(await page.getByLabel("服务商名称").last().inputValue(), "草稿服务商");
  } finally { await page.close(); }
});

test("real streaming conversation stays inert, collapses, forks, and produces inspectable logs", async () => {
  const page = await browser.newPage();
  try {
    await page.goto(base + "/"); await page.getByLabel("对话模型").selectOption({ label: "本地 Chat 模型" });
    for (const label of ["联网搜索", "图片生成", "代码执行"]) assert.equal(await page.getByLabel(label).isDisabled(), true, `${label} is Responses-only`);
    await page.getByLabel("对话模型").selectOption({ label: "备用精选模型" });
    await page.getByText("工具版本与原生参数", { exact: true }).click(); await page.getByLabel("系统指令").fill("保持简洁"); await page.getByLabel("原生 JSON options").fill('{"temperature":0}');
    await page.getByLabel("消息").fill("你好，假服务");
    await Promise.all([page.getByRole("button", { name: "发送" }).click(), page.getByRole("button", { name: "发送" }).click()]);
    await page.getByText("来自假服务的 <b>安全文本</b>", { exact: true }).waitFor();
    assert.equal(await page.getByLabel("对话模型").inputValue(), await page.getByLabel("对话模型").locator('option', { hasText: "备用精选模型" }).getAttribute("value"));
    assert.equal(await page.getByLabel("系统指令").inputValue(), "保持简洁"); assert.equal(await page.getByLabel("原生 JSON options").inputValue(), '{"temperature":0}');
    assert.equal(await page.locator(".message-text b").count(), 0, "model output must not become HTML");
    assert.equal(providerCalls, 1, "a double click must dispatch one provider request");
    await page.locator("details.message.assistant").last().waitFor();
    assert.equal(await page.locator("details.message").evaluateAll(messages => messages.some(message => [...message.childNodes].some(node => node.nodeType === Node.TEXT_NODE && node.textContent.trim() === "null"))), false, "absent generated images must not render a null text node");
    const messageBox = await page.locator("#messages").boundingBox(); const composerBox = await page.locator(".composer").boundingBox();
    assert.ok(composerBox.y >= messageBox.y + messageBox.height, "composer must not cover message content");
    let assistant = page.locator("details.message.assistant").last(); await assistant.locator(":scope > summary").click(); assert.equal(await assistant.getAttribute("open"), null);
    await page.locator(".session-item.active button").first().click(); assistant = page.locator("details.message.assistant").last(); assert.equal(await assistant.getAttribute("open"), null, "collapse state survives rerender"); await assistant.locator(":scope > summary").click();
    await assistant.getByRole("button", { name: "复制为新会话" }).click(); await page.locator(".session-item.active").filter({ hasText: "分支" }).waitFor();
    await page.getByLabel("消息").fill("失败后保留这份草稿"); await page.getByText("工具版本与原生参数", { exact: true }).click(); await page.getByLabel("原生 JSON options").fill('{"temperature":0.2}');
    await page.route("**/api/sessions/**", route => {
      if (route.request().url().endsWith("/messages")) return route.fulfill({ status: 503, contentType: "application/json", body: '{"error":"测试失败"}' });
      if (route.request().method() === "GET") return route.fulfill({ status: 503, contentType: "application/json", body: '{"error":"会话重载也失败"}' });
      return route.continue();
    });
    await page.getByRole("button", { name: "发送" }).click(); await page.getByRole("alert").filter({ hasText: "会话重载也失败" }).waitFor();
    assert.equal(await page.getByLabel("消息").inputValue(), "失败后保留这份草稿"); assert.equal(await page.getByLabel("原生 JSON options").inputValue(), '{"temperature":0.2}');
    assert.equal(await page.getByRole("button", { name: "新建会话" }).isEnabled(), true); assert.equal(await page.locator(".session-item.active button").first().isEnabled(), true);
    await page.unroute("**/api/sessions/**");
    await page.getByRole("link", { name: "请求日志", exact: true }).click(); await page.getByText("responses.stream", { exact: true }).waitFor();
    await page.locator(".log-row").filter({ hasText: "responses.stream" }).first().getByRole("button", { name: "查看" }).click(); await page.getByRole("heading", { name: "Request body" }).waitFor();
    assert.match(await page.locator("#log-detail").textContent(), /fake-chat-model/);
    assert.ok(await page.getByRole("link", { name: /下载(完整|原始)请求正文/ }).count(), "raw request download is always available");
  } finally { await page.close(); }
});

test("resource catalog is rendered into usable forms at narrow width", async () => {
  const page = await browser.newPage({ viewport: { width: 390, height: 844 } });
  try {
    for (const [name, route] of [["文件", "/files"], ["容器", "/containers"], ["批处理", "/batches"], ["原生操作", "/native"]]) {
      await page.goto(base + route); await page.locator("form[data-operation]").first().waitFor(); await page.waitForFunction(() => [...document.styleSheets].some(sheet => sheet.href?.endsWith("/assets/workbench.css"))); assert.ok(await page.locator("form[data-operation]").count() > 0, `${name} operation forms`); assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${route} overflows`);
    }
    assert.ok(await page.getByLabel("完全使用这份 JSON（忽略上方字段）").count() > 0, "native forms expose full JSON mode");
    for (const upload of await page.locator('input[type="file"]').all()) assert.equal(await upload.evaluate(input => input.required), false, "uploads allow JSON file references");
    await page.screenshot({ path: "/tmp/mygo-workbench-ui-mobile.png", fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 }); await page.goto(base + "/"); await page.getByLabel("对话模型").waitFor(); await page.waitForFunction(() => [...document.styleSheets].some(sheet => sheet.href?.endsWith("/assets/workbench.css"))); await page.screenshot({ path: "/tmp/mygo-workbench-ui-desktop.png", fullPage: true });
  } finally { await page.close(); }
});


test("resource forms upload, download, and submit complete native JSON", async () => {
  const page = await browser.newPage();
  try {
    await page.goto(base + "/files");
    const upload = page.locator('form[data-operation="files.upload"]');
    await upload.getByLabel("服务商").selectOption({label:"本地假服务"});
    await upload.locator('input[type="file"]').setInputFiles({name:"sample.txt",mimeType:"text/plain",buffer:Buffer.from("browser-upload-content")});
    await upload.getByRole("button",{name:"执行操作"}).click();
    await upload.locator(".operation-output").filter({hasText:"file_browser"}).waitFor();
    const download = page.locator('form[data-operation="files.download"]');
    await download.getByLabel("服务商").selectOption({label:"本地假服务"});
    await download.locator('[name="file_id"]').fill("file_browser");
    await download.getByRole("button",{name:"执行并下载"}).click();
    const [file] = await Promise.all([page.waitForEvent("download"),download.getByRole("link",{name:"保存下载"}).click()]);
    assert.equal(await fs.readFile(await file.path(),"utf8"),"browser-download-content");
    for (const [route,operation,params,result] of [
      ["containers","containers.create",{name:"browser-container",memory_limit:"4g"},"cntr_browser"],
      ["batches","batches.create",{input_file_id:"file_browser",endpoint:"/v1/responses",completion_window:"24h"},"batch_browser"],
    ]) {
      await page.goto(base + "/" + route);const form = page.locator(`form[data-operation="${operation}"]`);
      await form.getByLabel("服务商").selectOption({label:"本地假服务"});await form.locator("summary").click();
      await form.getByLabel("完全使用这份 JSON（忽略上方字段）").check();await form.getByLabel("params").fill(JSON.stringify(params));
      await form.getByRole("button",{name:"执行操作"}).click();await form.locator(".operation-output").filter({hasText:result}).waitFor();
    }
  } finally { await page.close(); }
});

test("themes and mobile chat retain readable controls without overlap", async () => {
  const page = await browser.newPage({viewport:{width:1440,height:1000}});
  const screenshots = path.join(repo,"bin","workbench-browser");await fs.mkdir(screenshots,{recursive:true});
  try {
    await page.goto(base);await page.getByLabel("对话模型").waitFor();
    for (const theme of ["rose","sand","sage","dusk"]) {
      await page.evaluate(theme => localStorage.setItem("webui-theme",theme),theme);
      for (const colorScheme of ["light","dark"]) {
        await page.emulateMedia({colorScheme});await page.reload();await page.getByLabel("对话模型").waitFor();await page.evaluate(()=>document.fonts.ready);
        assert.ok(await page.locator(".session-item").evaluateAll(items => items.every(item => [...item.children].every(child => child.getBoundingClientRect().right <= item.getBoundingClientRect().right + 1))), "session controls fit their panel");
        await page.screenshot({path:path.join(screenshots,`${theme}-${colorScheme}.png`),fullPage:true});
      }
    }
    await page.setViewportSize({width:390,height:844});await page.reload();await page.getByLabel("对话模型").waitFor();
    await page.getByLabel("消息",{exact:true}).fill("长内容测试 "+"abcdefghijk".repeat(150));
    const messages = await page.locator("#messages").boundingBox(); const composer = await page.locator(".composer").boundingBox();
    assert.ok(composer.y >= messages.y + messages.height, "mobile composer does not obscure history");
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),"mobile chat fits viewport");
    assert.equal(await page.getByRole("link",{name:"请求日志",exact:true}).isVisible(),true,"mobile navigation has visible accessible labels");
    await page.screenshot({path:path.join(screenshots,"mobile-chat-dark.png"),fullPage:true});
  } finally {await page.close();}
});
