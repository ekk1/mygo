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
 const fileData=async file=>({data:await new Promise((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(r.result.split(",")[1]);r.onerror=()=>reject(r.error);r.readAsDataURL(file);}),mimeType:file.type||"application/octet-stream"});
 function openaiRequest(page,v){const params={model:v.model};let operation,uploads={};
   if(page==="chat"){
    const tools=[];if(v.search)tools.push({type:"web_search"});if(v.image_tool)tools.push({type:"image_generation"});if(v.code)tools.push({type:"code_interpreter",container:v.container||{type:"auto"}});
    if(v.protocol==="chat"){if(tools.length||lines(v.file_ids).length)throw new Error("这些工具和文件 ID 用于 Responses，请切换协议或在扩展参数中提供 Chat 原生结构");operation="chat.create";params.messages=[...(v.system?[{role:"system",content:v.system}]:[]),{role:"user",content:v.prompt}];optional(params,"max_completion_tokens",number(v.max_tokens));optional(params,"reasoning_effort",v.reasoning);}
    else {operation="responses.create";params.input=[{role:"user",content:[{type:"input_text",text:v.prompt},...lines(v.file_ids).map(id=>({type:"input_file",file_id:id}))]}];optional(params,"instructions",v.system);optional(params,"max_output_tokens",number(v.max_tokens));if(v.reasoning)params.reasoning={effort:v.reasoning};if(tools.length)params.tools=tools;}
    optional(params,"service_tier",v.tier);optional(params,"temperature",number(v.temperature));
   }else if(page==="image"||page==="image-edit"){operation=page==="image"?"images.generate":"images.edit";params.prompt=v.prompt;for(const key of ["size","quality","output_format"])optional(params,key,v[key]);optional(params,"n",number(v.n));if(page==="image-edit")uploads={image:v.files,mask:v.mask};}
   else if(page==="speech"){operation="audio.speech";params.input=v.prompt;params.voice=v.voice;params.response_format=v.response_format;optional(params,"instructions",v.instructions);}
   else {operation=page==="translate"?"audio.translate":"audio.transcribe";optional(params,"language",v.language);optional(params,"response_format",v.response_format);if(v.prompt)params.prompt=v.prompt;uploads={file:v.files};}
   return {operation,params:nativeExtras(params,v.extra),uploads};
 }
 function anthropicRequest(page,v){const params={model:v.model,max_tokens:number(v.max_tokens),messages:[{role:"user",content:[{type:"text",text:v.prompt},...lines(v.file_ids).map(id=>({type:"document",source:{type:"file",file_id:id}}))]}]};optional(params,"system",v.system);optional(params,"temperature",number(v.temperature));optional(params,"service_tier",v.tier);if(v.thinking)params.thinking={type:"enabled",budget_tokens:Number(v.thinking)};const tools=[];if(v.search)tools.push({type:"web_search_20250305",name:"web_search"});if(v.code)tools.push({type:"code_execution_20250825",name:"code_execution"});if(tools.length)params.tools=tools;return {operation:"messages.create",params:nativeExtras(params,v.extra)};}
 async function geminiRequest(page,v){if(page==="video"){const params={model:v.model,instances:[{prompt:v.prompt}],parameters:{aspectRatio:v.aspect_ratio}};if(v.duration)params.parameters.durationSeconds=Number(v.duration);return {operation:"videos.create",params:nativeExtras(params,v.extra)};}
  const parts=[{text:v.prompt||"请准确转写这段音频。"}];for(const file of v.files)parts.push({inlineData:await fileData(file)});for(const uri of lines(v.file_ids))parts.push({fileData:{fileUri:uri,mimeType:v.mime}});
  const params={model:v.model,contents:[{role:"user",parts}]};const generation={};if(v.system)params.systemInstruction={parts:[{text:v.system}]};optional(generation,"temperature",number(v.temperature));optional(generation,"maxOutputTokens",number(v.maxOutputTokens));if(v.thinkingBudget!==""&&v.thinkingBudget!==undefined)generation.thinkingConfig={thinkingBudget:Number(v.thinkingBudget)};
  if(page==="image"||page==="image-edit"){generation.responseModalities=["TEXT","IMAGE"];generation.imageConfig={aspectRatio:v.aspectRatio};optional(generation.imageConfig,"imageSize",v.imageSize);}
  if(page==="speech"){generation.responseModalities=["AUDIO"];generation.speechConfig={voiceConfig:{prebuiltVoiceConfig:{voiceName:v.voice}}};}
  if(Object.keys(generation).length)params.generationConfig=generation;optional(params,"serviceTier",v.tier);
  const tools=[];if(v.search)tools.push({googleSearch:{}});if(v.code)tools.push({codeExecution:{}});if(tools.length)params.tools=tools;
  return {operation:"content.generate",params:nativeExtras(params,v.extra)};
 }
 async function xaiRequest(page,v){const params={};let operation,uploads;
  if(page==="chat"){params.model=v.model;const tools=[];if(v.search)tools.push({type:"web_search"},{type:"x_search"});if(v.code)tools.push({type:"code_interpreter"});if(v.protocol==="chat"){if(tools.length||lines(v.file_ids).length)throw new Error("Grok 服务端工具及文件在 Responses 页面使用");operation="chat.create";params.messages=[...(v.system?[{role:"system",content:v.system}]:[]),{role:"user",content:v.prompt}];optional(params,"max_tokens",number(v.max_tokens));}else{operation="responses.create";params.input=[...(v.system?[{role:"system",content:v.system}]:[]),{role:"user",content:[{type:"input_text",text:v.prompt},...lines(v.file_ids).map(id=>({type:"input_file",file_id:id}))]}];if(tools.length)params.tools=tools;optional(params,"max_output_tokens",number(v.max_tokens));}optional(params,"temperature",number(v.temperature));optional(params,"service_tier",v.tier);optional(params,"reasoning_effort",v.reasoning);
  }else if(page==="image"||page==="image-edit"){operation=page==="image"?"images.generate":"images.edit";params.model=v.model;params.prompt=v.prompt;optional(params,"aspect_ratio",v.aspect_ratio);optional(params,"resolution",v.resolution);optional(params,"n",number(v.n));if(page==="image-edit"){const files=await Promise.all(v.files.map(fileData));if(files.length===1)params.image={url:`data:${files[0].mimeType};base64,${files[0].data}`};else params.images=files.map(f=>({url:`data:${f.mimeType};base64,${f.data}`}));}}
  else if(page==="speech"){operation="audio.speech";params.text=v.prompt;params.voice_id=v.voice;optional(params,"language",v.language);}
  else if(page==="transcribe"){operation="audio.transcribe";uploads={file:v.files};optional(params,"language",v.language);}
  else {operation="videos.create";params.model=v.model;params.prompt=v.prompt;optional(params,"aspect_ratio",v.aspect_ratio);optional(params,"duration",number(v.duration));}
  return {operation,params:nativeExtras(params,v.extra),uploads};
 }
 const builders={openai:openaiRequest,compatible:openaiRequest,anthropic:anthropicRequest,gemini:geminiRequest,xai:xaiRequest};
 W.workspace=async()=>{
  const {vendor,feature:page,profile}=W;const chat=page==="chat";
  const draftKey=`wb-draft-${profile.id}-${page}`;let draft={};try{draft=JSON.parse(sessionStorage.getItem(draftKey)||"{}");}catch{}
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
  const prompt=el("textarea",{name:"prompt",rows:chat?4:6,placeholder:chat?"输入消息，Ctrl / ⌘ + Enter 发送":"输入本次任务的内容…"},draft.prompt||"");
  prompt.required=!["transcribe","translate"].includes(page);controls.prompt=prompt;
  const settingsFields=descriptors(vendor,page).map(([name,label,type,value,choices])=>{
    let control;
    if(type==="select")control=select(name,choices,draft[name]??(name==="protocol"?profile.protocol:value));
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
  const files=el("input",{type:"file",name:"files",multiple:page==="image-edit",accept:page==="image-edit"?"image/*":"audio/*",required:["image-edit","transcribe","translate"].includes(page)});
  const mask=el("input",{type:"file",name:"mask",accept:"image/png"});
  const status=el("div",{class:"workspace-status","aria-live":"polite"});
  const result=el("section",{class:"results card","aria-live":"polite"},el("h2",{},"结果"),el("p",{class:"muted"},"完成任务后，结果会显示在这里。"));
  const preview=el("div",{class:"preview-slot"});
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
  const stop=button("停止",()=>controller?.abort(),"quiet");stop.disabled=true;
  const previewButton=button("预览请求",()=>perform(true),"secondary");
  const attachments=el("div",{class:"attachment-list"});
  function updateAttachments(){
    attachments.replaceChildren(...[...files.files].map(file=>el("span",{class:"badge"},file.name)));
    if(files.files.length)attachments.append(button("清除附件",()=>{files.value="";mask.value="";updateAttachments();invalidate();},"quiet"));
  }
  files.addEventListener("change",updateAttachments);
  const composer=el("section",{class:"card composer stack"},
    field(promptLabels[page]||"提示词 / 输入",prompt),
    ["image-edit","transcribe","translate"].includes(page)?field(page==="image-edit"?"原始图片":"音频文件",files):null,
    attachments,
    page==="image-edit"&&["openai","compatible"].includes(vendor)?field("蒙版（可选）",mask):null,
    status,el("div",{class:"actions"},send,previewButton,stop),chat?el("small",{class:"muted"},"Enter 换行 · Ctrl / ⌘ + Enter 发送"):null);
  const form=el("form",{class:"workspace-form "+(chat?"chat-form":"task-form"),novalidate:""},
    vendor==="xai"&&["speech","transcribe"].includes(page)?null:modelBar,
    chat?messages:null,composer,configuration,preview,chat?null:result);
  let session=null,parent="";
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
    values.model=values.model.trim();values.files=[...files.files];values.mask=[...mask.files];return values;
  };
  function invalidate(){
    if(preview.childNodes.length)preview.replaceChildren(W.notice("输入或参数已变化，请重新预览。"));
    const values=collect();delete values.files;delete values.mask;
    try{sessionStorage.setItem(draftKey,JSON.stringify(values));}catch{}
  }
  function pathMessages(){
    const byID=new Map(session.messages.map(message=>[message.id,message])),path=[],seen=new Set();
    for(let id=parent;id&&byID.has(id)&&!seen.has(id);){seen.add(id);const message=byID.get(id);path.unshift(message);id=message.parent_id;}
    return path;
  }
  const statuses={complete:"已完成",pending:"生成中",error:"失败",cancelled:"已停止"};
  function renderMessages(followEnd=false){
    const scrollTop=messages.scrollTop,atEnd=followEnd||messages.scrollHeight-messages.scrollTop-messages.clientHeight<48;
    messages.replaceChildren();
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
      const node=el("details",{class:`message ${message.role}`,open:true,"data-message-id":message.id,"data-status":message.status},
        el("summary",{},message.role==="user"?"你":"模型",el("span",{class:"badge"},statuses[message.status]||message.status),message.id===parent?el("span",{class:"badge good"},"从这里续接"):null),
        message.text?el("div",{class:"message-text"},message.text):null,
        message.error?W.notice(message.error,true):null,
        message.output?W.result(message.output):null);
      const branch=button("从这里继续",()=>{if(busy)return;parent=message.id;remember();invalidate();renderMessages();prompt.focus();},"quiet");
      const fork=W.action("复制为新会话",async()=>{
        if(busy)return;busy=true;const unlock=W.lock(form),unlockList=W.lock(sessionsPanel);
        try{
          session=await W.api(`/api/sessions/${session.id}/fork`,{method:"POST",body:JSON.stringify({node_id:message.id})});
          parent=session.head_id;remember();invalidate();renderMessages();updateSessionList();
        }finally{busy=false;unlock();unlockList();applyProtocol();renderMessages();renderSessions();}
      },"quiet",status);
      branch.disabled=busy;fork.disabled=busy;
      node.append(el("div",{class:"message-meta"},message.text?W.copyButton(message.text):null,branch,fork));messages.append(node);
    }
    messages.scrollTop=atEnd?messages.scrollHeight:scrollTop;
  }
  async function openSession(id,selectedParent){
    if(busy)return;busy=true;const unlock=W.lock(form),unlockList=W.lock(sessionsPanel);
    try{
      const loaded=await W.api(`/api/sessions/${id}`);
      if(loaded.profile_id!==profile.id||loaded.operation!==operationForChat())throw new Error("会话归属不匹配，请重新选择。");
      session=loaded;parent=selectedParent&&loaded.messages.some(message=>message.id===selectedParent)?selectedParent:loaded.head_id;
      remember();invalidate();renderMessages(true);
    }catch(error){W.fail(error,status);}
    finally{busy=false;unlock();unlockList();applyProtocol();renderMessages();}
    updateSessionList();
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
    const fresh=button("新建会话",()=>{if(busy)return;session=null;parent="";remember();invalidate();renderMessages();renderSessions();prompt.focus();},"quiet");fresh.disabled=busy;
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
  controls.protocol?.addEventListener("change",async()=>{session=null;parent="";applyProtocol();renderMessages();await restoreSession();});
  async function restoreSession(){
    const cached=W.readCache(sessionListKey());sessionList=Array.isArray(cached?.rows)?cached.rows:[];sessionListLoaded=cached?.loaded===true;sessionStatus.replaceChildren();renderSessions();
    let active;try{active=JSON.parse(sessionStorage.getItem(activeKey())||"null");}catch{}
    if(active?.id)await openSession(active.id,active.parent);
  }
  async function perform(isPreview){
    if(busy||!W.validate(form))return;
    const values=collect();
    if(controls.protocol?.value==="chat")for(const name of toolNames)values[name]=["search","code","image_tool"].includes(name)?false:"";
    busy=true;const unlock=W.lock(form),unlockList=W.lock(sessionsPanel);
    controller=new AbortController();stop.disabled=false;
    send.textContent=isPreview?actionLabels[page]:"处理中…";
    status.replaceChildren(W.notice(isPreview?"正在构建请求预览…":"请求处理中…"));
    if(!chat&&!isPreview)result.setAttribute("aria-busy","true");
    try{
      const request=await builders[vendor](page,values);let response;
      if(chat){
        const payload={provider_id:profile.id,operation:request.operation,params:request.params,parent_id:parent,expected_head:session?.head_id||"",revision:W.config.revision,text:values.prompt,stream:values.stream};
        if(!isPreview&&!session)session=await W.api("/api/sessions",{method:"POST",body:JSON.stringify({profile_id:profile.id,operation:request.operation,title:values.prompt.slice(0,60)||"新会话"})});
        let liveText;
        if(!isPreview){
          remember();renderMessages();messages.querySelector(".empty")?.remove();
          messages.append(el("details",{class:"message user",open:true},el("summary",{},"你"),el("div",{class:"message-text"},values.prompt)));
          liveText=el("div",{class:"message-text","data-live-text":""},"等待模型响应…");
          messages.append(el("details",{class:"message assistant",open:true},el("summary",{},"模型 · 生成中"),liveText));liveText.dataset.waiting="true";
          messages.scrollTop=messages.scrollHeight;
        }
        const onEvent=event=>{
          const e=event.event||{},data=e.data||{};let text="";
          if(e.type==="response.output_text.delta")text=data.delta||"";
          else if(e.type==="content_block_delta")text=data.delta?.text||"";
          else if(data.choices)text=data.choices.map(choice=>choice.delta?.content||"").join("");
          else if(data.candidates)text=data.candidates.flatMap(candidate=>candidate.content?.parts||[]).map(part=>part.text||"").join("");
          if(!text||!liveText)return;
          const atEnd=messages.scrollHeight-messages.scrollTop-messages.clientHeight<48;
          if(liveText.dataset.waiting){liveText.textContent="";delete liveText.dataset.waiting;}
          liveText.textContent+=text;
          if(atEnd)messages.scrollTop=messages.scrollHeight;
        };
        response=await W.api(`/api/sessions/${session?.id||"new"}/native${isPreview?"?preview=1":""}`,{method:"POST",body:JSON.stringify(payload),signal:controller.signal,onEvent});
      }else response=await W.native(request.operation,request.params,request.uploads,isPreview,controller.signal);
      status.replaceChildren();
      if(isPreview){preview.replaceChildren(W.preview(response));preview.scrollIntoView({block:"nearest"});}
      else if(chat){
        session=response;parent=session.head_id;remember();
        const last=session.messages.at(-1);
        if(last?.status==="complete")prompt.value="";
        else status.replaceChildren(W.notice(last?.error||"请求未完成，输入已保留。",true));
        invalidate();preview.replaceChildren();renderMessages();updateSessionList();
      }else{
        result.replaceChildren(el("h2",{},"结果"),W.result(response));result.removeAttribute("aria-busy");
        if(page==="video"){const id=response.request_id||response.name||response.id;if(id)videoTask(input("video_id",id));}
        status.replaceChildren(W.notice(page==="video"?"任务已提交，可在下方查询进度。":"已完成。"));
        result.scrollIntoView({block:"nearest"});
      }
    }catch(error){
      status.replaceChildren(W.notice(error.name==="AbortError"?"已停止等待，已收到的内容会保留。":error.message,true));
      if(chat&&session){
        try{
          for(let attempt=0;attempt<100;attempt++){
            session=await W.api(`/api/sessions/${session.id}`);
            if(!session.messages.some(message=>message.status==="pending"))break;
            await new Promise(resolve=>setTimeout(resolve,100));
          }
          parent=session.head_id;remember();renderMessages();updateSessionList();
        }catch(reload){W.fail(reload,status);}
      }
    }finally{
      busy=false;unlock();unlockList();stop.disabled=true;controller=null;send.textContent=actionLabels[page];
      result.removeAttribute("aria-busy");applyProtocol();if(chat){renderMessages();renderSessions();}
    }
  }
  form.addEventListener("submit",event=>{event.preventDefault();perform(false);});
  prompt.addEventListener("keydown",event=>{if(chat&&!event.isComposing&&event.key==="Enter"&&(event.ctrlKey||event.metaKey)){event.preventDefault();perform(false);}});
  W.root.replaceChildren(W.heading(labels[page],`${W.vendors[vendor].name} / ${profile.name}`),chat?el("div",{class:"chat-layout"},sessionsPanel,form):form);
  function videoTask(id){
    W.root.querySelector("#video-task")?.remove();
    const taskStatus=el("div",{});id.required=true;
    const query=el("form",{id:"video-task",class:"card stack"},el("h2",{},"查询视频任务"),field(vendor==="gemini"?"Operation name":"Request ID",id),taskStatus,el("div",{class:"actions"},el("button",{type:"submit"},"查询状态")));
    query.addEventListener("submit",async event=>{
      event.preventDefault();if(!W.validate(query))return;const unlock=W.lock(query);taskStatus.replaceChildren(W.notice("正在查询…"));
      try{const data=await W.native("videos.get",{video_id:id.value.trim()});taskStatus.replaceChildren(W.notice(data.done===false?"任务仍在处理中。":data.error?"任务失败，请查看结果。":"已更新任务状态。",Boolean(data.error)));result.replaceChildren(el("h2",{},"视频任务"),W.result(data));}
      catch(error){taskStatus.replaceChildren(W.notice(error.message,true));}finally{unlock();}
    });W.root.append(query);
  }
  applyProtocol();
  if(page==="video")videoTask(input("video_id"));
  if(chat){renderMessages();await restoreSession();}
 };
})();
