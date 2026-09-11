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
const browserErrors=[];

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
    if(request.method==="GET"&&url.pathname.endsWith("/models"))return json({data:[{id:"fake-chat-model"},{id:"discovered-model"}],models:[{name:"models/fake-chat-model"},{name:"models/discovered-model"}]});
    if(request.method==="POST"&&url.pathname.endsWith("/responses")){
      providerCalls++;
      const result={id:"resp-test",status:"completed",output:[{type:"message",role:"assistant",content:[{type:"output_text",text:"来自假服务的 <b>安全文本</b>"}]}]};
      if(body.stream&&raw.includes("stream-fail")){response.setHeader("Content-Type","text/event-stream");response.end('event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"已生成的部分回答"}\n\nevent: response.failed\ndata: {"type":"response.failed","response":{"status":"failed","error":{"message":"upstream denied"}}}\n\n');return;}
      if(body.stream&&raw.includes("stream-long")){
        const text=Array.from({length:90},(_,index)=>`第 ${index+1} 行流式文字`).join("\n");
        result.output[0].content[0].text=text+"\n最后一行";
        response.setHeader("Content-Type","text/event-stream");response.write('event: response.output_text.delta\ndata: '+JSON.stringify({type:"response.output_text.delta",delta:text})+'\n\n');
        const timer=setTimeout(()=>response.end('event: response.output_text.delta\ndata: '+JSON.stringify({type:"response.output_text.delta",delta:"\n最后一行"})+'\n\nevent: response.completed\ndata: '+JSON.stringify({type:"response.completed",response:result})+'\n\n'),1000);
        response.on("close",()=>clearTimeout(timer));return;
      }
      if(body.stream){response.setHeader("Content-Type","text/event-stream");response.write('event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"来自假服务的 "}\n\n');const timer=setTimeout(()=>response.end('event: response.completed\ndata: '+JSON.stringify({type:"response.completed",response:result})+'\n\n'),raw.includes("stream-cancel")?2000:80);response.on("close",()=>clearTimeout(timer));return;}
      return json(result);
    }
    if(request.method==="POST"&&url.pathname.endsWith("/messages"))return json({id:"msg-test",role:"assistant",content:[{type:"text",text:"Claude answer"}]});
    if(request.method==="POST"&&url.pathname.endsWith(":generateContent"))return json({candidates:[{content:{role:"model",parts:[{text:"Gemini answer",thoughtSignature:"native-signature"}]}}]});
    if(request.method==="POST"&&url.pathname.endsWith(":streamGenerateContent")){response.setHeader("Content-Type","text/event-stream");response.end('data: '+JSON.stringify({candidates:[{content:{role:"model",parts:[{text:"Gemini answer",thoughtSignature:"native-signature"}]},finishReason:"STOP"}]})+'\n\n');return;}
    if(request.method==="GET"&&url.pathname==="/v1/files/file_detail")return json({id:"file_detail",filename:"detail.txt",bytes:123});
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
  // Access logs must not fill an unread pipe during the full route sweep.
  workbench = spawn(binary, ["-addr", `127.0.0.1:${port}`, "-data-dir", path.join(temp, "data")], { cwd: repo, stdio: ["ignore", "ignore", "pipe"] });
  let stderr = ""; workbench.stderr.on("data", chunk => { stderr += chunk; }); workbench.once("exit", code => { if (code && stderr) process.stderr.write(stderr); });
  await waitForServer(base + "/settings");
  if (process.env.WORKBENCH_BROWSER_LIBS) process.env.LD_LIBRARY_PATH = process.env.WORKBENCH_BROWSER_LIBS + (process.env.LD_LIBRARY_PATH ? ":" + process.env.LD_LIBRARY_PATH : "");
  browser = await chromium.launch({ headless: true, executablePath: chromiumPath, chromiumSandbox: true });
  const originalPage=browser.newPage.bind(browser);browser.newPage=async options=>{const page=await originalPage(options);page.on("pageerror",error=>browserErrors.push(error.message));return page;};
  const cfg=await (await fetch(base+"/api/config")).json();
  cfg.providers=[{id:"alpha",name:"主账号",kind:"openai",base_url:provider.baseURL,proxy_url:"-",api_key:"alpha-secret",models:["fake-chat-model"]},{id:"beta",name:"备用账号",kind:"openai",base_url:provider.baseURL,proxy_url:"-",api_key:"beta-secret",models:["fake-chat-model"]},...[["anthropic","Claude"],["gemini","Gemini"],["xai","Grok"],["compatible","中转站"]].map(([kind,name])=>({id:kind,name,kind,base_url:kind==="gemini"?provider.baseURL.replace(/\/v1$/,""):provider.baseURL,proxy_url:"-",api_key:kind+"-secret",models:["fake-chat-model"],resources:kind==="compatible"?["files","containers","batches","images","audio"]:[]}))];
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
  assert.deepEqual(browserErrors,[],"browser scripts must not throw");
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
  await page.goto(base+"/ai/openai/alpha/image");await page.getByRole("heading",{name:"图片生成",exact:true}).waitFor();await page.getByLabel("提示词 / 输入",{exact:true}).fill("a dot");await page.getByRole("button",{name:"生成图片",exact:true}).click();await page.locator(".results img").waitFor();assert.ok(await page.locator(".results img").evaluate(img=>img.complete));
  await page.goto(base+"/ai/openai/alpha/speech");await page.getByRole("heading",{name:"语音合成",exact:true}).waitFor();await page.getByLabel("要朗读的文本",{exact:true}).fill("你好");await page.getByRole("button",{name:"合成语音",exact:true}).click();await page.locator(".results audio").waitFor();
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
  await page.goto(base+"/ai/openai/beta/chat");await page.getByLabel("消息",{exact:true}).fill("stream-cancel");await page.getByRole("button",{name:"发送",exact:true}).click();await page.waitForFunction(()=>document.querySelector("[data-live-text]")?.textContent.includes("来自假服务"));assert.match(await page.locator("[data-live-text]").textContent(),/来自假服务/);
  await page.getByRole("button",{name:"停止",exact:true}).click();await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);assert.match(await page.locator("details.message.assistant").last().textContent(),/已停止/);assert.match(await page.locator("details.message.assistant .message-text").first().textContent(),/来自假服务/);
 }finally{await page.close();}
});

test("discovery offers searchable models and selection updates the actual request",async()=>{
 const page=await browser.newPage();page.setDefaultTimeout(5000);
 try{
  await page.goto(base+"/ai/openai/alpha/chat");
  await page.getByLabel("实际模型",{exact:true}).fill("a-custom-model");
  await page.getByRole("button",{name:"发现模型",exact:true}).click();
  const dialog=page.getByRole("dialog",{name:"选择模型",exact:true});await dialog.waitFor();
  await dialog.getByLabel("搜索模型",{exact:true}).fill("discovered");
  await dialog.getByRole("button",{name:"discovered-model",exact:true}).waitFor();assert.equal(await page.locator("#model-catalog option").count(),2);
  await page.screenshot({path:path.join(repo,"bin/workbench-browser/model-picker.png"),fullPage:true});
  await dialog.getByRole("button",{name:"discovered-model",exact:true}).click();
  await dialog.waitFor({state:"detached"});
  assert.equal(await page.getByLabel("实际模型",{exact:true}).inputValue(),"discovered-model");
  await page.getByLabel("消息",{exact:true}).fill("picker request");
  await page.getByRole("button",{name:"预览请求",exact:true}).click();
  await page.locator("[data-request-preview]").waitFor();
  assert.match(await page.locator("[data-request-preview]").textContent(),/discovered-model/);
 }finally{await page.close();}
});

test("resource details open a modal and Escape restores the row focus",async()=>{
 files.push({id:"file_detail",filename:"detail.txt",bytes:123});
 const page=await browser.newPage();page.setDefaultTimeout(5000);
 try{
  await page.goto(base+"/ai/openai/alpha/files");
  const trigger=page.locator('[data-resource-id="file_detail"]').getByRole("button",{name:"详情",exact:true});await trigger.click();
  const dialog=page.getByRole("dialog",{name:"资源详情",exact:true});await dialog.waitFor();
  await dialog.locator("pre").waitFor();
  assert.match(await dialog.textContent(),/detail.txt/);
  await page.screenshot({path:path.join(repo,"bin/workbench-browser/resource-detail.png"),fullPage:true});
  assert.equal(await page.evaluate(()=>document.querySelector("dialog").matches(":modal")),true);
  await page.keyboard.press("Escape");await dialog.waitFor({state:"detached"});
  assert.equal(await trigger.evaluate(el=>el===document.activeElement),true);
 }finally{files=files.filter(f=>f.id!=="file_detail");await page.close();}
});

test("profile controls align and parameter rows stay compact",async()=>{
 const page=await browser.newPage({viewport:{width:1440,height:1000}});
 try{
  await page.goto(base+"/ai/openai/alpha/image");await page.getByLabel("尺寸",{exact:true}).waitFor();
  const profile=await page.getByLabel("当前 profile",{exact:true}).boundingBox(),manage=await page.getByRole("link",{name:"管理",exact:true}).boundingBox();
  assert.ok(Math.abs(profile.y+profile.height/2-manage.y-manage.height/2)<2,"profile and management centers differ");
  const model=await page.getByLabel("实际模型",{exact:true}).boundingBox(),discover=await page.getByRole("button",{name:"发现模型",exact:true}).boundingBox();
  assert.ok(Math.abs(model.y+model.height-discover.y-discover.height)<2,"model and discovery bottoms differ");
  const size=await page.getByLabel("尺寸",{exact:true}).boundingBox(),format=await page.getByLabel("质量",{exact:true}).boundingBox();
  assert.ok(format.y-size.y-size.height<48,"parameter rows have excessive blank space");
 }finally{await page.close();}
});

test("OpenAI protocol controls prevent unsupported combinations",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base+"/ai/openai/alpha/chat");await page.locator(".parameters>summary").click();
  await page.getByLabel("联网搜索",{exact:true}).check();await page.getByLabel("接口协议",{exact:true}).selectOption("chat");
  assert.equal(await page.getByLabel("联网搜索",{exact:true}).isDisabled(),true);
  await page.getByLabel("消息",{exact:true}).fill("chat native input");
  await page.getByRole("button",{name:"预览请求",exact:true}).click();
  await page.locator("[data-request-preview]").waitFor();assert.match(await page.locator("[data-request-preview]").textContent(),/chat\/completions/);
 }finally{await page.close();}
});

test("failed confirmation keeps its context and allows one retry",async()=>{
 const page=await browser.newPage();page.setDefaultTimeout(5000);let attempts=0;
 try{
  await page.route("**/api/native/openai/batches.cancel?*",async route=>{attempts++;await route.fulfill({status:attempts===1?500:200,contentType:"application/json",body:attempts===1?'{"error":"任务暂时不能取消"}':'{"id":"batch-browser","status":"cancelling"}'});});
  await page.goto(base+"/ai/openai/alpha/batches");await page.getByRole("button",{name:"取消任务",exact:true}).click();
  const dialog=page.getByRole("dialog");await dialog.getByRole("button",{name:"确认",exact:true}).click();
  await dialog.getByRole("alert").waitFor();assert.match(await dialog.textContent(),/任务暂时不能取消/);
  await dialog.getByRole("button",{name:"确认",exact:true}).click();await dialog.waitFor({state:"detached"});assert.equal(attempts,2);
 }finally{await page.close();}
});

test("JSONL downloads preserve bytes instead of entering the chat stream parser",async()=>{
 const page=await browser.newPage();try{
  const raw='{"custom_id":"one"}\n{"custom_id":"two"}\n';
  await page.route("**/api/native/anthropic/batches.list?*",route=>route.fulfill({json:{data:[{id:"batch-jsonl",processing_status:"ended"}]}}));
  await page.route("**/api/native/anthropic/batches.results?*",route=>route.fulfill({contentType:"application/x-ndjson",headers:{"Content-Disposition":'attachment; filename="results.jsonl"'},body:raw}));
  await page.goto(base+"/ai/anthropic/anthropic/batches");
  const [download]=await Promise.all([page.waitForEvent("download",{timeout:5000}),page.getByRole("button",{name:"下载结果",exact:true}).click()]);
  assert.equal(await fs.readFile(await download.path(),"utf8"),raw);
 }finally{await page.close();}
});

test("conversations restore their selected branch and lock input while forking",async()=>{
 const page=await browser.newPage();let releaseFork;try{
  await page.goto(base+"/ai/openai/beta/chat");
  const send=async text=>{await page.getByLabel("消息",{exact:true}).fill(text);await page.getByRole("button",{name:"发送",exact:true}).click();await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);};
  await send("first-path");await send("sibling-message");
  assert.equal(await page.locator(".session-panel").getByText("null",{exact:true}).count(),0);
  assert.ok(await page.locator("#messages").evaluate(node=>node.scrollHeight-node.scrollTop-node.clientHeight<2),"new replies remain in view");
  await page.locator("details.message.assistant").first().getByRole("button",{name:"从这里继续",exact:true}).click();await send("new-branch");
  assert.equal(await page.locator(".message-text").filter({hasText:"sibling-message"}).count(),0);
  await page.goto(base+"/ai/openai/beta/image");await page.goto(base+"/ai/openai/beta/chat");
  await page.locator("details.message.user").last().waitFor();assert.match(await page.locator("details.message.user").last().textContent(),/new-branch/);
  const first=page.locator("details.message.assistant").first(),parentID=await first.getAttribute("data-message-id");
  await first.getByRole("button",{name:"从这里继续",exact:true}).click();
  assert.equal(await page.getByLabel("查看分支",{exact:true}).inputValue(),parentID);
  assert.equal(await page.getByLabel("查看分支",{exact:true}).locator("option:checked").textContent(),"当前续接点");
  const gate=new Promise(resolve=>{releaseFork=resolve;});let started;
  const requestStarted=new Promise(resolve=>{started=resolve;});
  await page.route("**/api/sessions/*/fork",async route=>{started();await gate;await route.continue();});
  await first.getByRole("button",{name:"复制为新会话",exact:true}).click();await requestStarted;
  assert.equal(await page.getByLabel("消息",{exact:true}).isDisabled(),true);
  assert.equal(await page.getByRole("button",{name:"新建会话",exact:true}).isDisabled(),true);
  assert.equal(await page.getByRole("button",{name:"发送",exact:true}).isDisabled(),true);
  releaseFork();await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);
  assert.equal(await page.locator("details.message").count(),2,"fork keeps only the selected ancestry");
 }finally{releaseFork?.();await page.close();}
});

test("mobile navigation closes with Escape and restores focus",async()=>{
 const page=await browser.newPage({viewport:{width:390,height:844}});try{
  await page.goto(base+"/ai/openai/alpha/chat");const toggle=page.getByRole("button",{name:"☰ 导航",exact:true});await toggle.click();
  await page.locator("#sidebar.is-open").waitFor();await page.keyboard.press("Escape");
  assert.equal(await page.locator("#sidebar").isVisible(),false);assert.equal(await toggle.getAttribute("aria-expanded"),"false");
  assert.equal(await toggle.evaluate(el=>el===document.activeElement),true);
 }finally{await page.close();}
});

test("paging ignores rapid repeats and failures retain the current page",async()=>{
 const page=await browser.newPage();let secondFailed=false;const cursors=[];
 try{
  await page.route("**/api/native/openai/files.list?*",async route=>{
   const cursor=route.request().postDataJSON().params.after||"";cursors.push(cursor);
   await new Promise(resolve=>setTimeout(resolve,100));
   if(cursor&&!secondFailed){secondFailed=true;return route.fulfill({status:503,json:{error:"列表暂不可用"}});}
   await route.fulfill({json:{data:[{id:cursor?"file-page2":"file-page1",filename:cursor?"second.txt":"first.txt"}],has_more:!cursor,last_id:"file-page1"}});
  });
  await page.goto(base+"/ai/openai/alpha/files");await page.locator('[data-resource-id="file-page1"]').waitFor();
  await page.getByRole("button",{name:"下一页",exact:true}).evaluate(button=>{button.click();button.click();});
  await page.getByRole("alert").waitFor();assert.equal(await page.locator('[data-resource-id="file-page1"]').count(),1);
  assert.equal(await page.getByRole("button",{name:"上一页",exact:true}).isDisabled(),true);
  await page.getByRole("button",{name:"重试",exact:true}).click();await page.locator('[data-resource-id="file-page2"]').waitFor();
  await page.getByRole("button",{name:"上一页",exact:true}).click();await page.locator('[data-resource-id="file-page1"]').waitFor();
  assert.deepEqual(cursors,["","file-page1","file-page1",""]);
 }finally{await page.close();}
});

test("logs show actual HTTP bodies and offer complete truncated downloads",async()=>{
 const page=await browser.newPage();try{
  await page.route("**/api/logs",route=>route.fulfill({json:[{id:"log-fixture",operation:"files.upload",provider_id:"alpha",status:"error",started_at:"2026-09-11T12:00:00Z"}]}));
  await page.route("**/api/logs/log-fixture",route=>route.fulfill({json:{metadata:{save_response:true},requests:[{id:"req-fixture",request:{method:"POST",url:"http://fake/upload"},response:{status_code:503},request_body:"request fixture bytes",response_body:"response fixture bytes",response_truncated:true}]}}));
  await page.goto(base+"/logs");await page.getByRole("button",{name:"查看",exact:true}).click();
  const modal=page.getByRole("dialog",{name:"请求详情",exact:true});await modal.getByText("response fixture bytes",{exact:true}).waitFor();
  assert.match(await modal.textContent(),/503/);assert.match(await modal.textContent(),/响应正文预览已截断/);
  assert.equal(await modal.getByRole("link",{name:"下载完整响应正文",exact:true}).count(),1);
  assert.doesNotMatch(await modal.textContent(),/object HTML/);
 }finally{await page.close();}
});

test("profile save keeps the editor locked and supports browsers without randomUUID",async()=>{
 const page=await browser.newPage();let requestBody;
 try{
  await page.addInitScript(()=>Object.defineProperty(Crypto.prototype,"randomUUID",{value:undefined,configurable:true}));
  await page.route("**/api/config",async route=>{
   if(route.request().method()!=="PUT")return route.continue();
   requestBody=route.request().postDataJSON();await new Promise(resolve=>setTimeout(resolve,200));await route.fulfill({status:409,json:{error:"配置已变化"}});
  });
  await page.goto(base+"/ai/openai/profiles");await page.getByRole("button",{name:"新建 profile",exact:true}).click();
  const modal=page.getByRole("dialog",{name:"新建 profile",exact:true});
  await modal.getByLabel("Profile 名称",{exact:true}).fill("HTTP profile");await modal.getByLabel("Base URL",{exact:true}).fill(provider.baseURL);
  await modal.getByRole("button",{name:"保存 profile",exact:true}).click();
  assert.equal(await modal.getByRole("button",{name:"关闭",exact:true}).isDisabled(),true);
  await modal.getByRole("alert").waitFor();assert.match(requestBody.providers.at(-1).id,/^p-[0-9a-f]+$/);
  assert.equal(await modal.getByLabel("Profile 名称",{exact:true}).inputValue(),"HTTP profile");
  await modal.getByRole("button",{name:"载入最新配置，保留输入",exact:true}).click();await modal.getByText("已载入最新配置，请核对后重新保存。").waitFor();
  assert.equal(await modal.getByLabel("Profile 名称",{exact:true}).inputValue(),"HTTP profile");
 }finally{await page.close();}
});

test("switching compatible profiles falls back to an available page",async()=>{
 const page=await browser.newPage();try{
  const config=await(await fetch(base+"/api/config")).json();config.providers.push({...config.providers.find(profile=>profile.id==="compatible"),id:"compatible-basic",name:"仅文字",resources:[]});
  await page.route("**/api/config",route=>route.fulfill({json:config}));
  await page.route("**/api/native/compatible/files.list?*",route=>route.fulfill({json:{data:[],has_more:false}}));
  await page.goto(base+"/ai/compatible/compatible/files");await page.getByLabel("当前 profile").selectOption("compatible-basic");
  await page.waitForURL("**/compatible-basic/chat");await page.getByLabel("消息",{exact:true}).waitFor();
 }finally{await page.close();}
});

test("every provider page uses the same usable desktop and mobile structure",async t=>{
 const page=await browser.newPage({viewport:{width:1440,height:900}});let checked=0;
 const pending=new Set();page.on("request",request=>pending.add(request.url()));page.on("requestfinished",request=>pending.delete(request.url()));page.on("requestfailed",request=>pending.delete(request.url()));
 const groups={openai:["chat","image","image-edit","speech","transcribe","translate","files","containers","batches"],anthropic:["chat","files","batches"],gemini:["chat","image","image-edit","speech","transcribe","video","files","batches"],xai:["chat","image","image-edit","speech","transcribe","video","files","batches"],compatible:["chat","image","image-edit","speech","transcribe","translate","files","containers","batches"]};
 try{
  await page.route("**/api/native/*/*.list?*",route=>route.fulfill({json:{data:[],files:[],batches:[],has_more:false}}));
  for(const width of [1440,390]){
   await page.setViewportSize({width,height:900});
   for(const [vendor,pages]of Object.entries(groups))for(const feature of pages){
    try{await page.goto(base+"/ai/"+vendor+"/"+(vendor==="openai"?"alpha":vendor)+"/"+feature);}catch(error){throw new Error(vendor+"/"+feature+" at "+width+" pending requests: "+JSON.stringify([...pending])+"; page: "+await page.locator("body").innerText({timeout:2000}),{cause:error});}
    try{await page.locator(".page-heading").waitFor({timeout:5000});await page.waitForFunction(()=>document.querySelector(".workspace-form,.resource-manager"));}catch(error){throw new Error(vendor+"/"+feature+" at "+width+": "+await page.locator("#app").innerText(),{cause:error});}
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),vendor+"/"+feature+" overflows at "+width);
    assert.equal(await page.getByRole("alert").count(),0);
    if(["files","containers","batches"].includes(feature))await page.locator(".table-scroll:not([aria-busy]) table").waitFor();
    if(["image","image-edit","speech"].includes(feature)){
     const composer=await page.locator(".composer").boundingBox(),parameters=await page.locator(".parameters").boundingBox();
     assert.ok(width>1000?composer.x<parameters.x:composer.y<parameters.y,"primary input should lead "+vendor+"/"+feature);
     assert.ok(await page.locator(".composer button[type=submit]").evaluate(button=>button.getBoundingClientRect().bottom<innerHeight),"primary action below initial viewport "+vendor+"/"+feature);
    }
    if(feature==="chat"){
     await page.locator(".parameters>summary").click();
     const composer=await page.locator(".composer").boundingBox(),parameters=await page.locator(".parameters").boundingBox();
     assert.ok(width>1000?composer.x<parameters.x:composer.y<parameters.y,"expanded chat settings must leave input accessible "+vendor);
     if(width===1440)assert.ok(parameters.y+parameters.height<900,"parameter pane stays within the desktop viewport");
     if(width===1440)await page.screenshot({path:path.join(repo,"bin/workbench-browser","audit-"+vendor+"-chat.png"),fullPage:true});
    }
    if(width===390&&feature==="image")await page.screenshot({path:path.join(repo,"bin/workbench-browser","audit-"+vendor+"-mobile-image.png"),fullPage:true});
    checked++;if(checked%10===0)t.diagnostic("checked "+checked+" routes; latest "+vendor+"/"+feature+" at "+width);
   }
  }
  assert.equal(checked,74);
 }finally{await page.close();}
});

test("partial stream failures remain visible and preserve the draft",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base+"/ai/openai/beta/chat");await page.getByLabel("消息",{exact:true}).fill("stream-fail");
  await page.getByRole("button",{name:"发送",exact:true}).click();await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);
  assert.match(await page.locator("details.message.assistant").last().textContent(),/已生成的部分回答/);
  assert.match(await page.locator("details.message.assistant").last().getByRole("alert").textContent(),/upstream denied/);
  assert.equal(await page.getByLabel("消息",{exact:true}).inputValue(),"stream-fail");
 }finally{await page.close();}
});

test("download filenames containing percent signs stay usable",async()=>{
 const page=await browser.newPage();try{
  await page.route("**/api/native/openai/files.download?*",route=>route.fulfill({headers:{"Content-Type":"application/json","Content-Disposition":'attachment; filename="100%.json"'},body:'{ "keep": true }\n'}));
  await page.goto(base+"/ai/openai/alpha/chat");await page.getByLabel("消息",{exact:true}).waitFor();
  const result=await page.evaluate(async()=>{const data=await window.WB.native("files.download",{file_id:"test"});return {filename:data.filename,text:await data.blob.text()};});
  assert.deepEqual(result,{filename:"100%.json",text:'{ "keep": true }\n'});
 }finally{await page.close();}
});

test("Gemini empty resource pages accept omitted repeated fields",async()=>{
 const page=await browser.newPage();try{
  await page.route("**/api/native/gemini/files.list?*",route=>route.fulfill({json:{}}));
  await page.goto(base+"/ai/gemini/gemini/files");await page.getByText("这里还没有资源。",{exact:true}).waitFor();
  assert.equal(await page.getByRole("alert").count(),0);
 }finally{await page.close();}
});

test("long streamed replies use one scroller and respect reading earlier text",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base+"/ai/openai/beta/chat");await page.getByLabel("消息",{exact:true}).fill("stream-long");
  await page.getByRole("button",{name:"发送",exact:true}).click();
  await page.waitForFunction(()=>document.querySelector("[data-live-text]")?.textContent.includes("第 90 行"));
  assert.ok(await page.locator("[data-live-text]").evaluate(node=>node.scrollHeight<=node.clientHeight+2),"the message must not add a nested scroller");
  assert.ok(await page.locator("#messages").evaluate(node=>node.scrollHeight-node.scrollTop-node.clientHeight<2),"long streamed text follows the bottom");
  await page.locator("#messages").evaluate(node=>{node.scrollTop=0;});
  await page.waitForFunction(()=>!document.querySelector(".composer button[type=submit]").disabled);
  assert.equal(await page.locator("#messages").evaluate(node=>node.scrollTop),0,"completion preserves the reader's position");
  assert.match(await page.locator(".message.assistant").last().textContent(),/最后一行/);
 }finally{await page.close();}
});
