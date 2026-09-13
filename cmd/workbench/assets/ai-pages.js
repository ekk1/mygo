"use strict";
(() => {
 const W=window.WB,{el,button,field,input,select,pretty}=W;
 const labels={chat:"文字对话",image:"图片生成","image-edit":"图片编辑",speech:"语音合成",transcribe:"音频转写",translate:"音频翻译",video:"视频生成",files:"文件",containers:"容器",batches:"Batch 任务"};
 const menus={openai:["chat","image","image-edit","speech","transcribe","translate","files","containers","batches"],anthropic:["chat","files","batches"],gemini:["chat","image","image-edit","speech","transcribe","video","files","batches"],xai:["chat","image","image-edit","speech","transcribe","video","files","batches"]};
 W.menu=(selectedProfile=W.profile)=>{let ids=menus[W.vendor];if(W.vendor==="compatible"){ids=["chat"];for(const name of selectedProfile?.resources||[]){if(name==="images")ids.push("image","image-edit");else if(name==="audio")ids.push("speech","transcribe","translate");else ids.push(name);}}return(ids||[]).map(id=>[id,labels[id]]);};
 const descriptors=(kind,page)=>{
   const text=page==="chat",image=page==="image"||page==="image-edit";
   const fields=[];
   if(text){fields.push(["stream","流式输出","checkbox",true]);if(["openai","compatible","xai"].includes(kind))fields.push(["protocol","接口协议","select","",[["responses","Responses"],["chat","Chat Completions"]]]);
     fields.push(["system","系统指令","textarea",""]);
     if(kind==="gemini")fields.push(["temperature","Temperature","number",""],["maxOutputTokens","最大输出 tokens","number",""],["thinkingBudget","Thinking budget","number",""],["tier","Service tier","select","",[["","省略（服务商默认）"],["standard","standard"],["flex","flex"],["priority","priority"]]]);
     else if(kind==="anthropic")fields.push(["max_tokens","最大输出 tokens","number","4096"],["temperature","Temperature","number",""],["tier","Service tier","select","",[["","省略（服务商默认）"],["auto","auto"],["standard_only","standard_only"]]],["thinking","Thinking budget tokens","number",""]);
     else fields.push(["tier","Service tier","text",""],["temperature","Temperature","number",""],["max_tokens","最大输出 tokens","number",""],["reasoning","Reasoning effort","text",""]);
     fields.push(["search","联网搜索","checkbox",false],["code","代码执行","checkbox",false]);
     if(kind==="openai"||kind==="compatible")fields.push(["image_tool","图片生成工具","checkbox",false],["container","容器 ID（可选）","text",""]);
     fields.push(["file_ids",kind==="gemini"?"文件 URI（每行一个）":"附件文件 ID（每行一个）","textarea",""]);
     if(kind==="gemini")fields.push(["mime","文件 MIME 类型","text","application/pdf"]);
   } else if(image){if(kind==="gemini")fields.push(["aspectRatio","宽高比","text","1:1"],["imageSize","分辨率","text",""]);else if(kind==="xai")fields.push(["aspect_ratio","宽高比","text","1:1"],["resolution","分辨率","text",""]);else fields.push(["size","尺寸","text","1024x1024"],["quality","质量","select","auto",["auto","low","medium","high"]],["output_format","输出格式","select","png",["png","jpeg","webp"]]);
     if(kind!=="gemini")fields.push(["n","图片数量","number","1"]);
   }else if(page==="speech"){
     fields.push(kind==="gemini"?["voice","音色","text","Kore"]:kind==="xai"?["voice","音色 ID","text","eve"]:["voice","音色","text","alloy"]);
     if(kind==="openai"||kind==="compatible")fields.push(["response_format","音频格式","select","mp3",["mp3","wav","opus","flac","aac","pcm"]],["instructions","声音指令","textarea",""]);
     if(kind==="xai")fields.push(["language","语言","text","auto"]);
   }else if(page==="transcribe"||page==="translate"){
     if(kind!=="gemini")fields.push(["language","语言（可选）","text",""]);
     if(kind==="openai"||kind==="compatible")fields.push(["response_format","输出格式","select","json",["json","verbose_json","text","srt","vtt","diarized_json"]]);
   }else if(page==="video")fields.push(["aspect_ratio","宽高比","text","16:9"],["duration","时长（秒，可选）","number",""]);
   return fields;
 };
 const optional=(body,key,value)=>{if(value!==""&&value!==undefined)body[key]=value;};
 const number=v=>v===""||v===undefined||v===null?undefined:Number(v);
 const lines=v=>(v||"").split(/[\n,]/).map(x=>x.trim()).filter(Boolean);
 const nativeExtras=(body,raw)=>{const extra=JSON.parse(raw||"{}");if(!extra||Array.isArray(extra)||typeof extra!=="object")throw new Error("扩展参数必须是 JSON 对象");for(const [key,value]of Object.entries(extra)){if(Object.hasOwn(body,key))throw new Error(`扩展参数 ${key} 与页面字段重复，请只在一处设置`);body[key]=value;}return body;};
 function openaiRequest(page,v){const params={model:v.model};let operation;
   if(page==="chat"){
    const tools=[];if(v.search)tools.push({type:"web_search"});if(v.image_tool)tools.push({type:"image_generation"});if(v.code)tools.push({type:"code_interpreter",container:v.container||{type:"auto"}});
    if(v.protocol==="chat"){if(tools.length||lines(v.file_ids).length)throw new Error("这些工具和文件 ID 用于 Responses，请切换协议或在扩展参数中提供 Chat 原生结构");operation="chat.create";params.messages=[...(v.system?[{role:"system",content:v.system}]:[]),{role:"user",content:v.prompt}];optional(params,"max_completion_tokens",number(v.max_tokens));optional(params,"reasoning_effort",v.reasoning);}
    else {operation="responses.create";params.input=[{role:"user",content:[{type:"input_text",text:v.prompt},...lines(v.file_ids).map(id=>({type:"input_file",file_id:id}))]}];optional(params,"instructions",v.system);optional(params,"max_output_tokens",number(v.max_tokens));if(v.reasoning)params.reasoning={effort:v.reasoning};if(tools.length)params.tools=tools;}
    optional(params,"service_tier",v.tier);optional(params,"temperature",number(v.temperature));
   }else if(page==="image"||page==="image-edit"){operation=page==="image"?"images.generate":"images.edit";params.prompt=v.prompt;for(const key of ["size","quality","output_format"])optional(params,key,v[key]);optional(params,"n",number(v.n));}
   else if(page==="speech"){operation="audio.speech";params.input=v.prompt;params.voice=v.voice;params.response_format=v.response_format;optional(params,"instructions",v.instructions);}
   else {operation=page==="translate"?"audio.translate":"audio.transcribe";optional(params,"language",v.language);optional(params,"response_format",v.response_format);if(v.prompt)params.prompt=v.prompt;}
   return {operation,params:nativeExtras(params,v.extra)};
 }
 function anthropicRequest(page,v){const params={model:v.model,max_tokens:number(v.max_tokens),messages:[{role:"user",content:[{type:"text",text:v.prompt},...lines(v.file_ids).map(id=>({type:"document",source:{type:"file",file_id:id}}))]}]};optional(params,"system",v.system);optional(params,"temperature",number(v.temperature));optional(params,"service_tier",v.tier);if(v.thinking)params.thinking={type:"enabled",budget_tokens:Number(v.thinking)};const tools=[];if(v.search)tools.push({type:"web_search_20250305",name:"web_search"});if(v.code)tools.push({type:"code_execution_20250825",name:"code_execution"});if(tools.length)params.tools=tools;return {operation:"messages.create",params:nativeExtras(params,v.extra)};}
 function geminiRequest(page,v){if(page==="video"){const params={model:v.model,instances:[{prompt:v.prompt}],parameters:{aspectRatio:v.aspect_ratio}};if(v.duration)params.parameters.durationSeconds=Number(v.duration);return {operation:"videos.create",params:nativeExtras(params,v.extra)};}
  const parts=[{text:v.prompt||"请准确转写这段音频。"}];for(const uri of lines(v.file_ids))parts.push({fileData:{fileUri:uri,mimeType:v.mime}});
  const params={model:v.model,contents:[{role:"user",parts}]};const generation={};if(v.system)params.systemInstruction={parts:[{text:v.system}]};optional(generation,"temperature",number(v.temperature));optional(generation,"maxOutputTokens",number(v.maxOutputTokens));if(v.thinkingBudget!==""&&v.thinkingBudget!==undefined)generation.thinkingConfig={thinkingBudget:Number(v.thinkingBudget)};
  if(page==="image"||page==="image-edit"){generation.responseModalities=["TEXT","IMAGE"];generation.imageConfig={aspectRatio:v.aspectRatio};optional(generation.imageConfig,"imageSize",v.imageSize);}
  if(page==="speech"){generation.responseModalities=["AUDIO"];generation.speechConfig={voiceConfig:{prebuiltVoiceConfig:{voiceName:v.voice}}};}
  if(Object.keys(generation).length)params.generationConfig=generation;optional(params,"serviceTier",v.tier);
  const tools=[];if(v.search)tools.push({googleSearch:{}});if(v.code)tools.push({codeExecution:{}});if(tools.length)params.tools=tools;
  return {operation:"content.generate",params:nativeExtras(params,v.extra)};
 }
 function xaiRequest(page,v){const params={};let operation;
  if(page==="chat"){params.model=v.model;const tools=[];if(v.search)tools.push({type:"web_search"},{type:"x_search"});if(v.code)tools.push({type:"code_interpreter"});if(v.protocol==="chat"){if(tools.length||lines(v.file_ids).length)throw new Error("Grok 服务端工具及文件在 Responses 页面使用");operation="chat.create";params.messages=[...(v.system?[{role:"system",content:v.system}]:[]),{role:"user",content:v.prompt}];optional(params,"max_tokens",number(v.max_tokens));}else{operation="responses.create";params.input=[...(v.system?[{role:"system",content:v.system}]:[]),{role:"user",content:[{type:"input_text",text:v.prompt},...lines(v.file_ids).map(id=>({type:"input_file",file_id:id}))]}];if(tools.length)params.tools=tools;optional(params,"max_output_tokens",number(v.max_tokens));}optional(params,"temperature",number(v.temperature));optional(params,"service_tier",v.tier);optional(params,"reasoning_effort",v.reasoning);
  }else if(page==="image"||page==="image-edit"){operation=page==="image"?"images.generate":"images.edit";params.model=v.model;params.prompt=v.prompt;optional(params,"aspect_ratio",v.aspect_ratio);optional(params,"resolution",v.resolution);optional(params,"n",number(v.n));}
  else if(page==="speech"){operation="audio.speech";params.text=v.prompt;params.voice_id=v.voice;optional(params,"language",v.language);}
  else if(page==="transcribe"){operation="audio.transcribe";optional(params,"language",v.language);}
  else {operation="videos.create";params.model=v.model;params.prompt=v.prompt;optional(params,"aspect_ratio",v.aspect_ratio);optional(params,"duration",number(v.duration));}
  return {operation,params:nativeExtras(params,v.extra)};
 }
 const builders={openai:openaiRequest,compatible:openaiRequest,anthropic:anthropicRequest,gemini:geminiRequest,xai:xaiRequest};
 W.workspace=async()=>{
  const {vendor,feature:page,profile}=W;const chat=page==="chat";
  const draftKey=`wb-draft-${profile.id}-${page}`;let draft={};try{draft=JSON.parse(sessionStorage.getItem(draftKey)||"{}");}catch{}
  const requestedProtocol={"chat.create":"chat","responses.create":"responses"}[new URLSearchParams(location.search).get("operation")];
  const controls={};const defaultModel={openai:{image:"gpt-image-1", "image-edit":"gpt-image-1",speech:"gpt-4o-mini-tts",transcribe:"gpt-4o-transcribe",translate:"whisper-1"},gemini:{image:"gemini-2.5-flash-image","image-edit":"gemini-2.5-flash-image",speech:"gemini-2.5-flash-preview-tts"},xai:{image:"grok-imagine-image","image-edit":"grok-imagine-image",video:"grok-imagine-video"}};
  const model=input("model",draft.model||defaultModel[vendor]?.[page]||profile.models?.[0]||"");model.setAttribute("list","model-catalog");model.required=!(vendor==="xai"&&["speech","transcribe"].includes(page));controls.model=model;
  const catalogKey="wb-models-"+JSON.stringify([profile.id,profile.base_url,W.config.revision]);
  let modelIDs=[...(profile.models||[])];
  try{const saved=JSON.parse(sessionStorage.getItem(catalogKey)||"null");if(Array.isArray(saved))modelIDs=[...new Set([...modelIDs,...saved.filter(id=>typeof id==="string")])];}catch{}
  const catalog=el("datalist",{id:"model-catalog"});
  const updateCatalog=()=>catalog.replaceChildren(...modelIDs.map(id=>el("option",{value:id})));
  updateCatalog();
  const discover=button("发现模型",async()=>{
    const modal=W.dialog("选择模型"),search=input("model_search","","search");
    search.placeholder="按模型名称搜索";search.setAttribute("autocomplete","off");
    const list=el("div",{class:"model-options"}),info=el("div",{}),controller=new AbortController();
    const render=()=>{
      const shown=modelIDs.filter(id=>id.toLowerCase().includes(search.value.trim().toLowerCase()));
      list.replaceChildren(...shown.map(id=>button(id,()=>{model.value=id;model.dispatchEvent(new Event("input",{bubbles:true}));modal.close();},"model-option secondary")));
      if(!shown.length)list.append(W.notice(modelIDs.length?"没有匹配的模型，仍可在页面手动输入模型名。":"还没有模型，可刷新目录或在页面手动输入。"));
    };
    let loading=false;
    const refresh=button("刷新模型",async()=>{
      if(loading)return;loading=true;refresh.disabled=true;info.replaceChildren(W.notice("正在读取当前 profile 的模型目录…"));
      try{
        const result=await W.native("models.list",{}, {},false,controller.signal);
        const rows=result.data||result.models;
        if(!Array.isArray(rows))throw new Error("服务商返回的模型目录格式无法识别");
        modelIDs=[...new Set(rows.map(item=>typeof item==="string"?item:item.id||item.name?.replace(/^models\//,"")).filter(id=>typeof id==="string"&&id.trim()))];
        updateCatalog();try{sessionStorage.setItem(catalogKey,JSON.stringify(modelIDs));}catch{}
        info.replaceChildren(W.notice(`读取到 ${modelIDs.length} 个模型，点击模型名称即可使用。`));render();
      }catch(error){if(error.name!=="AbortError")info.replaceChildren(W.notice(error.message,true));}
      finally{loading=false;refresh.disabled=false;}
    });
    search.addEventListener("input",render);
    modal.dialog.addEventListener("close",()=>controller.abort(),{once:true});
    modal.body.replaceChildren(el("div",{class:"model-search"},field("搜索模型",search),refresh),info,list);
    render();search.focus();
  });
  const actionLabels={chat:"发送",image:"生成图片","image-edit":"编辑图片",speech:"合成语音",transcribe:"开始转写",translate:"翻译音频",video:"提交视频任务"};
  const promptLabels={chat:"消息",speech:"要朗读的文本",transcribe:"转写提示（可选）",translate:"翻译提示（可选）",video:"视频描述"};
  const prompt=el("textarea",{name:"prompt",rows:chat?3:6,placeholder:chat?"输入消息，Ctrl / ⌘ + Enter 发送":"输入本次任务的内容…"},draft.prompt||"");
  prompt.required=!["transcribe","translate"].includes(page);controls.prompt=prompt;
  const settingsFields=descriptors(vendor,page).map(([name,label,type,value,choices])=>{
    let control;
    if(type==="select")control=select(name,choices,name==="protocol"?(requestedProtocol||draft[name]||profile.protocol):draft[name]??value);
    else if(type==="textarea")control=el("textarea",{name,rows:2},draft[name]??value);
    else control=input(name,draft[name]??value,type);
    if(type==="checkbox")control.checked=draft[name]??value;
    if(type==="number"){control.step=name==="temperature"?"any":"1";control.min=["thinking","thinkingBudget"].includes(name)?"0":"1";if(name==="temperature")control.min="0";}
    if(name==="max_tokens"&&vendor==="anthropic")control.required=true;
    if(name==="tier"&&type==="text"){control.setAttribute("list","service-tiers");control.placeholder="留空使用服务商默认值";}
    controls[name]=control;
    return {name,node:field(label,control)};
  });
  const extras=el("textarea",{name:"extra",rows:5,spellcheck:"false"},draft.extra||"{}");controls.extra=extras;
  const chosen={};
  const attachmentKey=chat?"attachment":page==="image-edit"?"image":"file";
  if(chat||["image-edit","transcribe","translate"].includes(page))chosen[attachmentKey]=W.assetControl({
    label:chat?"选择图片":page==="image-edit"?"选择原始图片":"选择音频",accept:chat||page==="image-edit"?"image/":"audio/",multiple:chat||page==="image-edit",selected:draft.asset_selection?.[attachmentKey]||[],onChange:invalidate});
  if(page==="image-edit"&&["openai","compatible"].includes(vendor))chosen.mask=W.assetControl({label:"选择蒙版",accept:"image/png",multiple:false,selected:draft.asset_selection?.mask||[],onChange:invalidate});
  const status=el("div",{class:"workspace-status","aria-live":"polite"});
  const result=el("section",{class:"results card","aria-live":"polite"},el("h2",{},"结果"),el("p",{class:"muted"},"完成任务后，结果会显示在这里。"));
  let previewDialog=null;
  const messages=el("div",{id:"messages",class:"messages"});
  const toolNames=new Set(["search","code","image_tool","container","file_ids","mime"]);
  const group=(name,items)=>el("fieldset",{class:"parameter-group"},el("legend",{},name),el("div",{class:"parameter-grid"},items.map(item=>item.node)));
  const general=settingsFields.filter(item=>!toolNames.has(item.name)&&item.name!=="system");
  const tools=settingsFields.filter(item=>toolNames.has(item.name));
  const system=settingsFields.filter(item=>item.name==="system");
  const protocolHelp=el("p",{class:"field-help muted small",hidden:true},"当前使用 Chat Completions，Responses 的工具与文件引用不参与此请求。");
  const configuration=el("details",{class:"card parameters",open:!chat},el("summary",{},"参数设置"),
    general.length?group(chat?"生成参数":"输出设置",general):null,
    system.length?group("系统指令",system):null,
    tools.length?group("工具与附件",tools):null,protocolHelp,
    el("details",{class:"advanced"},el("summary",{},"原生扩展参数"),field("额外 JSON 字段",extras,"使用当前服务商的原生字段；重名字段会提示冲突。")));
  const modelBar=el("div",{class:"model-bar"},field("实际模型",model),discover,catalog,el("datalist",{id:"service-tiers"},["auto","default","flex","priority"].map(value=>el("option",{value}))));
  const send=el("button",{type:"submit"},actionLabels[page]);let controller=null,busy=false;
  const stop=W.action("停止",async()=>{if(currentTask?.id){acceptTask(await W.api(`/api/tasks/${currentTask.id}/cancel`,{method:"POST",body:"{}"}));if(!busy)await refreshTask();}else controller?.abort();},"quiet");stop.disabled=true;stop.hidden=true;
  const previewButton=button("预览请求",()=>perform(true),"secondary");
  const composer=el("section",{class:"card composer stack"},
    field(promptLabels[page]||"提示词 / 输入",prompt),
    ...Object.values(chosen).map(control=>control.node),
    status,el("div",{class:"actions"},send,previewButton,stop,chat?el("small",{class:"muted composer-hint"},"Enter 换行 · Ctrl / ⌘ + Enter 发送"):null));
  const usageSummary=el("div",{class:"session-usage"});
  const conversation=chat?el("section",{class:"conversation","aria-label":"当前对话"},usageSummary,messages,composer):null;
  const form=el("form",{class:"workspace-form "+(chat?"chat-form":"task-form"),novalidate:""},
    vendor==="xai"&&["speech","transcribe"].includes(page)?null:modelBar,
    chat?conversation:composer,configuration,chat?null:result);
  let session=null,parent="",currentTask=null;
  const taskKey=`wb-active-task-${profile.id}-${page}`;
  const running=task=>["queued","running"].includes(task?.status);
  const historyLink=()=>el("a",{href:"/tasks?"+new URLSearchParams({provider_id:profile.id,feature:page}),class:"button quiet"},"生成历史");
  const messageViews=new WeakMap(),collapsedMessages=new Set();
  const sessionsPanel=el("aside",{class:"card session-panel"});
  const sessionStatus=el("div",{class:"small"});let sessionList=[],sessionListLoaded=false;
  const operationForChat=()=>vendor==="anthropic"?"messages.create":vendor==="gemini"?"content.generate":controls.protocol?.value==="chat"?"chat.create":"responses.create";
  const activeKey=()=>`wb-session-${profile.id}-${operationForChat()}`;
  const sessionListKey=()=>`wb-session-list-${profile.id}-${operationForChat()}`;
  const saveSessionList=()=>W.writeCache(sessionListKey(),{rows:sessionList,loaded:sessionListLoaded});
  function updateSessionList(item=session){
    if(item){
      const {id,title,profile_id,operation,updated_at}=item;
      if(profile_id===profile.id&&operation===operationForChat()){
        sessionList=[{id,title,profile_id,operation,updated_at},...sessionList.filter(row=>row.id!==id)].sort((a,b)=>String(b.updated_at).localeCompare(String(a.updated_at)));
        saveSessionList();
      }
    }
    renderSessions();
  }
  const remember=()=>{try{if(session)sessionStorage.setItem(activeKey(),JSON.stringify({id:session.id,parent}));else sessionStorage.removeItem(activeKey());}catch{}};
  function applyProtocol(){
    const incompatible=controls.protocol?.value==="chat";
    for(const name of toolNames)if(controls[name])controls[name].disabled=incompatible||busy;
    protocolHelp.hidden=!incompatible;
  }
  const collect=()=>{
    const values=Object.fromEntries(Object.entries(controls).map(([key,c])=>[key,c.type==="checkbox"?c.checked:c.value]));
    values.model=values.model.trim();values.asset_ids=Object.fromEntries(Object.entries(chosen).map(([key,control])=>[key,control.getIDs()]));return values;
  };
  function invalidate(){
    previewDialog?.close();previewDialog=null;
    const values=collect();delete values.asset_ids;
    values.asset_selection=Object.fromEntries(Object.entries(chosen).map(([key,control])=>[key,control.getAssets()]));
    try{sessionStorage.setItem(draftKey,JSON.stringify(values));}catch{}
  }
  function pathMessages(){
    const byID=new Map(session.messages.map(message=>[message.id,message])),path=[],seen=new Set();
    for(let id=parent;id&&byID.has(id)&&!seen.has(id);){seen.add(id);const message=byID.get(id);path.unshift(message);id=message.parent_id;}
    return path;
  }
  const statuses={complete:"已完成",pending:"生成中",error:"失败",cancelled:"已停止",interrupted:"已中断"};
  function renderMessages(followEnd=false){
    const scrollTop=messages.scrollTop,atEnd=followEnd||messages.scrollHeight-messages.scrollTop-messages.clientHeight<48;
    messages.replaceChildren();
    usageSummary.replaceChildren();
    if(session?.messages.length)usageSummary.append(W.usageSummary(session.usage,"会话全部分支 · 已知累计"),el("div",{class:"small muted"},"当前分支 · "+["input","output","total"].map((k,i)=>["输入","输出","合计"][i]+" "+(W.sumUsage(pathMessages()).tokens[k]?.toLocaleString("zh-CN")??"未知")).join(" · ")));
    if(!session?.messages.length){messages.append(el("div",{class:"empty"},el("strong",{},"开始一个新对话"),el("p",{},"选择模型，输入消息；需要时再调整参数。")));return;}
    const parents=new Set(session.messages.map(message=>message.parent_id));
    const leaves=session.messages.filter(message=>!parents.has(message.id));
    if(leaves.length>1){
      const options=leaves.map((message,index)=>[message.id,`分支 ${index+1} · ${message.text.slice(0,40)||statuses[message.status]}`]);
      if(!leaves.some(message=>message.id===parent))options.unshift([parent,"当前续接点"]);
      const choices=select("branch",options,parent);
      choices.disabled=busy;choices.addEventListener("change",()=>{parent=choices.value;remember();invalidate();renderMessages();});
      messages.append(field("查看分支",choices));
    }
    for(const message of pathMessages()){
      let view=messageViews.get(message);
      if(!view){
        const content=message.output?W.result(message.output,{lazyMedia:true,skipMedia:!!message.asset_ids?.length}):message.asset_ids?.length?el("div",{class:"result-content"}):null;
        if(message.asset_ids?.length)content.append(W.assetResults(message.asset_ids,{lazyMedia:true}));
        const native=content?.querySelector(":scope > .native-details"),resultCopy=content?.querySelector(":scope > .copy-result");
        native?.remove();resultCopy?.remove();
        if(message.text)content?.querySelector(":scope > .message-text")?.remove();
        const continuation=el("span",{class:"continuation-label"},"当前续接点"),key=session.id+":"+message.id;
        const body=el("div",{class:"message-body",id:"message-body-"+message.id},
          message.text?el("div",{class:"message-text"},message.text):null,
          message.error?W.notice(message.error,true):null,
          content);
        const mediaItems=[...(content?.querySelectorAll(".media-preview")||[])];
        const mediaLabel=[...new Set(mediaItems.map(item=>item.dataset.mediaKind))].join("、");
        const excerpt=(message.text||content?.querySelector(".message-text")?.textContent||message.error||"原生结果").replace(/\s+/g," ").slice(0,120);
        const collapsed=el("div",{class:"message-collapsed"},el("p",{},excerpt),mediaItems.length?W.mediaPlaceholder(`${mediaLabel} · ${mediaItems.length}`):null);
        const fold=button("收起消息",()=>{if(collapsedMessages.has(key))collapsedMessages.delete(key);else collapsedMessages.add(key);setCollapsed();},"quiet message-toggle");
        fold.setAttribute("aria-controls",body.id);
        function setCollapsed(){const hidden=collapsedMessages.has(key);body.hidden=hidden;collapsed.hidden=!hidden;fold.textContent=hidden?"展开消息":"收起消息";fold.setAttribute("aria-expanded",String(!hidden));}
        setCollapsed();
        const node=el("article",{class:`message ${message.role}`,"data-message-id":message.id,"data-status":message.status},
          el("header",{class:"message-heading"},el("strong",{},message.role==="user"?"你":"模型"),message.status!=="complete"?el("span",{class:"badge"},statuses[message.status]||message.status):null,continuation,fold),collapsed,body);
        const branch=button("从这里继续",()=>{if(busy)return;parent=message.id;remember();invalidate();renderMessages();prompt.focus();},"quiet");
        const fork=W.action("复制为新会话",async()=>{
          if(busy)return;busy=true;const unlock=W.lock(form),unlockList=W.lock(sessionsPanel);
          try{
            session=await W.api(`/api/sessions/${session.id}/fork`,{method:"POST",body:JSON.stringify({node_id:message.id})});
            parent=session.head_id;remember();invalidate();renderMessages();updateSessionList();
          }finally{busy=false;unlock();unlockList();applyProtocol();renderMessages();renderSessions();}
        },"quiet",status);
        if(message.role==="assistant")body.append(W.usageLine(message.usage));
        body.append(el("div",{class:"message-meta"},message.text?W.copyButton(message.text):resultCopy,branch,fork,native));
        view={node,continuation,branch,fork,fold};messageViews.set(message,view);
      }
      view.continuation.hidden=message.id!==parent;
      view.branch.disabled=busy||running(currentTask);view.fork.disabled=busy||running(currentTask);view.fold.disabled=false;
      messages.append(view.node);
    }
    messages.scrollTop=atEnd?messages.scrollHeight:scrollTop;
  }
  async function openSession(id,selectedParent){
    if(busy)return;busy=true;const unlock=W.lock(form),unlockList=W.lock(sessionsPanel);
    let loaded=false;messages.setAttribute("aria-busy","true");status.replaceChildren(W.notice("正在载入会话…"));
    try{
      const data=await W.api(`/api/sessions/${id}`);
      if(data.profile_id!==profile.id||data.operation!==operationForChat())throw new Error("会话归属不匹配，请重新选择。");
      session=data;parent=selectedParent&&data.messages.some(message=>message.id===selectedParent)?selectedParent:data.head_id;
      const pending=data.messages.find(message=>message.status==="pending"&&message.task_id);
      currentTask=pending?{id:pending.task_id,status:"running",session_id:data.id}:null;
      remember();invalidate();loaded=true;status.replaceChildren();
    }catch(error){status.replaceChildren(W.notice(error.message,true));}
    finally{busy=false;unlock();unlockList();applyProtocol();messages.removeAttribute("aria-busy");renderMessages(loaded);}
    updateSessionList();renderTaskStatus();
  }
  function renameSession(item){
    const modal=W.dialog("重命名会话"),name=input("session_title",item.title);name.required=true;name.maxLength=120;
    const feedback=el("div",{});
    const editor=el("form",{class:"stack"},field("会话名称",name),feedback,el("div",{class:"actions"},el("button",{type:"submit"},"保存名称")));
    editor.addEventListener("submit",async event=>{
      event.preventDefault();if(!W.validate(editor))return;const unlock=W.lock(editor);modal.setBusy(true);
      try{const updated=await W.api(`/api/sessions/${item.id}`,{method:"PATCH",body:JSON.stringify({title:name.value.trim()})});if(session?.id===item.id)session.title=updated.title;modal.close();updateSessionList(updated);}
      catch(error){feedback.replaceChildren(W.notice(error.message,true));}
      finally{unlock();modal.setBusy(false);}
    });modal.body.append(editor);name.focus();name.select();
  }
  async function refreshSessions(){
    if(busy)return;busy=true;const unlock=W.lock(form),unlockList=W.lock(sessionsPanel);sessionStatus.replaceChildren(W.notice("正在读取会话列表…"));
    try{
      sessionList=(await W.api(`/api/sessions?profile_id=${encodeURIComponent(profile.id)}`)).filter(item=>item.operation===operationForChat());
      sessionListLoaded=true;saveSessionList();sessionStatus.replaceChildren();
    }catch(error){sessionStatus.replaceChildren(W.notice(error.message,true));}
    finally{busy=false;unlock();unlockList();applyProtocol();renderSessions();}
  }
  function renderSessions(){
    const fresh=button("新建会话",()=>{if(busy)return;session=null;parent="";currentTask=null;status.replaceChildren();renderTaskStatus();remember();invalidate();renderMessages();renderSessions();prompt.focus();},"quiet");fresh.disabled=busy;
    const refresh=button("刷新",refreshSessions,"quiet");refresh.setAttribute("aria-label","刷新会话");refresh.disabled=busy;
    sessionsPanel.replaceChildren(el("div",{class:"section-head"},el("h2",{},"会话"),el("div",{class:"actions"},refresh,fresh)),sessionStatus,
      ...(!sessionList.length?[el("p",{class:"small muted"},sessionListLoaded?"这里还没有会话，发送第一条消息后会自动保存。":"尚未加载会话列表，点击「刷新会话」读取。")]:[]),
      el("div",{class:"session-list"},sessionList.map(item=>{
        const open=button(item.title,()=>openSession(item.id),"quiet");open.disabled=busy;
        const manage=W.action("管理",async()=>{if(busy)return;const modal=W.dialog(item.title);modal.body.append(el("div",{class:"actions"},
          button("重命名",()=>{modal.close();renameSession(item);}),
          button("删除会话",()=>{modal.close();W.confirm(`删除会话「${item.title}」？`,async()=>{
            await W.api(`/api/sessions/${item.id}`,{method:"DELETE"});
            sessionList=sessionList.filter(row=>row.id!==item.id);saveSessionList();
            if(session?.id===item.id){session=null;parent="";remember();renderMessages();}renderSessions();
          },"只删除本地会话，不影响云端资源。");},"danger")));},"quiet",status);manage.disabled=busy;
        return el("div",{class:`session-item ${session?.id===item.id?"active":""}`},open,manage);
      })));
  }
  form.addEventListener("input",invalidate);form.addEventListener("change",invalidate);
  controls.protocol?.addEventListener("change",async()=>{session=null;parent="";currentTask=null;status.replaceChildren();renderTaskStatus();applyProtocol();renderMessages();await restoreSession();});
  let sessionQueryConsumed=false;
  async function restoreSession(){
    const cached=W.readCache(sessionListKey());sessionList=Array.isArray(cached?.rows)?cached.rows:[];sessionListLoaded=cached?.loaded===true;sessionStatus.replaceChildren();renderSessions();
    let active;try{active=JSON.parse(sessionStorage.getItem(activeKey())||"null");}catch{}
    const requested=!sessionQueryConsumed&&new URLSearchParams(location.search).get("session");sessionQueryConsumed=true;
    if(requested||active?.id)await openSession(requested||active.id,requested?undefined:active.parent);
  }
  function acceptTask(task,pendingSession){
    currentTask=task;W.rememberTasks([task]);
    if(pendingSession){session=pendingSession;parent=session.head_id;remember();updateSessionList();}
    if(!chat)W.writeCache(taskKey,{id:task.id});
    stop.disabled=!running(task);stop.hidden=!running(task);
  }
  function renderTaskStatus(){
    send.disabled=busy||running(currentTask);previewButton.disabled=busy||running(currentTask);
    stop.hidden=!running(currentTask);stop.disabled=!running(currentTask);
    if(running(currentTask))status.replaceChildren(W.notice("正在后台生成中"),el("div",{class:"actions"},
      W.action("刷新状态",refreshTask,"quiet",status),historyLink()),el("small",{class:"muted"},"可以离开此页。再次打开只查看状态，完成后读取结果。"));
  }
  async function refreshTask(){
    if(!currentTask)return;
    const taskID=currentTask.id,sessionID=session?.id;
    const stillSelected=()=>currentTask?.id===taskID&&session?.id===sessionID;
    const followEnd=messages.scrollHeight-messages.scrollTop-messages.clientHeight<48;
    let task,updatedSession;
    try{
      task=await W.api(`/api/tasks/${taskID}`);
      if(!stillSelected())return;
      if(chat&&sessionID)updatedSession=await W.api(`/api/sessions/${sessionID}`);
    }catch(error){if(stillSelected())throw error;return;}
    if(!stillSelected())return;
    acceptTask(task);
    if(chat&&session){
      session=updatedSession;parent=session.head_id;remember();updateSessionList();
    }else if(!running(task)){
      result.replaceChildren(el("h2",{},"结果"),W.taskResult(task));
      if(page==="video"){const data=task.result||{},id=data.request_id||data.name||data.id;if(id)videoTask(input("video_id",id));}
    }
    if(!running(task))status.replaceChildren(W.notice(task.status==="complete"?"已完成，结果已保存。":task.error||"任务已停止。",task.status!=="complete"));
    renderTaskStatus();if(chat)renderMessages(followEnd);return task;
  }
  async function waitForTask(){
    // Only the page that submitted a non-streaming task watches its detail.
    // Restored pages use the explicit status button and never reattach events.
    if(!running(currentTask)&&!leaving)await refreshTask();
    while(running(currentTask)&&!leaving){await new Promise(resolve=>setTimeout(resolve,500));if(!leaving)await refreshTask();}
  }
  let leaving=false;
  window.addEventListener("pagehide",()=>{leaving=true;controller?.abort();});
  async function perform(isPreview){
    if(busy||running(currentTask)||!W.validate(form))return;
    const trigger=document.activeElement,values=collect();
    if(["image-edit","transcribe","translate"].includes(page)&&!values.asset_ids[attachmentKey]?.length){status.replaceChildren(W.notice(page==="image-edit"?"请从精选资产中选择原始图片。":"请从精选资产中选择音频。",true));return;}
    if(controls.protocol?.value==="chat")for(const name of toolNames)values[name]=["search","code","image_tool"].includes(name)?false:"";
    busy=true;const unlock=W.lock(form),unlockList=W.lock(sessionsPanel);
    controller=new AbortController();stop.disabled=true;stop.hidden=true;
    if(!isPreview)currentTask=null;
    send.textContent=isPreview?actionLabels[page]:"处理中…";
    status.replaceChildren(W.notice(isPreview?"正在构建请求预览…":"正在提交后台任务…"));
    if(!chat&&!isPreview)result.setAttribute("aria-busy","true");
    try{
      const request=await builders[vendor](page,values);let response;
      if(chat){
        const payload={provider_id:profile.id,operation:request.operation,params:request.params,asset_ids:values.asset_ids,parent_id:parent,expected_head:session?.head_id||"",revision:W.config.revision,text:values.prompt,stream:values.stream,background:!isPreview};
        if(!isPreview&&!session)session=await W.api("/api/sessions",{method:"POST",body:JSON.stringify({profile_id:profile.id,operation:request.operation,title:values.prompt.slice(0,60)||"新会话"})});
        let liveText;
        if(!isPreview){
          remember();renderMessages();messages.querySelector(".empty")?.remove();
          messages.append(el("article",{class:"message user"},el("header",{class:"message-heading"},el("strong",{},"你")),el("div",{class:"message-text"},values.prompt)));
          liveText=el("div",{class:"message-text","data-live-text":""},"等待模型响应…");
          messages.append(el("article",{class:"message assistant"},el("header",{class:"message-heading"},el("strong",{},"模型"),el("span",{class:"badge"},"生成中")),liveText));liveText.dataset.waiting="true";
          messages.scrollTop=messages.scrollHeight;
        }
        const onEvent=event=>{
          if(event.type==="done"&&event.task){acceptTask(event.task);return;}
          if(event.type==="started"){acceptTask(event.task,event.session);status.replaceChildren(W.notice("正在生成，可以离开此页，任务会继续。"));return;}
          const e=event.event||{},data=e.data||{};let text="";
          if(e.type==="response.output_text.delta")text=data.delta||"";
          else if(e.type==="content_block_delta")text=data.delta?.text||"";
          else if(data.choices)text=data.choices.map(choice=>choice.delta?.content||"").join("");
          else if(data.candidates)text=data.candidates.flatMap(candidate=>candidate.content?.parts||[]).filter(part=>!part.thought).map(part=>part.text||"").join("");
          if(!text||!liveText)return;
          const atEnd=messages.scrollHeight-messages.scrollTop-messages.clientHeight<48;
          if(liveText.dataset.waiting){liveText.textContent="";delete liveText.dataset.waiting;}
          liveText.textContent+=text;if(atEnd)messages.scrollTop=messages.scrollHeight;
        };
        response=await W.api(`/api/sessions/${session?.id||"new"}/native${isPreview?"?preview=1":""}`,{method:"POST",body:JSON.stringify(payload),signal:controller.signal,onEvent});
      }else response=await W.native(request.operation,request.params,request.uploads,isPreview,controller.signal,{assets:values.asset_ids,background:!isPreview,feature:page});
      status.replaceChildren();
      if(isPreview)previewDialog=W.showPreview(response,trigger);
      else{
        if(response.task){acceptTask(response.task,response.session);renderTaskStatus();await waitForTask();}
        else if(chat){session=response;parent=session.head_id;remember();if(currentTask)await refreshTask();}
        else result.replaceChildren(el("h2",{},"结果"),W.result(response));
        if(chat){
          const last=session.messages.at(-1);
          if(last?.status==="complete"){prompt.value="";for(const control of Object.values(chosen))control.setAssets([]);}
          else status.replaceChildren(W.notice(last?.error||"请求未完成，输入已保留。",true));
          invalidate();renderMessages();updateSessionList();
        }else result.scrollIntoView({block:"nearest"});
      }
    }catch(error){
      if(!leaving){
        if(currentTask){
          try{await refreshTask();}catch(reload){status.replaceChildren(W.notice(reload.message,true));}
          if(running(currentTask))renderTaskStatus();
          else status.replaceChildren(W.notice(error.message,true));
        }else status.replaceChildren(W.notice(error.name==="AbortError"?"连接已断开。已提交的生成可在后台任务中查看。":error.message,true));
      }
    }finally{
      busy=false;unlock();unlockList();controller=null;send.textContent=actionLabels[page];
      result.removeAttribute("aria-busy");applyProtocol();renderTaskStatus();if(chat){renderMessages();renderSessions();}
    }
  }
  form.addEventListener("submit",event=>{event.preventDefault();perform(false);});
  prompt.addEventListener("keydown",event=>{if(chat&&!event.isComposing&&event.key==="Enter"&&(event.ctrlKey||event.metaKey)){event.preventDefault();perform(false);}});
  const heading=W.heading(labels[page],`${W.vendors[vendor].name} / ${profile.name}`,chat?null:historyLink());if(chat)heading.classList.add("chat-heading");
  W.root.replaceChildren(heading,chat?el("div",{class:"chat-layout"},sessionsPanel,form):form);
  function videoTask(id){
    W.root.querySelector("#video-task")?.remove();
    const taskStatus=el("div",{});id.required=true;
    const query=el("form",{id:"video-task",class:"card stack"},el("h2",{},"查询视频任务"),field(vendor==="gemini"?"Operation name":"Request ID",id),taskStatus,el("div",{class:"actions"},el("button",{type:"submit"},"查询状态")));
    query.addEventListener("submit",async event=>{
      event.preventDefault();if(!W.validate(query))return;const unlock=W.lock(query);taskStatus.replaceChildren(W.notice("正在查询…"));
      try{const response=await W.native("videos.get",{video_id:id.value.trim()}, {},false,undefined,{background:true,feature:"video"});acceptTask(response.task);renderTaskStatus();await waitForTask();taskStatus.replaceChildren(W.notice("查询结果已保存；上游仍在生成时可稍后再次查询。"));}
      catch(error){taskStatus.replaceChildren(W.notice(error.message,true));}finally{unlock();}
    });W.root.append(query);
  }
  applyProtocol();
  if(page==="video")videoTask(input("video_id"));
  if(chat){renderMessages();await restoreSession();}
  else {const saved=W.readCache(taskKey);if(saved?.id){currentTask={id:saved.id,status:"running"};try{await refreshTask();}catch(error){currentTask=null;W.writeCache(taskKey,null);status.replaceChildren(W.notice(error.message,true));renderTaskStatus();}}}
 };
})();
