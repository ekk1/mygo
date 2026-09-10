// Run with Debian's nodejs: node --test utils/webui/request_test.cjs
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

let server, base;
before(async () => {
  server = http.createServer(async (req, res) => {
    if (req.url === "/bad") { res.writeHead(422); res.end("invalid input"); return; }
    if (req.url === "/empty") { res.writeHead(204); res.end(); return; }
    if (req.url === "/invalid-json") { res.end("not json"); return; }
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const body = Buffer.concat(chunks);
    let fields = null;
    if ((req.headers["content-type"] || "").startsWith("multipart/form-data")) {
      const request = new Request(base, { method: "POST", headers: req.headers, body });
      fields = Array.from((await request.formData()).entries());
    }
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ method: req.method, url: req.url, headers: req.headers, body: body.toString(), fields }));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  base = "http://127.0.0.1:" + server.address().port;
  global.window = { location: { href: base + "/" } };
  require("./assets/webui.js");
});
after(async () => {
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
});

test("request returns Response; HTTP errors retain the readable body", async () => {
  assert.ok((await window.webui.request(base)) instanceof Response);
  await assert.rejects(window.webui.request(base + "/bad"), asyncError => {
    assert.equal(asyncError.response.status, 422);
    return true;
  });
  try { await window.webui.request(base + "/bad"); }
  catch (error) { assert.equal(await error.response.text(), "invalid input"); }
});

test("json encodes body without changing options and handles empty and invalid replies", async () => {
  const options = { method: "POST", body: { title: "粉色" }, headers: { "X-Test": "yes" } };
  const result = await window.webui.json(base, options);
  assert.equal(result.body, '{"title":"粉色"}');
  assert.equal(result.headers["content-type"], "application/json");
  assert.equal(result.headers.accept, "application/json");
  assert.equal(result.headers["x-test"], "yes");
  assert.deepEqual(options.body, { title: "粉色" });
  assert.equal(await window.webui.json(base + "/empty"), null);
  await assert.rejects(window.webui.json(base + "/invalid-json"), SyntaxError);
});

test("form sends repeated fields with a browser-generated multipart boundary", async () => {
  const data = new FormData(); data.append("tag", "one"); data.append("tag", "粉色");
  const result = await (await window.webui.form(base, data, { headers: { "Content-Type": "wrong" } })).json();
  assert.equal(result.method, "POST");
  assert.deepEqual(result.fields, [["tag", "one"], ["tag", "粉色"]]);
});

test("GET form appends query fields, preserves existing query, and sends no body", async () => {
  const data = new FormData(); data.append("q", "粉色"); data.append("q", "two");
  const result = await (await window.webui.form("/search?old=1", data, { method: "GET", body: "ignored" })).json();
  assert.equal(result.url, "/search?old=1&q=%E7%B2%89%E8%89%B2&q=two");
  assert.equal(result.body, "");
  await assert.rejects(window.webui.form(base, {}), TypeError);
});

test("request passes through native cancellation", async () => {
  const controller = new AbortController(); controller.abort();
  await assert.rejects(window.webui.request(base, { signal: controller.signal }), { name: "AbortError" });
});
