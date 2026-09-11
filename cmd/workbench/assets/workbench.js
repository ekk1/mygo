"use strict";
(() => {
  const css = document.createElement("link"); css.rel = "stylesheet"; css.href = "/assets/workbench.css"; document.head.append(css);
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
  W.url = (page, id = W.profile?.id) => `/ai/${vendor}/${encodeURIComponent(id)}/${page}`;
  W.heading = (name, subtitle, actions) => el("div",{class:"page-heading"},el("div",{},el("p",{class:"eyebrow"},vendor ? vendors[vendor]?.name : "PERSONAL WORKBENCH"),el("h1",{},name),el("p",{class:"muted"},subtitle)),actions);
  W.notice = (text,error=false)=>el("div",{class:`notice ${error?"error":""}`,role:error?"alert":"status"},text);
  W.fail = (error,target=W.root)=>{target.querySelector("[data-error]")?.remove();target.prepend(el("div",{"data-error":""},W.notice(error.message||String(error),true)));};
  W.api = async (path,options={}) => {
    const response=await fetch(path,{cache:"no-store",...options,headers:{"X-Workbench-Request":"1",...(options.body instanceof FormData ? {} : {"Content-Type":"application/json"}),...options.headers}});
    if (!response.ok) {let text;try{text=(await response.json()).error;}catch{}throw new Error(text||`请求失败 (${response.status})`);}
    if (response.headers.get("Content-Type")?.includes("application/x-ndjson")) {
      const reader=response.body.getReader(),decoder=new TextDecoder();let buffer="",completed;
      const line=raw=>{if(!raw.trim())return;const event=JSON.parse(raw);if(event.type==="error")throw new Error(event.error);if(event.type==="done")completed=event.session;else options.onEvent?.(event);};
      try {for(;;){const {value,done}=await reader.read();buffer+=decoder.decode(value||new Uint8Array(),{stream:!done});let i;while((i=buffer.indexOf("\n"))>=0){line(buffer.slice(0,i));buffer=buffer.slice(i+1);}if(done)break;}if(buffer.trim())line(buffer);if(!completed)throw new Error("连接已结束，但未收到完成状态");return completed;}finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
    }
    if (options.binary && !response.headers.get("Content-Type")?.includes("json")) return {blob:await response.blob(),filename:decodeURIComponent(response.headers.get("Content-Disposition")?.match(/filename\*=UTF-8''([^;]+)/i)?.[1]||response.headers.get("Content-Disposition")?.match(/filename="?([^";]+)/)?.[1]||"download.bin")};
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
  W.jsonDetails=value=>el("details",{class:"native-details"},el("summary",{},"原生结果"),el("pre",{},pretty(value)));
  W.result=(value)=>{
    const box=el("div",{class:"result-content"});
    if(value?.blob){const type=value.blob.type;if(type.startsWith("audio/"))box.append(el("audio",{controls:"",src:W.blobURL(value.blob)}));else if(type.startsWith("video/"))box.append(el("video",{controls:"",src:W.blobURL(value.blob)}));box.append(W.download(value.blob,value.filename));return box;}
    if(typeof value==="string"){box.append(el("pre",{class:"result-text"},value));return box;}
    let assets=0;const texts=[];
    const visit=(v,key="")=>{if(!v||typeof v!=="object")return;
      if(typeof v.text==="string")texts.push(v.text);
      if(typeof v.output_text==="string")texts.push(v.output_text);
      if(v.message?.content && typeof v.message.content==="string")texts.push(v.message.content);
      if((v.b64_json || v.type==="image_generation_call"&&v.result) && assets++<20){const uri="data:image/png;base64,"+(v.b64_json||v.result);box.append(el("img",{src:uri,alt:"生成的图片"}),el("a",{href:uri,download:"image.png"},"下载图片"));}
      const inline=v.inlineData||v.inline_data;if(inline?.data && assets++<20){const mime=inline.mimeType||inline.mime_type||"application/octet-stream";const raw=Uint8Array.from(atob(inline.data),c=>c.charCodeAt(0));let blob=new Blob([raw],{type:mime});if(mime.startsWith("audio/L16")||mime.startsWith("audio/pcm"))blob=W.wav(raw,Number(mime.match(/rate=(\d+)/)?.[1]||24000));const url=W.blobURL(blob);if(mime.startsWith("image/"))box.append(el("img",{src:url,alt:"生成的图片"}));if(mime.startsWith("audio/"))box.append(el("audio",{src:url,controls:""}));box.append(W.download(blob,mime.startsWith("audio/")?"speech.wav":"output.png"));}
      if(W.vendor==="gemini"&&typeof v.uri==="string") {
        try {const media=new URL(v.uri),base=new URL(W.profile.base_url);const match=media.pathname.match(/\/files\/([^/:]+)(?::download)?$/);if(media.origin===base.origin&&match){box.append(button("下载生成的视频",async event=>{event.currentTarget.disabled=true;try{const data=await W.native("files.download",{file_id:"files/"+match[1],filename:"video.mp4"});box.append(W.result(data));}catch(error){W.fail(error,box);}finally{event.currentTarget.disabled=false;}}));}}catch{}
      }
      if(typeof v.url==="string" && /^https?:\/\//.test(v.url))box.append(el("a",{href:v.url,target:"_blank",rel:"noopener noreferrer"},"打开媒体 / 下载"));
      for(const [k,x]of Object.entries(v))if(!["inlineData","inline_data","HTTP","http","native_events"].includes(k)){if(Array.isArray(x))x.forEach(y=>visit(y,k));else if(x&&typeof x==="object")visit(x,k);}
    };visit(value);if(texts.length)box.prepend(el("div",{class:"message-text"},[...new Set(texts)].join("\n")));box.append(W.jsonDetails(value));return box;
  };
  W.wav=(bytes,rate)=>{const header=new ArrayBuffer(44);const v=new DataView(header);const str=(offset,s)=>[...s].forEach((c,i)=>v.setUint8(offset+i,c.charCodeAt(0)));str(0,"RIFF");v.setUint32(4,36+bytes.length,true);str(8,"WAVEfmt ");v.setUint32(16,16,true);v.setUint16(20,1,true);v.setUint16(22,1,true);v.setUint32(24,rate,true);v.setUint32(28,rate*2,true);v.setUint16(32,2,true);v.setUint16(34,16,true);str(36,"data");v.setUint32(40,bytes.length,true);return new Blob([header,bytes],{type:"audio/wav"});};
  W.preview = (value) => {
    let expanded=false;const truncate=v=>typeof v==="string" && v.length>2000 ? v.slice(0,2000)+`… [省略 ${v.length-2000} 字符，仅预览折叠]` : Array.isArray(v)?v.map(truncate):v&&typeof v==="object"?Object.fromEntries(Object.entries(v).map(([k,x])=>[k,truncate(x)])):v;
    const pre=el("pre",{"data-request-preview":""},pretty(truncate(value)));
    return el("section",{class:"request-preview"},el("div",{class:"section-head"},el("h3",{},"发送前 · 原始请求"),button("展开完整内容",event=>{expanded=!expanded;pre.textContent=pretty(expanded?value:truncate(value));event.currentTarget.textContent=expanded?"折叠长内容":"展开完整内容";})),el("p",{class:"small muted"},"这是上游请求；长文本只在这里折叠，实际发送保持完整。"),pre,button("复制完整请求体",async()=>{try{await navigator.clipboard.writeText(pretty(value.body??value));}catch(error){W.fail(error);}}),W.download(new Blob([pretty(value.body??value)],{type:"application/json"}),"request.json"));
  };
  W.confirm = (title, action) => {const dialog=el("dialog",{},el("h2",{},title),el("p",{},"此操作会修改当前 profile 的云端资源。"),el("div",{class:"actions"},button("返回",()=>dialog.close()),button("确认",async()=>{dialog.close();try{await action();}catch(e){W.fail(e);}},"danger")));dialog.addEventListener("close",()=>dialog.remove());document.body.append(dialog);dialog.showModal();};
  function renderNavigation(){
    const nav=document.querySelector("#navigation");nav.replaceChildren(el("a",{href:"/",class:"nav-link",...(parts.length?{}:{"aria-current":"page"})},"⌂",el("span",{},"工作台")),el("div",{class:"nav-caption"},"模块"),el("a",{href:"/ai",class:"nav-link",...(!vendor&&parts[0]==="ai"?{"aria-current":"page"}:{})},"✦",el("span",{},"AI")));
    if(parts[0]==="ai")for(const [kind,v]of Object.entries(vendors)){
      nav.append(el("a",{href:`/ai/${kind}`,class:"nav-link vendor-link",...(kind===vendor?{"aria-current":"page"}:{})},el("span",{class:"vendor-mark"},v.initial),el("span",{},v.name)));
      if(kind===vendor && W.profile)nav.append(el("div",{class:"feature-nav"},W.menu().map(([id,label])=>el("a",{href:W.url(id),class:"nav-link",...(feature===id?{"aria-current":"page"}:{})},label))));
    }
    const header=document.querySelector("#workspace-header");header.replaceChildren(button("☰ 导航",()=>{const side=document.querySelector("#sidebar");const open=side.classList.toggle("is-open");header.querySelector(".menu-toggle").setAttribute("aria-expanded",String(open));},"quiet menu-toggle"),el("div",{class:"breadcrumbs"},el("a",{href:"/"},"工作台"),parts[0]==="ai"?el("span",{},"/ AI",vendor?` / ${vendors[vendor]?.name||vendor}`:""):null));
    if(W.profile){const profiles=W.config.providers.filter(p=>p.kind===vendor);const picker=select("profile",profiles.map(p=>[p.id,p.name]),W.profile.id);picker.setAttribute("aria-label","当前 profile");picker.addEventListener("change",()=>location.assign(W.url(feature,picker.value)));header.append(el("div",{class:"profile-switch"},field("Profile",picker),el("a",{href:`/ai/${vendor}/profiles`,class:"button quiet"},"管理")));}
  }
  function home(){W.root.replaceChildren(W.heading(parts[0]==="ai"?"AI":"个人工作台",parts[0]==="ai"?"选择服务商，进入自己的工作空间。":"一个入口，处理日常工作。"));if(parts[0]!=="ai"){W.root.append(el("a",{href:"/ai",class:"module-entry"},el("span",{class:"module-symbol"},"✦"),el("div",{},el("h2",{},"AI"),el("p",{},"文字、图片、音频、视频与云端资源")),el("span",{},"进入 →")));return;}
    W.root.append(el("div",{class:"vendor-list"},Object.entries(vendors).map(([kind,v])=>el("a",{href:`/ai/${kind}`,class:"vendor-entry"},el("span",{class:"vendor-symbol"},v.initial),el("div",{},el("h2",{},v.name),el("p",{class:"muted"},v.description)),el("span",{class:"badge"},`${W.config.providers.filter(p=>p.kind===kind).length} profiles`),el("span",{},"→")))));
  }
  function profileSettings(){
    const profiles=W.config.providers.filter(p=>p.kind===vendor);
    W.root.replaceChildren(W.heading(`${vendors[vendor].name} · Profiles`,"每个 profile 对应一组 key、base URL 与代理配置。",button("新建 profile",()=>editProfile())));
    W.root.append(el("div",{class:"profile-list"},profiles.map(p=>el("section",{class:"card profile-row"},el("div",{},el("h2",{},p.name),el("p",{class:"muted"},p.base_url),el("span",{class:"badge"},p.has_key?"已保存 key":"未设置 key")),el("div",{class:"actions"},el("a",{href:W.url("chat",p.id),class:"button"},"进入"),button("编辑",()=>editProfile(p)),button("复制",()=>editProfile({...p,id:null,name:p.name+" 副本",has_key:false,api_key:""})),button("删除",()=>W.confirm(`删除 profile「${p.name}」？`,async()=>{const before=W.config.providers;W.config.providers=before.filter(item=>item.id!==p.id);try{await saveConfig();}catch(error){W.config.providers=before;throw error;}profileSettings();}),"danger"))))));
    if(!profiles.length)editProfile();
  }
  async function saveConfig(){W.config=await W.api("/api/config",{method:"PUT",body:JSON.stringify(W.config)});}
  function editProfile(old={}){
    W.root.querySelector("#profile-editor")?.remove();
    const name=input("name",old.name||"");name.required=true;
    const base=input("base_url",old.base_url||vendors[vendor].base);base.required=true;
    const key=input("api_key","","password");key.autocomplete="new-password";
    const proxy=input("proxy_url",old.proxy_url||"");
    const protocol=select("protocol",[["responses","Responses"],["chat","Chat Completions"]],old.protocol||"responses");
    const clear=input("clear_key","","checkbox");
    const models=el("textarea",{name:"models",rows:3},(old.models||[]).join("\n"));
    const resources=el("div",{class:"actions"},["files","containers","batches","images","audio"].map(id=>field({files:"文件",containers:"容器",batches:"Batch",images:"图片",audio:"音频"}[id],el("input",{type:"checkbox",name:"capability",value:id,checked:old.resources?.includes(id)}))));
    const status=el("div",{});const submit=el("button",{type:"submit"},"保存 profile");
    const form=el("form",{id:"profile-editor",class:"card stack","data-profile-editor":""},el("h2",{},old.id?"编辑 profile":"新建 profile"),el("div",{class:"grid-2"},field("Profile 名称",name),field("Base URL",base),field("API key",key,old.has_key?"已保存；留空保留原 key":"保存在本机，不向页面回显"),field("Proxy URL",proxy,"留空使用环境代理；- 表示直连")),old.has_key?field("清除已保存 key",clear):null,["openai","compatible","xai"].includes(vendor)?field("默认文字协议",protocol):null,field("常用模型（每行一个实际模型 ID）",models),vendor==="compatible"?el("div",{},el("p",{},"开启中转站已支持的能力"),resources):null,el("div",{class:"actions"},submit,button("取消",()=>form.remove())),status);
    form.addEventListener("submit",async event=>{event.preventDefault();submit.disabled=true;const snapshot=structuredClone(W.config);try{const p={id:old.id||"p-"+crypto.randomUUID(),kind:vendor,name:name.value.trim(),base_url:base.value.trim(),proxy_url:proxy.value.trim(),api_key:key.value,clear_key:clear.checked,has_key:old.has_key||false,models:models.value.split(/\r?\n/).map(x=>x.trim()).filter(Boolean),protocol:protocol.value,resources:[...resources.querySelectorAll("input:checked")].map(x=>x.value)};const index=W.config.providers.findIndex(x=>x.id===p.id);if(index>=0)W.config.providers[index]=p;else W.config.providers.push(p);await saveConfig();profileSettings();W.root.prepend(W.notice("Profile 已保存。"));}catch(error){W.config=snapshot;status.replaceChildren(W.notice(error.message,true));}finally{submit.disabled=false;}});
    W.root.append(form);name.focus();
  }
  async function settings(){const saveResponses=input("save_responses","","checkbox");saveResponses.checked=W.config.save_responses;const theme=select("theme",[["sand","燕麦"],["rose","玫瑰灰"],["sage","鼠尾草"],["dusk","暮色"]],localStorage.getItem("webui-theme")||"sand");theme.onchange=()=>{localStorage.setItem("webui-theme",theme.value);location.reload();};W.root.replaceChildren(W.heading("全局设置","服务商的 key 和地址在各自的 profile 中配置。"),el("section",{class:"card stack"},field("主题",theme),field("默认保存响应正文日志",saveResponses,"请求日志始终保留；响应正文可能包含较大的媒体数据。"),button("保存设置",async()=>{try{W.config.save_responses=saveResponses.checked;await saveConfig();W.root.prepend(W.notice("设置已保存。"));}catch(error){W.fail(error);}})));}
  async function logs() {
    const rows=await W.api("/api/logs");
    const detail=el("section",{id:"log-detail",class:"card log-detail",hidden:""});
    async function show(log) {
      try {
        const data=await W.api(`/api/logs/${log.id}`);
        detail.removeAttribute("hidden");
        detail.replaceChildren(el("h2",{},"请求详情"),el("pre",{},pretty(data.metadata)),data.requests.map(req=>el("div",{},
          el("h3",{},"Request body"),el("pre",{},req.request_body||"（空）"),
          el("a",{href:`/api/logs/${log.id}/${req.id}/request.body`,download:"request.body"},"下载完整请求正文"),
          el("h3",{},"Response body"),el("pre",{},req.response_body||"未保存响应正文")
        )));
      } catch(error) {W.fail(error);}
    }
    W.root.replaceChildren(W.heading("请求日志","查看各 profile 的请求与响应。"),el("section",{class:"card"},rows.length?rows.map(log=>el("div",{class:"log-row"},
      el("strong",{},log.operation),el("span",{},W.config.providers.find(p=>p.id===log.provider_id)?.name||log.provider_id),
      el("span",{class:"badge"},log.status),button("查看",()=>show(log))
    )):el("p",{class:"empty"},"还没有请求记录。")),detail);
  }
  async function boot(){try{W.config=await W.api("/api/config");if(vendor&&!vendors[vendor])throw new Error("服务商入口不存在");if(profileID){W.profile=W.config.providers.find(p=>p.id===profileID&&p.kind===vendor);if(!W.profile)throw new Error("此服务商下找不到这个 profile，请重新选择。");sessionStorage.setItem(`wb-profile-${vendor}`,profileID);}else if(vendor&&feature!=="profiles"){const list=W.config.providers.filter(p=>p.kind===vendor);if(list.length){const remembered=sessionStorage.getItem(`wb-profile-${vendor}`);location.replace(W.url("chat",list.find(p=>p.id===remembered)?.id||list[0].id));return;}}
      W.requestContext=Object.freeze({profileID:W.profile?.id,vendor,revision:W.config.revision});
      renderNavigation();if(parts[0]==="settings")await settings();else if(parts[0]==="logs")await logs();else if(vendor&&(feature==="profiles"||!W.profile))profileSettings();else if(W.profile){if(!W.menu().some(([id])=>id===feature))throw new Error("当前 profile 没有启用此功能。");if(["files","containers","batches"].includes(feature))await W.resources();else await W.workspace();}else home();
    }catch(error){W.root.replaceChildren(W.notice(error.message,true),el("a",{href:"/ai"},"返回 AI"));}}
  document.addEventListener("DOMContentLoaded",boot);
})();
