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
let calls=[],files=[];

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
    const chunks=[];for await(const chunk of request)chunks.push(chunk);
    const raw=Buffer.concat(chunks).toString("utf8");let body;try{body=JSON.parse(raw);}catch{}
    calls.push({method:request.method,url:request.url,body,raw,headers:request.headers});
    const json=value=>{response.setHeader("Content-Type","application/json");response.end(JSON.stringify(value));};
    const url=new URL(request.url,"http://test");
    if(request.method==="GET"&&url.pathname.endsWith("/models"))return json({data:[{id:"fake-chat-model"}],models:[{name:"models/fake-chat-model"}]});
    if(request.method==="POST"&&url.pathname.endsWith("/responses")){
      providerCalls++;
      const result={id:"resp-test",status:"completed",output:[{type:"message",role:"assistant",content:[{type:"output_text",text:"来自假服务的 <b>安全文本</b>"}]}]};
      if(body.stream){response.setHeader("Content-Type","text/event-stream");response.write('event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"来自假服务的 "}\n\n');const timer=setTimeout(()=>response.end('event: response.completed\ndata: '+JSON.stringify({type:"response.completed",response:result})+'\n\n'),raw.includes("stream-cancel")?2000:80);response.on("close",()=>clearTimeout(timer));return;}
      return json(result);
    }
    if(request.method==="POST"&&url.pathname.endsWith("/messages"))return json({id:"msg-test",role:"assistant",content:[{type:"text",text:"Claude answer"}]});
    if(request.method==="POST"&&url.pathname.endsWith(":generateContent"))return json({candidates:[{content:{role:"model",parts:[{text:"Gemini answer",thoughtSignature:"native-signature"}]}}]});
    if(request.method==="POST"&&url.pathname.endsWith(":streamGenerateContent")){response.setHeader("Content-Type","text/event-stream");response.end('data: '+JSON.stringify({candidates:[{content:{role:"model",parts:[{text:"Gemini answer",thoughtSignature:"native-signature"}]},finishReason:"STOP"}]})+'\n\n');return;}
    if(request.method==="GET"&&url.pathname==="/v1/files")return json(url.searchParams.has("after")?{data:[],has_more:false}:{data:files,has_more:files.length>0,last_id:files.at(-1)?.id});
    if(request.method==="POST"&&url.pathname==="/v1/files"){assert.match(request.headers["content-type"],/multipart/);assert.match(raw,/browser-upload-content/);const file={id:"file_browser",filename:"sample.txt",bytes:22,purpose:"assistants",created_at:1700000000};files.push(file);return json(file);}
    if(request.method==="GET"&&url.pathname==="/v1/files/file_browser/content"){response.setHeader("Content-Type","application/octet-stream");response.end("browser-download-content");return;}
    if(request.method==="DELETE"&&url.pathname==="/v1/files/file_browser"){files=files.filter(f=>f.id!=="file_browser");return json({id:"file_browser",deleted:true});}
    if(request.method==="GET"&&url.pathname==="/v1/files/file_browser")return json(files[0]);
    if(request.method==="GET"&&url.pathname==="/v1/containers")return json({data:[{id:"cntr-browser",name:"分析容器",status:"running",created_at:1700000000}],has_more:false});
    if(request.method==="GET"&&url.pathname==="/v1/containers/cntr-browser/files")return json({data:[{id:"cfile",path:"/mnt/data/report.csv",bytes:10}],has_more:false});
    if(request.method==="GET"&&url.pathname==="/v1/batches")return json({data:[{id:"batch-browser",status:"in_progress",input_file_id:"file-in",request_counts:{total:10,completed:3,failed:0}}],has_more:false});
    if(request.method==="POST"&&url.pathname==="/v1/batches/batch-browser/cancel")return json({id:"batch-browser",status:"cancelling"});
    if(request.method==="POST"&&url.pathname==="/v1/images/generations")return json({data:[{b64_json:"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aV1sAAAAASUVORK5CYII="}]});
    if(request.method==="POST"&&url.pathname==="/v1/audio/speech"){response.setHeader("Content-Type","audio/mpeg");response.end("test-audio");return;}
    response.writeHead(404,{"Content-Type":"application/json"});response.end(JSON.stringify({error:"fake endpoint missing: "+request.method+" "+request.url}));
  });
  provider.baseURL = `http://127.0.0.1:${await listen(provider)}/v1`;
  const port = await freePort(); base = `http://127.0.0.1:${port}`;
  workbench = spawn(binary, ["-addr", `127.0.0.1:${port}`, "-data-dir", path.join(temp, "data")], { cwd: repo, stdio: ["ignore", "pipe", "pipe"] });
  let stderr = ""; workbench.stderr.on("data", chunk => { stderr += chunk; }); workbench.once("exit", code => { if (code && stderr) process.stderr.write(stderr); });
  await waitForServer(base + "/settings");
  if (process.env.WORKBENCH_BROWSER_LIBS) process.env.LD_LIBRARY_PATH = process.env.WORKBENCH_BROWSER_LIBS + (process.env.LD_LIBRARY_PATH ? ":" + process.env.LD_LIBRARY_PATH : "");
  browser = await chromium.launch({ headless: true, executablePath: chromiumPath, chromiumSandbox: true });
  const cfg=await (await fetch(base+"/api/config")).json();
  cfg.providers=[{id:"alpha",name:"主账号",kind:"openai",base_url:provider.baseURL,proxy_url:"-",api_key:"alpha-secret",models:["fake-chat-model"]},{id:"beta",name:"备用账号",kind:"openai",base_url:provider.baseURL,proxy_url:"-",api_key:"beta-secret",models:["fake-chat-model"]},...[["anthropic","Claude"],["gemini","Gemini"],["xai","Grok"],["compatible","中转站"]].map(([kind,name])=>({id:kind,name,kind,base_url:kind==="gemini"?provider.baseURL.replace(/\/v1$/,""):provider.baseURL,proxy_url:"-",api_key:kind+"-secret",models:["fake-chat-model"]}))];
  const saved=await fetch(base+"/api/config",{method:"PUT",headers:{"Content-Type":"application/json","X-Workbench-Request":"1"},body:JSON.stringify(cfg)});assert.equal(saved.status,200,await saved.text());
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


test("workspace navigation and profile settings are independent", async()=>{
 const page=await browser.newPage();try{
  await page.goto(base);await page.getByRole("heading",{name:"个人工作台",exact:true}).waitFor();await page.locator(".module-entry").click();
  await page.locator(".vendor-entry").last().waitFor();assert.equal(await page.locator(".vendor-entry").count(),5);
  await page.goto(base+"/ai/openai/profiles");await page.getByRole("button",{name:"新建 profile",exact:true}).click();
  const form=page.locator("#profile-editor");await form.getByLabel("Profile 名称").fill("第三个账号");await form.getByLabel("Base URL",{exact:true}).fill(provider.baseURL);await form.getByLabel("API key",{exact:true}).fill("third-secret");await form.getByLabel("Proxy URL",{exact:true}).fill("-");await form.getByRole("button",{name:"保存 profile"}).click();await page.getByText("Profile 已保存。").waitFor();
  const row=page.locator(".profile-row").filter({hasText:"第三个账号"});await row.getByRole("button",{name:"编辑",exact:true}).click();assert.equal(await page.getByLabel("API key",{exact:true}).inputValue(),"");
  await page.goto(base+"/ai/openai/alpha/chat");await page.getByLabel("当前 profile").selectOption("beta");await page.waitForURL("**/ai/openai/beta/chat");await page.reload();assert.equal(await page.getByLabel("当前 profile").inputValue(),"beta");
  assert.equal(await page.getByText("添加逻辑模型").count(),0);
 }finally{await page.close();}
});

test("preview is side-effect free, folds long input, and sends exact original body",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base+"/ai/openai/alpha/chat");await page.getByLabel("实际模型",{exact:true}).fill("fake-chat-model");await page.locator(".parameters > summary").click();await page.getByLabel("Service tier",{exact:true}).fill("flex");await page.getByLabel("Temperature",{exact:true}).fill("0");
  const message="长消息"+"abcdefghij".repeat(600);await page.getByLabel("消息",{exact:true}).fill(message);
  const before=calls.length;const beforeSessions=await (await fetch(base+"/api/sessions?profile_id=alpha")).json();
  await page.getByRole("button",{name:"预览请求",exact:true}).click();await page.locator("[data-request-preview]").waitFor();assert.equal(calls.length,before,"preview must not send upstream");assert.match(await page.locator("[data-request-preview]").textContent(),/省略/);
  const afterSessions=await (await fetch(base+"/api/sessions?profile_id=alpha")).json();assert.equal(afterSessions.length,beforeSessions.length,"preview must not create a session");
  await page.getByRole("button",{name:"展开完整内容"}).click();const preview=JSON.parse(await page.locator("[data-request-preview]").textContent());assert.equal(preview.body.input[0].content[0].text,message);assert.equal(preview.body.service_tier,"flex");assert.equal(preview.body.temperature,0);
  await page.getByRole("button",{name:"发送",exact:true}).click();await page.locator("details.message.assistant").last().waitFor();await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);assert.deepEqual(calls.findLast(c=>c.url==="/v1/responses").body,preview.body);assert.equal(await page.locator(".message-text b").count(),0);
  await page.getByLabel("消息",{exact:true}).fill("第二条");await page.getByRole("button",{name:"预览请求",exact:true}).click();await page.locator("[data-request-preview]").waitFor();assert.match(await page.locator("[data-request-preview]").textContent(),/output_text/);
  await page.getByLabel("消息",{exact:true}).fill("修改后");await page.getByText("输入或参数已变化，请重新预览。").waitFor();
  await page.getByLabel("当前 profile").selectOption("beta");await page.waitForURL("**/beta/chat");await page.getByRole("button",{name:"新建会话",exact:true}).waitFor();assert.equal(await page.locator(".session-item").count(),0,"sessions must be isolated");assert.equal(await page.getByLabel("消息",{exact:true}).inputValue(),"");
 }finally{await page.close();}
});

test("provider pages use their own native protocol and retain service tier",async()=>{
 for(const [vendor,result]of [["anthropic","Claude answer"],["gemini","Gemini answer"],["xai","安全文本"]]){
  const page=await browser.newPage();try{await page.goto(`${base}/ai/${vendor}/${vendor}/chat`);await page.getByLabel("消息",{exact:true}).fill("hello "+vendor);await page.getByRole("button",{name:"发送",exact:true}).click();await page.locator("details.message.assistant").last().waitFor();await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);assert.match(await page.locator("details.message.assistant").last().textContent(),new RegExp(result));}finally{await page.close();}
 }
 const claude=calls.findLast(c=>c.url==="/v1/messages");assert.equal(claude.headers["x-api-key"],"anthropic-secret");assert.equal(claude.headers.authorization,undefined);assert.equal(claude.body.max_tokens,4096);
 const gemini=calls.findLast(c=>/:(streamGenerateContent|generateContent)/.test(c.url));assert.equal(gemini.headers["x-goog-api-key"],"gemini-secret");assert.equal(gemini.body.contents[0].parts[0].text,"hello gemini");assert.equal(gemini.body.model,undefined);
});

test("file manager lists, previews upload, uploads, downloads, and deletes rows",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base+"/ai/openai/alpha/files");await page.getByRole("table").waitFor();await page.getByRole("button",{name:"上传文件",exact:true}).click();
  const form=page.locator("[data-resource-editor]");await form.getByLabel("文件",{exact:true}).setInputFiles({name:"sample.txt",mimeType:"text/plain",buffer:Buffer.from("browser-upload-content")});
  const before=calls.length;await form.getByRole("button",{name:"预览请求",exact:true}).click();await form.locator("[data-request-preview]").waitFor();assert.equal(calls.length,before);assert.match(await form.locator("[data-request-preview]").textContent(),/sample.txt/);
  await form.getByRole("button",{name:"提交",exact:true}).click();const row=page.locator('[data-resource-id="file_browser"]');await row.waitFor();assert.match(await row.textContent(),/sample.txt/);
  await page.getByRole("button",{name:"下一页",exact:true}).click();await page.getByText("这里还没有资源。",{exact:true}).waitFor();await page.getByRole("button",{name:"上一页",exact:true}).click();await row.waitFor();
  const [download]=await Promise.all([page.waitForEvent("download"),row.getByRole("button",{name:"下载",exact:true}).click()]);assert.equal(await fs.readFile(await download.path(),"utf8"),"browser-download-content");
  await row.getByRole("button",{name:"删除",exact:true}).click();await page.getByRole("dialog").getByRole("button",{name:"确认",exact:true}).click();await row.waitFor({state:"detached"});
  await page.goto(base+"/ai/openai/alpha/containers");await page.getByRole("link",{name:"打开文件"}).click();await page.locator('[data-resource-id="cfile"]').waitFor();assert.match(await page.getByRole("table").textContent(),/report.csv/);
  await page.goto(base+"/ai/openai/alpha/batches");await page.locator('[data-resource-id="batch-browser"]').waitFor();await page.getByRole("button",{name:"取消任务"}).click();await page.getByRole("dialog").getByRole("button",{name:"确认",exact:true}).click();await page.waitForFunction(()=>!document.querySelector('[role="status"]'));
  assert.ok(calls.some(c=>c.url==="/v1/batches/batch-browser/cancel"));
 }finally{await page.close();}
});

test("multimodal routes show dedicated forms and render generated media",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base+"/ai/openai/alpha/image");await page.getByRole("heading",{name:"图片生成",exact:true}).waitFor();await page.getByLabel("提示词 / 输入",{exact:true}).fill("a dot");await page.getByRole("button",{name:"生成 / 提交",exact:true}).click();await page.locator(".results img").waitFor();assert.ok(await page.locator(".results img").evaluate(img=>img.complete));
  await page.goto(base+"/ai/openai/alpha/speech");await page.getByRole("heading",{name:"语音合成",exact:true}).waitFor();await page.getByLabel("提示词 / 输入",{exact:true}).fill("你好");await page.getByRole("button",{name:"生成 / 提交",exact:true}).click();await page.locator(".results audio").waitFor();
  for(const route of ["image-edit","transcribe","translate"]){await page.goto(base+"/ai/openai/alpha/"+route);await page.locator(".workspace-form").waitFor();assert.equal(await page.locator('input[type="file"]').first().isVisible(),true);}
 }finally{await page.close();}
});

test("desktop and mobile navigation, forms and tables fit the viewport",async()=>{
 const screenshots=path.join(repo,"bin","workbench-browser");await fs.mkdir(screenshots,{recursive:true});const page=await browser.newPage({viewport:{width:1440,height:1000}});
 try{
  for(const [route,name]of [["/ai","providers"],["/ai/openai/alpha/chat","chat"],["/ai/openai/alpha/files","files"],["/ai/openai/alpha/image","image"]]){await page.goto(base+route);await page.waitForFunction(()=>document.querySelector('.page-heading'));await page.screenshot({path:path.join(screenshots,`redesign-${name}.png`),fullPage:true});}
  for(const [id,name]of [["sand","燕麦"],["sage","鼠尾草"],["dusk","暮色"],["rose","玫瑰灰"]]){await page.goto(base+"/settings");await page.getByLabel("主题",{exact:true}).selectOption(id);await page.waitForFunction(id=>document.querySelector("[data-webui-theme-sheet]").dataset.webuiThemeSheet===id,id);await page.goto(base+"/ai/openai/alpha/image");await page.waitForFunction(id=>document.querySelector("[data-webui-theme-sheet]").dataset.webuiThemeSheet===id,id);await page.screenshot({path:path.join(screenshots,`redesign-theme-${id}.png`),fullPage:true});}
  await page.setViewportSize({width:390,height:844});
  for(const route of ["/ai","/ai/openai/alpha/chat","/ai/openai/alpha/files","/ai/openai/alpha/image"]){await page.goto(base+route);await page.waitForFunction(()=>document.querySelector('.page-heading'));assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${route} overflows`);}
  await page.getByRole("button",{name:"☰ 导航"}).click();await page.locator("#sidebar.is-open").waitFor();assert.equal(await page.getByRole("link",{name:"请求日志",exact:true}).isVisible(),true);await page.screenshot({path:path.join(screenshots,"redesign-mobile-nav.png"),fullPage:true});
  await page.goto(base+"/ai/openai/alpha/chat");await page.getByLabel("消息",{exact:true}).waitFor();const messages=await page.locator("#messages").boundingBox(),composer=await page.locator(".composer").boundingBox();assert.ok(composer.y>=messages.y+messages.height,"composer overlaps messages");await page.screenshot({path:path.join(screenshots,"redesign-mobile-chat.png"),fullPage:true});
 }finally{await page.close();}
});

test("live text arrives before completion and cancellation retains the partial answer",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base+"/ai/openai/beta/chat");await page.getByLabel("消息",{exact:true}).fill("stream-cancel");await page.getByRole("button",{name:"发送",exact:true}).click();await page.locator("[data-live-text]").waitFor();assert.match(await page.locator("[data-live-text]").textContent(),/来自假服务/);
  await page.getByRole("button",{name:"停止",exact:true}).click();await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);assert.match(await page.locator("details.message.assistant").last().textContent(),/cancelled/);assert.match(await page.locator("details.message.assistant .message-text").first().textContent(),/来自假服务/);
 }finally{await page.close();}
});
