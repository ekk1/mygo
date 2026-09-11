"use strict";
(() => {
 const W=window.WB,{el,button,field,input,select,pretty}=W;
 const labels={chat:"文字对话",image:"图片生成","image-edit":"图片编辑",speech:"语音合成",transcribe:"音频转写",translate:"音频翻译",video:"视频生成",files:"文件",containers:"容器",batches:"Batch 任务"};
 const menus={openai:["chat","image","image-edit","speech","transcribe","translate","files","containers","batches"],anthropic:["chat","files","batches"],gemini:["chat","image","image-edit","speech","transcribe","video","files","batches"],xai:["chat","image","image-edit","speech","transcribe","video","files","batches"]};
 W.menu=()=>{let ids=menus[W.vendor];if(W.vendor==="compatible"){ids=["chat"];for(const name of W.profile?.resources||[]){if(name==="images")ids.push("image","image-edit");else if(name==="audio")ids.push("speech","transcribe","translate");else ids.push(name);}}return(ids||[]).map(id=>[id,labels[id]]);};
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
  const catalogKey=`wb-models-${profile.id}-${profile.base_url}`;
  let modelIDs=[...(profile.models||[])];
  try{const saved=JSON.parse(sessionStorage.getItem(catalogKey)||"null");if(Array.isArray(saved))modelIDs=[...new Set([...modelIDs,...saved.filter(id=>typeof id==="string")])];}catch{}
  const catalog=el("datalist",{id:"model-catalog"});
  const updateCatalog=()=>catalog.replaceChildren(modelIDs.map(id=>el("option",{value:id})));
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
    render();search.focus();refresh.click();
  });
  const prompt=el("textarea",{name:"prompt",rows:chat?4:6,placeholder:chat?"输入消息…":"描述你希望完成的工作…"},draft.prompt||"");prompt.required=!["transcribe","translate"].includes(page);controls.prompt=prompt;
  const settingsFields=descriptors(vendor,page).map(([name,label,type,value,choices])=>{let control;if(type==="select")control=select(name,choices,draft[name]??(name==="protocol"?profile.protocol:value));else if(type==="textarea")control=el("textarea",{name,rows:2},draft[name]??value);else control=input(name,draft[name]??value,type);if(type==="checkbox")control.checked=draft[name]??value;if(type==="number")control.step="any";if(name==="tier"&&type==="text"){control.setAttribute("list","service-tiers");control.placeholder="省略 / auto / default / flex / priority";}controls[name]=control;return field(label,control);});
  const extras=el("textarea",{name:"extra",rows:5,spellcheck:"false"},draft.extra||"{}");controls.extra=extras;
  const files=el("input",{type:"file",name:"files",multiple:page==="image-edit",accept:page==="image-edit"?"image/*":"audio/*",required:["image-edit","transcribe","translate"].includes(page)});
  const mask=el("input",{type:"file",name:"mask",accept:"image/png"});
  const status=el("div",{class:"workspace-status"});const result=el("section",{class:"results","aria-live":"polite"});const preview=el("div",{});const messages=el("div",{id:"messages",class:"messages"});
  const configuration=el("details",{class:"card parameters",open:!chat},el("summary",{},"参数设置"),el("div",{class:"parameter-grid"},settingsFields),el("details",{class:"advanced"},el("summary",{},"原生扩展参数"),field("额外 JSON 字段",extras,"填写当前服务商原生字段；与上方已设置字段重名会提示冲突。")));
  const modelBar=el("div",{class:"model-bar"},field("实际模型",model),discover,catalog,el("datalist",{id:"service-tiers"},["auto","default","flex","priority","fast","scale"].map(value=>el("option",{value}))));
  const send=el("button",{type:"submit"},chat?"发送":"生成 / 提交");let controller=null,busy=false;
  const stop=button("停止",()=>controller?.abort(),"quiet");stop.disabled=true;
  const previewButton=button("预览请求",()=>perform(true),"secondary");
  const composer=el("section",{class:"card composer stack"},field(chat?"消息":["transcribe","translate"].includes(page)?"转写提示（可选）":"提示词 / 输入",prompt),["image-edit","transcribe","translate"].includes(page)?field(page==="image-edit"?"原始图片":"音频文件",files):null,page==="image-edit"&&["openai","compatible"].includes(vendor)?field("蒙版（可选）",mask):null,el("div",{class:"actions"},send,previewButton,stop));
  const form=el("form",{class:"workspace-form"},vendor==="xai"&&["speech","transcribe"].includes(page)?null:modelBar,configuration,chat?messages:null,composer,preview,status,chat?null:result);
  let session=null,parent="";const sessionsPanel=el("aside",{class:"card session-panel"});
  const renderMessages=()=>{messages.replaceChildren();if(!session?.messages.length){messages.append(el("div",{class:"empty"},el("strong",{},"开始一个新对话"),el("p",{},"模型与参数属于当前 profile。发送前可检查原始请求。")));return;}for(const m of session.messages){const node=el("details",{class:`message ${m.role}`,open:true},el("summary",{},m.role==="user"?"你":"模型",el("span",{class:"badge"},m.status),m.id===parent?el("span",{class:"badge good"},"下一次从这里继续"):null),el("div",{class:"message-text"},m.text||m.error||"（见原生结果）"),m.output?W.result(m.output):null,el("div",{class:"message-meta"},button("从这里继续",()=>{parent=m.id;invalidate();renderMessages();},"quiet"),button("复制为新会话",async()=>{if(busy)return;try{session=await W.api(`/api/sessions/${session.id}/fork`,{method:"POST",body:JSON.stringify({node_id:m.id})});parent=session.head_id;invalidate();renderMessages();await refreshSessions();}catch(error){W.fail(error,status);}},"quiet")));messages.append(node);}};
  const refreshSessions=async()=>{const list=await W.api(`/api/sessions?profile_id=${encodeURIComponent(profile.id)}`);sessionsPanel.replaceChildren(el("h2",{},"会话"),button("新建会话",()=>{if(busy)return;session=null;parent="";invalidate();renderMessages();refreshSessions().catch(error=>W.fail(error,status));}),el("div",{class:"session-list"},list.filter(item=>item.operation===operationForChat()).map(item=>el("div",{class:`session-item ${session?.id===item.id?"active":""}`},button(item.title,async()=>{if(busy)return;try{session=await W.api(`/api/sessions/${item.id}`);parent=session.head_id;invalidate();renderMessages();await refreshSessions();}catch(error){W.fail(error,status);}},"quiet"),button("删除",()=>W.confirm(`删除会话「${item.title}」？`,async()=>{if(busy)return;await W.api(`/api/sessions/${item.id}`,{method:"DELETE"});if(session?.id===item.id){session=null;parent="";renderMessages();}await refreshSessions();}),"danger")))));};
  const operationForChat=()=>vendor==="anthropic"?"messages.create":vendor==="gemini"?"content.generate":controls.protocol?.value==="chat"?"chat.create":"responses.create";
  const collect=()=>{const values=Object.fromEntries(Object.entries(controls).map(([key,c])=>[key,c.type==="checkbox"?c.checked:c.value]));values.files=[...files.files];values.mask=[...mask.files];return values;};
  function invalidate(){if(preview.childNodes.length)preview.replaceChildren(W.notice("输入或参数已变化，请重新预览。"));const values=collect();delete values.files;delete values.mask;try{sessionStorage.setItem(draftKey,JSON.stringify(values));}catch{}}
  form.addEventListener("input",invalidate);form.addEventListener("change",invalidate);
  controls.protocol?.addEventListener("change",()=>{session=null;parent="";renderMessages();refreshSessions().catch(error=>W.fail(error,status));});
  async function perform(isPreview){if(busy||!form.reportValidity())return;busy=true;send.disabled=true;previewButton.disabled=true;discover.disabled=true;for(const c of [...Object.values(controls),files,mask])c.disabled=true;controller=new AbortController();stop.disabled=false;status.replaceChildren(W.notice(isPreview?"正在构建请求预览…":"请求处理中…"));
    try{const values=collect();const request=await builders[vendor](page,values);let response;
      if(chat){const payload={provider_id:profile.id,operation:request.operation,params:request.params,parent_id:parent,expected_head:session?.head_id||"",revision:W.config.revision,text:values.prompt,stream:values.stream};
        if(!isPreview&&!session){session=await W.api("/api/sessions",{method:"POST",body:JSON.stringify({profile_id:profile.id,operation:request.operation,title:values.prompt.slice(0,60)||"新会话"})});}
        let liveText;
        const onEvent=event=>{const e=event.event||{},d=e.data||{};let text="";if(e.type==="response.output_text.delta")text=d.delta||"";else if(e.type==="content_block_delta")text=d.delta?.text||"";else if(d.choices)text=d.choices.map(c=>c.delta?.content||"").join("");else if(d.candidates)text=d.candidates.flatMap(c=>c.content?.parts||[]).map(p=>p.text||"").join("");if(!text)return;if(!liveText){messages.querySelector(".empty")?.remove();liveText=el("div",{class:"message-text","data-live-text":""});messages.append(el("details",{class:"message assistant",open:true},el("summary",{},"模型 · 正在生成"),liveText));}liveText.textContent+=text;};
        response=await W.api(`/api/sessions/${session?.id||"new"}/native${isPreview?"?preview=1":""}`,{method:"POST",body:JSON.stringify(payload),signal:controller.signal,onEvent});
      }else response=await W.native(request.operation,request.params,request.uploads,isPreview,controller.signal);
      status.replaceChildren();if(isPreview)preview.replaceChildren(W.preview(response));else if(chat){session=response;parent=session.head_id;prompt.value="";invalidate();preview.replaceChildren();renderMessages();await refreshSessions();}else{result.replaceChildren(el("h2",{},page==="video"?"任务已提交":"结果"),W.result(response));if(page==="video"){const id=response.request_id||response.name||response.id;if(id)videoTask(input("video_id",id));}status.append(W.notice("请求已完成。"));}
    }catch(error){status.replaceChildren(W.notice(error.name==="AbortError"?"请求已停止。已提交的云端任务不会自动取消。":error.message,true));if(chat&&session){try{for(let attempt=0;attempt<100;attempt++){session=await W.api(`/api/sessions/${session.id}`);if(!session.messages.some(item=>item.status==="pending"))break;await new Promise(resolve=>setTimeout(resolve,100));}parent=session.head_id;renderMessages();await refreshSessions();}catch(reload){W.fail(reload,status);}}}
    finally{busy=false;send.disabled=false;previewButton.disabled=false;discover.disabled=false;for(const c of [...Object.values(controls),files,mask])c.disabled=false;stop.disabled=true;controller=null;}
  }
  form.addEventListener("submit",event=>{event.preventDefault();perform(false);});
  W.root.replaceChildren(W.heading(labels[page],`${W.vendors[vendor].name} / ${profile.name}`),chat?el("div",{class:"chat-layout"},sessionsPanel,form):form);
  function videoTask(id){W.root.querySelector("#video-task")?.remove();W.root.append(el("section",{id:"video-task",class:"card stack"},el("h2",{},"查询视频任务"),field(vendor==="gemini"?"Operation name":"Request ID",id),button("查询状态",async()=>{try{const data=await W.native("videos.get",{video_id:id.value});result.replaceChildren(W.result(data));}catch(error){W.fail(error,status);}})));}
  if(page==="video")videoTask(input("video_id"));
  if(chat){renderMessages();await refreshSessions();}
 };
})();
