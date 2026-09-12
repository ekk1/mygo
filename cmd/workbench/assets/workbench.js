"use strict";
(() => {
  const el = (tag, attrs, ...children) => {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(attrs || {})) {
      if (value === undefined || value === null || value === false) continue;
      if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2), value);
      else if (["checked", "disabled", "open", "required", "multiple"].includes(key)) node[key] = Boolean(value);
      else node.setAttribute(key, String(value));
    }
    for (const child of children.flat(Infinity)) if (child !== undefined && child !== null && child !== false) node.append(child instanceof Node ? child : document.createTextNode(String(child)));
    return node;
  };
  const button = (name, action, kind = "secondary") => el("button", {type:"button",class:kind,onclick:action}, name);
  const field = (label, control, hint) => { if(!control.hasAttribute("aria-label"))control.setAttribute("aria-label",label);return el("label", {class:"field"}, el("span",{},label),control,hint ? el("small",{class:"muted"},hint) : null); };
  const input = (name, value = "", type = "text") => el("input", {name, value, type});
  const select = (name, values, current = "") => el("select",{name},values.map(item=>{const [value,label]=Array.isArray(item)?item:[item,item];return el("option",{value,selected:value===current?"":null},label);}));
  const pretty = value => JSON.stringify(value, null, 2);
  const vendors = {
    openai:{name:"OpenAI",initial:"O",description:"Responses、Chat、图片与语音",base:"https://api.openai.com/v1"},
    anthropic:{name:"Anthropic",initial:"A",description:"Claude 对话、文件与 Message Batches",base:"https://api.anthropic.com/v1"},
    gemini:{name:"Google Gemini",initial:"G",description:"文字、图片、音频与视频",base:"https://generativelanguage.googleapis.com"},
    xai:{name:"xAI",initial:"X",description:"Grok 对话、图片、语音与视频",base:"https://api.x.ai/v1"},
    compatible:{name:"通用 AI",initial:"↗",description:"OpenAI 兼容接口 · 中转站",base:""},
  };
  const parts = location.pathname.split("/").filter(Boolean);
  const vendor = parts[0]==="ai" ? parts[1] : null;
  const profileID = parts[2] && parts[2]!=="profiles" ? parts[2] : null;
  const feature = parts[2]==="profiles" ? "profiles" : parts[3] || "chat";
  const W = window.WB = {el,button,field,input,select,pretty,vendors,vendor,profileID,feature,root:document.querySelector("#app"),config:null,profile:null};
  W.readCache=key=>{try{return JSON.parse(sessionStorage.getItem(key)||"null");}catch{return null;}};
  W.writeCache=(key,value)=>{try{sessionStorage.setItem(key,JSON.stringify(value));}catch{}};
  W.url = (page, id = W.profile?.id) => `/ai/${vendor}/${encodeURIComponent(id)}/${page}`;
  W.heading = (name, subtitle, actions) => el("div",{class:"page-heading"},el("div",{},el("p",{class:"eyebrow"},vendor ? vendors[vendor]?.name : "PERSONAL WORKBENCH"),el("h1",{},name),el("p",{class:"muted"},subtitle)),actions);
  W.notice = (text,error=false)=>el("div",{class:`notice ${error?"error":""}`,role:error?"alert":"status"},text);
  W.fail = (error,target=W.root)=>{target.querySelector("[data-error]")?.remove();target.prepend(el("div",{"data-error":""},W.notice(error.message||String(error),true)));};
  const downloadResponse = async response => {
    const disposition=response.headers.get("Content-Disposition")||"";
    const encoded=disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
    let filename=disposition.match(/filename="([^"]*)"/i)?.[1]||disposition.match(/filename=([^;]+)/i)?.[1]?.trim()||"download.bin";
    if(encoded){try{filename=decodeURIComponent(encoded);}catch{filename=encoded;}}
    return {blob:await response.blob(),filename};
  };
  W.api = async (path,options={}) => {
    const response=await fetch(path,{cache:"no-store",...options,headers:{"X-Workbench-Request":"1",...(options.body instanceof FormData ? {} : {"Content-Type":"application/json"}),...options.headers}});
    if (!response.ok) {let text;try{text=(await response.json()).error;}catch{}const error=new Error(text||`请求失败 (${response.status})`);error.status=response.status;throw error;}
    if(options.binary && /attachment/i.test(response.headers.get("Content-Disposition")||""))return downloadResponse(response);
    if (response.headers.get("Content-Type")?.includes("application/x-ndjson")) {
      const reader=response.body.getReader(),decoder=new TextDecoder();let buffer="",completed;
      const line=raw=>{if(!raw.trim())return;const event=JSON.parse(raw);if(event.type==="error")throw new Error(event.error);if(event.type==="done")completed=event.session;else options.onEvent?.(event);};
      try {for(;;){const {value,done}=await reader.read();buffer+=decoder.decode(value||new Uint8Array(),{stream:!done});let i;while((i=buffer.indexOf("\n"))>=0){line(buffer.slice(0,i));buffer=buffer.slice(i+1);}if(done)break;}if(buffer.trim())line(buffer);if(!completed)throw new Error("连接已结束，但未收到完成状态");return completed;}finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
    }
    if(options.binary&&!response.headers.get("Content-Type")?.includes("json"))return downloadResponse(response);
    return response.json();
  };
  W.native = (operation,params={},uploads={},preview=false,signal) => {
    const context=W.requestContext;
    if (!context) throw new Error("请先选择 profile");
    let body;
    if(Object.values(uploads).some(files=>files.length)) {body=new FormData();body.set("provider_id",context.profileID);body.set("params",JSON.stringify(params));for(const [name,files] of Object.entries(uploads))for(const file of files)body.append(name,file);}
    else body=JSON.stringify({provider_id:context.profileID,params});
    return W.api(`/api/native/${context.vendor}/${operation}${preview?"?preview=1&":"?"}revision=${context.revision}`,{method:"POST",body,binary:!preview,signal});
  };
  const objectURLs=new Set();
  W.blobURL=blob=>{const url=URL.createObjectURL(blob);objectURLs.add(url);return url;};
  window.addEventListener("pagehide",()=>{for(const url of objectURLs)URL.revokeObjectURL(url);});
  W.download=(blob,name)=>el("a",{href:W.blobURL(blob),download:name,class:"button secondary"},"下载 "+name);
  W.jsonDetails=value=>{
    const details=el("details",{class:"native-details"},el("summary",{},"原生结果"));let rendered=false;
    details.addEventListener("toggle",()=>{if(details.open&&!rendered){details.append(el("pre",{},pretty(value)));rendered=true;}});
    return details;
  };
  W.mediaPlaceholder=label=>el("span",{class:"media-placeholder"},el("span",{"aria-hidden":"true",class:"media-symbol"},"▧"),el("span",{},label));
  W.result=(value,{lazyMedia=false}={})=>{
    const box=el("div",{class:"result-content"});
    const media=(kind,render)=>{
      if(!lazyMedia){render(box);return;}
      const label=el("span",{},"展开"+kind),details=el("details",{class:"media-preview","data-media-kind":kind},el("summary",{},W.mediaPlaceholder(label)));let rendered=false;
      details.addEventListener("toggle",()=>{label.textContent=(details.open?"收起":"展开")+kind;if(details.open&&!rendered){const content=el("div",{class:"media-content"});render(content);details.append(content);rendered=true;}});
      box.append(details);
    };
    if(value?.blob){const type=value.blob.type;if(type.startsWith("audio/"))box.append(el("audio",{controls:"",src:W.blobURL(value.blob)}));else if(type.startsWith("video/"))box.append(el("video",{controls:"",src:W.blobURL(value.blob)}));box.append(W.download(value.blob,value.filename));return box;}
    if(typeof value==="string"){box.append(el("pre",{class:"result-text"},value),W.copyButton(value),W.download(new Blob([value],{type:"text/plain"}),"result.txt"));return box;}
    let assets=0;const texts=[];
    const visit=(v,key="")=>{if(!v||typeof v!=="object")return;
      if(typeof v.text==="string"&&v.thought!==true)texts.push(v.text);
      if(typeof v.output_text==="string")texts.push(v.output_text);
      if(v.message?.content && typeof v.message.content==="string")texts.push(v.message.content);
      if((v.b64_json || v.type==="image_generation_call"&&v.result) && assets++<20)media("图片",target=>{const bytes=v.b64_json||v.result,format=bytes.startsWith("/9j/")?"jpeg":bytes.startsWith("UklGR")?"webp":"png",uri="data:image/"+format+";base64,"+bytes;target.append(el("img",{src:uri,alt:"生成的图片",loading:"lazy",decoding:"async"}),el("a",{href:uri,download:"image."+format},"下载图片"));});
      const inline=v.inlineData||v.inline_data;if(inline?.data && assets++<20){const mime=inline.mimeType||inline.mime_type||"application/octet-stream";media(mime.startsWith("image/")?"图片":mime.startsWith("audio/")?"音频":"附件",target=>{const raw=Uint8Array.from(atob(inline.data),c=>c.charCodeAt(0));let blob=new Blob([raw],{type:mime});if(mime.startsWith("audio/L16")||mime.startsWith("audio/pcm"))blob=W.wav(raw,Number(mime.match(/rate=(\d+)/)?.[1]||24000));const url=W.blobURL(blob);if(mime.startsWith("image/"))target.append(el("img",{src:url,alt:"生成的图片",loading:"lazy",decoding:"async"}));if(mime.startsWith("audio/"))target.append(el("audio",{src:url,controls:"",preload:"none"}));target.append(W.download(blob,mime.startsWith("audio/")?"speech.wav":"output.png"));});}
      if(W.vendor==="gemini"&&typeof v.uri==="string") {
        try {const media=new URL(v.uri),base=new URL(W.profile.base_url);const match=media.pathname.match(/\/files\/([^/:]+)(?::download)?$/);if(media.origin===base.origin&&match){box.append(button("下载生成的视频",async event=>{const trigger=event.currentTarget;trigger.disabled=true;try{const data=await W.native("files.download",{file_id:"files/"+match[1],filename:"video.mp4"});box.append(W.result(data));}catch(error){W.fail(error,box);}finally{trigger.disabled=false;}}));}}catch{}
      }
      if(typeof v.url==="string" && /^https?:\/\//.test(v.url))box.append(el("a",{href:v.url,target:"_blank",rel:"noopener noreferrer"},"打开媒体 / 下载"));
      for(const [k,x]of Object.entries(v))if(!["inlineData","inline_data","HTTP","http","native_events"].includes(k)){if(Array.isArray(x))x.forEach(y=>visit(y,k));else if(x&&typeof x==="object")visit(x,k);}
    };visit(value);if(texts.length)box.prepend(el("div",{class:"message-text"},[...new Set(texts)].join("\n")));if(texts.length){const copy=W.copyButton([...new Set(texts)].join("\n"));copy.classList.add("copy-result");box.append(copy);}box.append(W.jsonDetails(value));return box;
  };
  W.wav=(bytes,rate)=>{const header=new ArrayBuffer(44);const v=new DataView(header);const str=(offset,s)=>[...s].forEach((c,i)=>v.setUint8(offset+i,c.charCodeAt(0)));str(0,"RIFF");v.setUint32(4,36+bytes.length,true);str(8,"WAVEfmt ");v.setUint32(16,16,true);v.setUint16(20,1,true);v.setUint16(22,1,true);v.setUint32(24,rate,true);v.setUint32(28,rate*2,true);v.setUint16(32,2,true);v.setUint16(34,16,true);str(36,"data");v.setUint32(40,bytes.length,true);return new Blob([header,bytes],{type:"audio/wav"});};
  W.preview = (value) => {
    const {curl,curl_error,...request}=value;
    let expanded=false;const truncate=v=>typeof v==="string" && v.length>2000 ? v.slice(0,2000)+`… [省略 ${v.length-2000} 字符，仅预览折叠]` : Array.isArray(v)?v.map(truncate):v&&typeof v==="object"?Object.fromEntries(Object.entries(v).map(([k,x])=>[k,truncate(x)])):v;
    const pre=el("pre",{"data-request-preview":""},pretty(truncate(request)));
    return el("section",{class:"request-preview"},el("div",{class:"actions preview-actions"},button("展开完整内容",event=>{expanded=!expanded;pre.textContent=pretty(expanded?request:truncate(request));event.currentTarget.textContent=expanded?"折叠长内容":"展开完整内容";}),W.copyButton(()=>pretty(request.body??request),"复制完整请求体"),curl?W.copyButton(curl,"复制 curl"):null,W.download(new Blob([pretty(request.body??request)],{type:"application/json"}),"request.json")),el("p",{class:"small muted"},"长文本仅在预览中折叠，复制和实际发送均保持完整。"),curl?el("p",{class:"small muted"},"curl 使用凭据和文件路径变量，请按命令中的注释设置后运行。"):null,curl_error?W.notice(curl_error,true):null,pre);
  };
  W.showPreview=(value,trigger)=>{const modal=W.dialog("请求预览",trigger);modal.dialog.classList.add("preview-dialog");modal.body.append(W.preview(value));return modal;};
  W.lock = container => {
    const states=[...container.querySelectorAll("input,select,textarea,button")].map(control=>[control,control.disabled]);
    for(const [control] of states)control.disabled=true;
    container.setAttribute("aria-busy","true");
    return ()=>{for(const [control,disabled]of states)control.disabled=disabled;container.removeAttribute("aria-busy");};
  };
  W.validate = form => {
    const invalid=[...form.elements].find(control=>control.willValidate&&!control.validity.valid);
    if(invalid){for(let parent=invalid.parentElement;parent;parent=parent.parentElement)if(parent.tagName==="DETAILS")parent.open=true;invalid.setAttribute("aria-invalid","true");invalid.addEventListener("input",()=>invalid.removeAttribute("aria-invalid"),{once:true});}
    return form.reportValidity();
  };
  W.action = (label, action, kind="secondary", target=W.root) => {
    const node=button(label,async()=>{
      if(node.disabled)return;node.disabled=true;
      try{await action();}catch(error){W.fail(error,target);}
      finally{node.disabled=false;if(node.isConnected&&document.activeElement===document.body)node.focus();}
    },kind);return node;
  };
  W.copyButton = (text,label="复制文字") => {
    const node=W.action(label,async()=>{
      const value=typeof text==="function"?text():text;
      try{if(navigator.clipboard){await navigator.clipboard.writeText(value);node.textContent="已复制";setTimeout(()=>{node.textContent=label;},1800);return;}}catch{}
      const modal=W.dialog("复制内容",node),content=el("textarea",{rows:8,readonly:""},value);
      modal.body.append(field("待复制内容",content),el("p",{class:"small muted"},"自动复制不可用，请使用键盘或长按复制。"));content.focus();content.select();
    },"quiet");
    return node;
  };
  let dialogID=0;
  W.dialog = (title,previous=document.activeElement) => {
    const id="wb-dialog-title-"+(++dialogID);
    const body=el("div",{class:"dialog-body"});let busy=false;
    const close=button("关闭",()=>dialog.close(),"quiet");
    const dialog=el("dialog",{class:"wb-dialog","aria-labelledby":id},
      el("div",{class:"dialog-header"},el("h2",{id},title),close),body);
    dialog.addEventListener("cancel",event=>{if(busy)event.preventDefault();});
    dialog.addEventListener("close",()=>{dialog.remove();if(previous?.isConnected)previous.focus();});
    dialog.addEventListener("click",event=>{if(busy||event.target!==dialog)return;const r=dialog.getBoundingClientRect();if(event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom)dialog.close();});
    document.body.append(dialog);dialog.showModal();
    return {dialog,body,close:()=>dialog.close(),setBusy:value=>{busy=value;close.disabled=value;dialog.setAttribute("aria-busy",String(value));}};
  };
  W.confirm = (title, action, description="此操作会修改当前 profile 的云端资源。") => new Promise(resolve=>{
    const modal=W.dialog(title),status=el("div",{});let completed=false;
    const back=button("返回",modal.close,"quiet");
    const confirm=button("确认",async()=>{
      const unlock=W.lock(modal.body);modal.setBusy(true);status.replaceChildren(W.notice("正在处理…"));
      try{await action();completed=true;modal.close();}
      catch(error){status.replaceChildren(W.notice(error.message,true));}
      finally{unlock();modal.setBusy(false);}
    },"danger");
    modal.body.replaceChildren(el("p",{class:"muted"},description),status,el("div",{class:"actions"},back,confirm));
    modal.dialog.addEventListener("close",()=>resolve(completed),{once:true});back.focus();
  });
  function renderNavigation(){
    const nav=document.querySelector("#navigation");nav.replaceChildren(el("a",{href:"/",class:"nav-link",...(parts.length?{}:{"aria-current":"page"})},"⌂",el("span",{},"工作台")),el("div",{class:"nav-caption"},"模块"),el("a",{href:"/ai",class:"nav-link",...(!vendor&&parts[0]==="ai"?{"aria-current":"page"}:{})},"✦",el("span",{},"AI")));
    if(parts[0]==="ai")for(const [kind,v]of Object.entries(vendors)){
      nav.append(el("a",{href:`/ai/${kind}`,class:"nav-link vendor-link",...(kind===vendor?{"aria-current":"page"}:{})},el("span",{class:"vendor-mark"},v.initial),el("span",{},v.name)));
      if(kind===vendor && W.profile)nav.append(el("div",{class:"feature-nav"},W.menu().map(([id,label])=>el("a",{href:W.url(id),class:"nav-link",...(feature===id?{"aria-current":"page"}:{})},label))));
    }
    const header=document.querySelector("#workspace-header"),side=document.querySelector("#sidebar"),mobile=matchMedia("(max-width:760px)");
    const backdrop=el("button",{class:"nav-backdrop",type:"button","aria-label":"关闭导航",hidden:true});
    const toggle=button("☰ 导航",()=>setNavigation(!side.classList.contains("is-open")),"quiet menu-toggle");
    toggle.setAttribute("aria-controls","sidebar");toggle.setAttribute("aria-expanded","false");
    function setNavigation(open){
      open=open&&mobile.matches;side.classList.toggle("is-open",open);backdrop.hidden=!open;toggle.setAttribute("aria-expanded",String(open));
      W.root.inert=open;header.querySelector(".profile-switch")?.toggleAttribute("inert",open);document.body.classList.toggle("nav-open",open);
      if(open){side.setAttribute("role","dialog");side.setAttribute("aria-modal","true");side.setAttribute("aria-label","导航");(side.querySelector(".feature-nav [aria-current]")||side.querySelector("a")).focus();}
      else{side.removeAttribute("role");side.removeAttribute("aria-modal");side.removeAttribute("aria-label");}
    }
    backdrop.onclick=()=>{setNavigation(false);toggle.focus();};document.body.append(backdrop);
    document.addEventListener("keydown",event=>{
      if(!side.classList.contains("is-open"))return;
      if(event.key==="Escape"){event.preventDefault();setNavigation(false);toggle.focus();}
      if(event.key==="Tab"){
        const nodes=[toggle,...side.querySelectorAll("a,button")].filter(node=>node.getClientRects().length);
        const first=nodes[0],last=nodes.at(-1);
        if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}
        else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}
      }
    });
    mobile.addEventListener("change",()=>setNavigation(false));
    header.replaceChildren(toggle,el("div",{class:"breadcrumbs"},el("a",{href:"/"},"工作台"),parts[0]==="ai"?el("span",{},"/ AI",vendor?` / ${vendors[vendor]?.name||vendor}`:""):null));
    if(W.profile){const profiles=W.config.providers.filter(p=>p.kind===vendor);const picker=select("profile",profiles.map(p=>[p.id,p.name]),W.profile.id);picker.setAttribute("aria-label","当前 profile");picker.addEventListener("change",()=>location.assign(W.url(vendor==="compatible"&&!W.menu(W.config.providers.find(p=>p.id===picker.value)).some(([id])=>id===feature)?"chat":feature,picker.value)));header.append(el("div",{class:"profile-switch"},field("Profile",picker),el("a",{href:`/ai/${vendor}/profiles`,class:"button quiet"},"管理")));}
  }
  function home(){W.root.replaceChildren(W.heading(parts[0]==="ai"?"AI":"个人工作台",parts[0]==="ai"?"选择服务商，进入自己的工作空间。":"一个入口，处理日常工作。"));if(parts[0]!=="ai"){W.root.append(el("a",{href:"/ai",class:"module-entry"},el("span",{class:"module-symbol"},"✦"),el("div",{},el("h2",{},"AI"),el("p",{},"文字、图片、音频、视频与云端资源")),el("span",{},"进入 →")));return;}
    W.root.append(el("div",{class:"vendor-list"},Object.entries(vendors).map(([kind,v])=>el("a",{href:`/ai/${kind}`,class:"vendor-entry"},el("span",{class:"vendor-symbol"},v.initial),el("div",{},el("h2",{},v.name),el("p",{class:"muted"},v.description)),el("span",{class:"badge"},`${W.config.providers.filter(p=>p.kind===kind).length} profiles`),el("span",{},"→")))));
  }
  function profileSettings(){
    const profiles=W.config.providers.filter(p=>p.kind===vendor);
    W.root.replaceChildren(W.heading(`${vendors[vendor].name} · Profiles`,"每个 profile 对应一组 key、base URL 与代理配置。",button("新建 profile",()=>editProfile())));
    W.root.append(el("div",{class:"profile-list"},profiles.map(p=>el("section",{class:"card profile-row"},el("div",{},el("h2",{},p.name),el("p",{class:"muted"},p.base_url),el("span",{class:"badge"},p.has_key?"已保存 key":"未设置 key")),el("div",{class:"actions"},el("a",{href:W.url("chat",p.id),class:"button"},"进入"),button("编辑",()=>editProfile(p)),button("复制",()=>editProfile({...p,id:null,name:p.name+" 副本",has_key:false,api_key:""})),button("删除",()=>W.confirm(`删除 profile「${p.name}」？`,async()=>{const before=W.config.providers;W.config.providers=before.filter(item=>item.id!==p.id);try{await saveConfig();}catch(error){W.config.providers=before;throw error;}profileSettings();},"删除本地连接配置；不会删除服务商的云端资源。"),"danger"))))));
    if(!profiles.length)editProfile();
  }
  async function saveConfig(){W.config=await W.api("/api/config",{method:"PUT",body:JSON.stringify(W.config)});}
  function editProfile(old={}){
    const modal=W.dialog(old.id?"编辑 profile":"新建 profile");
    const name=input("name",old.name||"");name.required=true;
    const base=input("base_url",old.base_url||vendors[vendor].base);base.required=true;
    const key=input("api_key","","password");key.autocomplete="new-password";
    const proxy=input("proxy_url",old.proxy_url||"");
    const protocol=select("protocol",[["responses","Responses"],["chat","Chat Completions"]],old.protocol||"responses");
    const clear=input("clear_key","","checkbox");
    const models=el("textarea",{name:"models",rows:3},(old.models||[]).join("\n"));
    const resources=el("div",{class:"actions"},["files","containers","batches","images","audio"].map(id=>field({files:"文件",containers:"容器",batches:"Batch",images:"图片",audio:"音频"}[id],el("input",{type:"checkbox",name:"capability",value:id,checked:old.resources?.includes(id)}))));
    const status=el("div",{});const submit=el("button",{type:"submit"},"保存 profile");
    const form=el("form",{id:"profile-editor",class:"stack","data-profile-editor":""},el("div",{class:"grid-2"},field("Profile 名称",name),field("Base URL",base),field("API key",key,old.has_key?"已保存；留空保留原 key":"保存在本机，不向页面回显"),field("Proxy URL",proxy,"留空使用环境代理；- 表示直连")),old.has_key?field("清除已保存 key",clear):null,["openai","compatible","xai"].includes(vendor)?field("默认文字协议",protocol):null,field("常用模型（每行一个实际模型 ID）",models),vendor==="compatible"?el("div",{},el("p",{},"开启中转站已支持的能力"),resources):null,el("div",{class:"actions"},submit,button("取消",modal.close)),status);
    form.addEventListener("submit",async event=>{event.preventDefault();if(!W.validate(form))return;const unlock=W.lock(form);modal.setBusy(true);const snapshot=structuredClone(W.config);try{const p={id:old.id||"p-"+(crypto.randomUUID?.()||Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,"0")).join("")),kind:vendor,name:name.value.trim(),base_url:base.value.trim(),proxy_url:proxy.value.trim(),api_key:key.value,clear_key:clear.checked,has_key:old.has_key||false,models:models.value.split(/\r?\n/).map(x=>x.trim()).filter(Boolean),protocol:protocol.value,resources:[...resources.querySelectorAll("input:checked")].map(x=>x.value)};const index=W.config.providers.findIndex(x=>x.id===p.id);if(index>=0)W.config.providers[index]=p;else W.config.providers.push(p);await saveConfig();modal.close();profileSettings();W.root.prepend(W.notice("Profile 已保存。"));}catch(error){W.config=snapshot;status.replaceChildren(W.notice(error.message,true));if(error.status===409)status.append(W.action("载入最新配置，保留输入",async()=>{W.config=await W.api("/api/config");status.replaceChildren(W.notice("已载入最新配置，请核对后重新保存。"));},"secondary",status));}finally{unlock();modal.setBusy(false);}});
    modal.body.append(form);name.focus();
  }
  async function settings(){
    const saveResponses=input("save_responses","","checkbox");saveResponses.checked=W.config.save_responses;
    let selectedTheme="sand";try{selectedTheme=localStorage.getItem("webui-theme")||"sand";}catch{}
    const theme=select("theme",[["sand","燕麦"],["rose","玫瑰灰"],["sage","鼠尾草"],["dusk","暮色"]],selectedTheme),status=el("div",{});
    theme.addEventListener("change",()=>{
      const sheet=document.querySelector("[data-webui-theme-sheet]"),previous=sheet.href;
      sheet.onload=()=>{sheet.dataset.webuiThemeSheet=theme.value;try{localStorage.setItem("webui-theme",theme.value);}catch{}sheet.onload=null;};
      sheet.onerror=()=>{sheet.onload=null;sheet.onerror=null;sheet.href=previous;status.replaceChildren(W.notice("主题加载失败，请重试。",true));};
      sheet.href="/webui/themes/"+theme.value+".css";
    });
    const form=el("form",{class:"card stack settings-form"},field("主题",theme),field("默认保存响应正文日志",saveResponses,"请求日志始终保留；响应正文可能包含较大的媒体数据。"),status,el("div",{class:"actions"},el("button",{type:"submit"},"保存设置")));
    form.addEventListener("submit",async event=>{
      event.preventDefault();const unlock=W.lock(form),previous=W.config.save_responses;
      try{W.config.save_responses=saveResponses.checked;await saveConfig();status.replaceChildren(W.notice("设置已保存。"));}
      catch(error){W.config.save_responses=previous;status.replaceChildren(W.notice(error.message,true));}
      finally{unlock();}
    });
    W.root.replaceChildren(W.heading("全局设置","服务商的 key 和地址在各自的 profile 中配置。"),form);
  }
  async function logs(){
    const status=el("div",{}),list=el("div",{}),search=input("log_search","","search");
    const profile=select("log_profile",[["","全部 profiles"],...W.config.providers.map(p=>[p.id,p.name])]);
    const cached=W.readCache("wb-log-list");let loaded=Array.isArray(cached),rows=loaded?cached:[];
    const render=()=>{
      const query=search.value.trim().toLowerCase(),filtered=rows.filter(log=>(!profile.value||log.provider_id===profile.value)&&[log.operation,log.status,log.error].some(value=>String(value||"").toLowerCase().includes(query)));
      list.replaceChildren(...filtered.map(log=>el("div",{class:"log-row"},el("div",{},el("strong",{},log.operation),el("small",{class:"resource-id"},new Date(log.started_at).toLocaleString())),el("span",{},W.config.providers.find(p=>p.id===log.provider_id)?.name||log.provider_id),el("span",{class:"badge"},log.status),button("查看",()=>show(log)))));
      if(!filtered.length)list.append(el("p",{class:"empty"},loaded?"没有匹配的请求记录。":"尚未加载日志，点击「刷新」读取。"));
    };
    async function load(){status.replaceChildren(W.notice("正在读取日志…"));try{rows=await W.api("/api/logs");loaded=true;W.writeCache("wb-log-list",rows);render();status.replaceChildren();}catch(error){status.replaceChildren(W.notice(error.message,true));}}
    async function show(log){
      const modal=W.dialog("请求详情");modal.body.replaceChildren(W.notice("正在读取详情…"));
      try{
        const data=await W.api(`/api/logs/${log.id}`);if(!modal.dialog.isConnected)return;
        modal.body.replaceChildren(el("details",{},el("summary",{},"操作信息"),el("pre",{},pretty(data.metadata))),
          ...data.requests.map(req=>el("section",{class:"log-request stack"},
            el("h3",{},"HTTP 请求"),el("pre",{},pretty(req.request)),
            el("h3",{},"Request body"),req.request_truncated?W.notice("请求正文预览已截断，请下载完整记录。"):null,el("pre",{},req.request_body||"（空）"),
            el("a",{href:`/api/logs/${log.id}/${req.id}/request.body`,download:"request.body",class:"button secondary"},"下载完整请求正文"),
            el("h3",{},"HTTP 响应"),el("pre",{},pretty(req.response)),
            el("h3",{},"Response body"),req.response_truncated?W.notice("响应正文预览已截断，请下载完整记录。"):null,
            el("pre",{},data.metadata.save_response?(req.response_body||"（空）"):"未保存响应正文"),
            data.metadata.save_response?el("a",{href:`/api/logs/${log.id}/${req.id}/response.body`,download:"response.body",class:"button secondary"},"下载完整响应正文"):null)));
      }catch(error){if(modal.dialog.isConnected)modal.body.replaceChildren(W.notice(error.message,true));}
    }
    const refresh=W.action("刷新",load);
    search.addEventListener("input",render);profile.addEventListener("change",render);
    W.root.replaceChildren(W.heading("请求日志","按 profile 和操作查找请求，查看完整 HTTP 记录。"),el("section",{class:"card"},el("div",{class:"resource-toolbar"},field("Profile",profile),field("搜索日志",search),refresh),status,list));
    render();
  }
  async function boot(){try{W.config=await W.api("/api/config");if(vendor&&!vendors[vendor])throw new Error("服务商入口不存在");if(profileID){W.profile=W.config.providers.find(p=>p.id===profileID&&p.kind===vendor);if(!W.profile)throw new Error("此服务商下找不到这个 profile，请重新选择。");sessionStorage.setItem(`wb-profile-${vendor}`,profileID);}else if(vendor&&feature!=="profiles"){const list=W.config.providers.filter(p=>p.kind===vendor);if(list.length){const remembered=sessionStorage.getItem(`wb-profile-${vendor}`);location.replace(W.url("chat",list.find(p=>p.id===remembered)?.id||list[0].id));return;}}
      W.requestContext=Object.freeze({profileID:W.profile?.id,vendor,revision:W.config.revision});
      document.title=[W.profile?W.menu().find(([id])=>id===feature)?.[1]:null,W.profile?.name,vendors[vendor]?.name||({settings:"全局设置",logs:"请求日志",ai:"AI"})[parts[0]],"个人工作台"].filter(Boolean).join(" · ");
      renderNavigation();if(parts[0]==="settings")await settings();else if(parts[0]==="logs")await logs();else if(vendor&&(feature==="profiles"||!W.profile))profileSettings();else if(W.profile){if(!W.menu().some(([id])=>id===feature))throw new Error("当前 profile 没有启用此功能。");if(["files","containers","batches"].includes(feature))await W.resources();else await W.workspace();}else home();
    }catch(error){W.root.replaceChildren(W.notice(error.message,true),el("a",{href:"/ai"},"返回 AI"));}}
  document.addEventListener("DOMContentLoaded",boot);
})();
