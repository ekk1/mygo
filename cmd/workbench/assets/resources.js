"use strict";
(() => {
 const W=window.WB,{el,button,field,input,select,pretty}=W;
 const size=value=>value===undefined?"—":Number(value)<1024?`${value} B`:Number(value)<1048576?`${(value/1024).toFixed(1)} KB`:`${(value/1048576).toFixed(1)} MB`;
 const timestamp=value=>{if(!value)return "—";const d=new Date(typeof value==="number"?value*1000:value);return Number.isNaN(d.getTime())?String(value):d.toLocaleString();};
 W.resources=async()=>{
  const {vendor,feature,profile}=W;const cid=feature==="containers"?new URLSearchParams(location.search).get("container"):null;
  const group=cid?"containers.files":feature;
  const title=cid?"容器文件":{files:"文件",containers:"容器",batches:"Batch 任务"}[feature];
  const search=input("search");search.placeholder="搜索当前页的名称或 ID";
  const status=el("div",{}),tableBox=el("div",{class:"table-scroll"}),pager=el("div",{class:"pagination"});
  const cacheKey="wb-resources-"+JSON.stringify([vendor,profile.id,profile.base_url,W.config.revision,group,cid]);
  const cached=W.readCache(cacheKey);
  let loaded=Array.isArray(cached?.rows),rows=loaded?cached.rows:[],cursor=loaded?cached.cursor||"":"",next=loaded?cached.next||"":"",previous=loaded&&Array.isArray(cached.previous)?cached.previous:[],loading=false;
  const remember=()=>{if(loaded)W.writeCache(cacheKey,{rows,cursor,next,previous});};
  const listParams=(pageCursor)=>{const params=vendor==="gemini"?{pageSize:50}:{limit:50};if(cid)params.container_id=cid;if(pageCursor){if(vendor==="anthropic"&&feature==="files")params.after_id=pageCursor;else if(vendor==="gemini")params.pageToken=pageCursor;else if(vendor==="xai")params.pagination_token=pageCursor;else if(vendor==="anthropic")params.after_id=pageCursor;else params.after=pageCursor;}return params;};
  const idOf=row=>row.id||row.batch_id||row.name;
  const withID=row=>cid?{container_id:cid,file_id:idOf(row)}:feature==="files"?{file_id:idOf(row)}:feature==="containers"?{container_id:idOf(row)}:{batch_id:idOf(row)};
  async function call(operation,params,uploads){return W.native(operation,params,uploads);}
  async function load(targetCursor=cursor,targetHistory=previous){
    if(loading)return;loading=true;refresh.disabled=true;search.disabled=true;primary.disabled=true;
    const unlockRows=W.lock(tableBox);
    for(const control of pager.querySelectorAll("button"))control.disabled=true;
    tableBox.setAttribute("aria-busy","true");status.replaceChildren(W.notice("正在读取资源列表…"));
    try{
      const data=await call(group+".list",listParams(targetCursor));
      const pageRows=data.data||data.files||data.batches||data.batch_jobs||data.containers||(vendor==="gemini"?[]:undefined);
      if(!Array.isArray(pageRows))throw new Error("服务商返回的资源列表格式无法识别，请查看请求日志");
      rows=pageRows;cursor=targetCursor;previous=[...targetHistory];
      next=data.nextPageToken||data.next_page_token||data.pagination_token||data.next_pagination_token||data.pagination?.next_token||(data.has_more?(data.last_id||idOf(rows.at(-1)||{})):"");
      loaded=true;remember();
      status.replaceChildren();
    }catch(error){status.replaceChildren(W.notice(error.message,true),button("重试",()=>load(targetCursor,targetHistory)));}
    finally{loading=false;refresh.disabled=false;search.disabled=false;primary.disabled=false;unlockRows();tableBox.removeAttribute("aria-busy");render();}
  }
  async function show(row){
    const modal=W.dialog("资源详情");
    modal.body.replaceChildren(W.notice("正在读取资源详情…"));
    try{const data=await call(group+".get",withID(row));if(modal.dialog.isConnected)modal.body.replaceChildren(el("pre",{},pretty(data)));}
    catch(error){if(modal.dialog.isConnected)modal.body.replaceChildren(W.notice(error.message,true));}
  }
  async function download(operation,params,name){
    const data=await call(operation,params);
    const blob=data?.blob||new Blob([typeof data==="string"?data:pretty(data)],{type:"application/json"});
    const link=W.download(blob,name||data?.filename||"download.bin");
    status.replaceChildren(el("div",{class:"actions"},W.notice("下载已就绪。"),link));link.click();
  }
  const rowButton=(label,action,kind="quiet")=>W.action(label,action,kind,status);
  function rowActions(row){const actions=[button("详情",()=>show(row),"quiet")];
   if(feature==="containers"&&!cid)actions.unshift(el("a",{href:W.url("containers")+"?container="+encodeURIComponent(idOf(row)),class:"button secondary"},"打开文件"));
   if(feature==="files"||cid){if(vendor!=="gemini")actions.push(rowButton("下载",()=>download(group+".download",{...withID(row),filename:row.filename||row.path?.split("/").at(-1)||"download.bin"},row.filename||row.path?.split("/").at(-1))));actions.push(W.copyButton(row.uri||idOf(row),"复制引用"));}
   if(feature==="batches"){
     const output=row.output_file_id||row.outputFileId||row.dest?.fileName||row.output?.responsesFile||row.response?.responsesFile;
     const errors=row.error_file_id;
     if(output)actions.push(rowButton("下载结果",()=>download("files.download",{file_id:output},"batch-results.jsonl")));
     if(errors)actions.push(rowButton("下载错误",()=>download("files.download",{file_id:errors},"batch-errors.jsonl")));
     if(vendor==="anthropic")actions.push(rowButton("下载结果",()=>download("batches.results",withID(row),"batch-results.jsonl")));
     if(vendor==="xai")actions.push(rowButton("查看结果",()=>xaiResults(row)),rowButton("添加请求",()=>appendBatch(row)));
     const phase=String(row.status||row.processing_status||row.metadata?.state||row.state?.name||(typeof row.state==="string"?row.state:"")).toLowerCase();if(!/(completed|ended|cancelled|canceled|failed|expired|succeeded)/.test(phase))actions.push(rowButton("取消任务",()=>W.confirm("取消这个 Batch 任务？",async()=>{const data=await call("batches.cancel",withID(row));if(data&&typeof data==="object")Object.assign(row,data);remember();render();status.replaceChildren(W.notice("取消请求已提交，点击「刷新」查看最新状态。"));}),"danger"));
   }else actions.push(rowButton("删除",()=>W.confirm(`删除「${row.filename||row.displayName||row.name||idOf(row)}」？`,async()=>{await call(group+".delete",withID(row));rows=rows.filter(item=>idOf(item)!==idOf(row));remember();render();status.replaceChildren(W.notice("已删除。点击「刷新」可重新读取列表。"));}),"danger"));
   return el("div",{class:"row-actions"},actions);
  }
  function render(){const query=search.value.trim().toLowerCase();const shown=rows.filter(row=>[row.filename,row.displayName,row.metadata?.displayName,row.name,row.id,row.batch_id,row.path].some(value=>String(value||"").toLowerCase().includes(query)));
    const headers=feature==="batches"?["任务","状态 / 进度","创建时间","操作"]:feature==="containers"&&!cid?["容器","状态","创建时间","操作"]:["文件","大小","用途 / 状态","创建时间","操作"];
    const table=el("table",{class:"resource-table"},el("thead",{},el("tr",{},headers.map(h=>el("th",{scope:"col"},h)))),el("tbody",{},shown.map(row=>{
      const name=row.filename||row.displayName||row.metadata?.displayName||row.path||row.name||idOf(row);const state=row.metadata?.state||(typeof row.state==="object"?row.state.name:row.state);
      let progress=row.request_counts||row.batch_stats||row.batchStats||row.metadata?.batchStats||(vendor==="xai"?row.state:null);let statusText=row.status||row.processing_status||state||row.purpose||"—";
      if(progress){const complete=progress.completed??progress.succeeded??progress.successfulRequestCount??progress.num_success??0;const failed=progress.failed??progress.errored??progress.failedRequestCount??progress.num_error??0;const total=progress.total??progress.requestCount??progress.num_requests??(Number(complete)+Number(failed)+Number(progress.processing||progress.num_pending||0));statusText+=` · ${complete} / ${total} · 失败 ${failed}`;}
      const cells=[el("td",{},el("strong",{},name),el("small",{class:"resource-id"},idOf(row)))];
      if(feature==="files"||cid)cells.push(el("td",{},size(row.bytes??row.size_bytes??row.sizeBytes)));
      cells.push(el("td",{},el("span",{class:"badge"},statusText)),el("td",{},timestamp(row.created_at||row.create_time||row.createTime||row.metadata?.createTime)),el("td",{},rowActions(row)));
      return el("tr",{"data-resource-id":idOf(row)},cells);
    })));
    tableBox.replaceChildren(table);if(!shown.length)tableBox.append(el("div",{class:"empty"},!loaded?"尚未加载资源列表，点击「刷新」读取。":rows.length?"当前页没有匹配的资源。":"这里还没有资源。"));
    const prev=button("上一页",()=>{if(!loading)load(previous.at(-1)||"",previous.slice(0,-1));});prev.disabled=!previous.length||loading;
    const nextButton=button("下一页",()=>{if(!loading)load(next,[...previous,cursor]);});nextButton.disabled=!next||loading;
    pager.replaceChildren(el("span",{class:"muted small"},`本页 ${shown.length} 项 · 搜索仅筛选当前页`),el("div",{class:"actions"},prev,nextButton));
  }
  function xaiResults(row){
    const modal=W.dialog("Batch 结果"),key=cacheKey+":results:"+idOf(row),feedback=el("div",{}),output=el("div",{class:"stack"});
    let data=W.readCache(key),busy=false;
    const refresh=button("刷新结果",()=>loadResults());
    const renderResults=()=>{
      output.replaceChildren();
      if(!data){output.append(W.notice("尚未加载结果，点击「刷新结果」读取。"));return;}
      const nextToken=data.pagination_token||data.next_pagination_token;
      output.append(el("pre",{},pretty(data)),el("div",{class:"actions"},W.download(new Blob([pretty(data)],{type:"application/json"}),"batch-results-page.json"),nextToken?button("下一页结果",()=>loadResults(nextToken)):null));
    };
    async function loadResults(token=""){
      if(busy)return;busy=true;const unlock=W.lock(modal.body);modal.setBusy(true);feedback.replaceChildren(W.notice("正在读取结果…"));
      try{data=await call("batches.results",{batch_id:idOf(row),limit:100,...(token?{pagination_token:token}:{})});W.writeCache(key,data);renderResults();feedback.replaceChildren();}
      catch(error){feedback.replaceChildren(W.notice(error.message,true),button("重试",()=>loadResults(token)));}
      finally{busy=false;unlock();modal.setBusy(false);}
    }
    modal.body.replaceChildren(el("div",{class:"actions"},refresh),feedback,output);renderResults();
  }
  function appendBatch(row){
    const modal=W.dialog("向 Batch 添加请求"),text=el("textarea",{rows:10},'[{"batch_request_id":"request-1","batch_request":{"responses":{"model":"","input":[{"role":"user","content":"你好"}]}}}]');
    const output=el("div",{});let busy=false;
    const form=el("form",{class:"stack"},field("原生 batch_requests 数组",text),el("div",{class:"actions"},el("button",{type:"submit"},"添加请求"),button("预览请求",()=>send(true))),output);
    async function send(preview){
      if(busy)return;const trigger=document.activeElement;busy=true;const unlock=W.lock(form);modal.setBusy(true);
      try{
        const requests=JSON.parse(text.value);if(!Array.isArray(requests)||!requests.length)throw new Error("请填写非空请求数组");
        const body={batch_id:idOf(row),batch_requests:requests},result=await W.native("batches.requests",body,{},preview);
        if(preview){output.replaceChildren();W.showPreview(result,trigger);}else{modal.close();status.replaceChildren(W.notice("请求已添加，点击「刷新」更新列表。"));}
      }catch(error){output.replaceChildren(W.notice(error.message,true));}
      finally{busy=false;unlock();modal.setBusy(false);}
    }
    text.addEventListener("input",()=>output.replaceChildren());form.addEventListener("submit",event=>{event.preventDefault();send(false);});
    modal.body.replaceChildren(form);text.focus();
  }
  const refresh=button("刷新",()=>load("",[]));search.addEventListener("input",render);
  const primary=button(feature==="batches"?"创建 Batch":feature==="containers"&&!cid?"创建容器":"上传文件",()=>openEditor(),"");
  W.root.replaceChildren(W.heading(title,`${W.vendors[vendor].name} / ${profile.name}${cid?" / "+cid:""}`,el("div",{class:"actions"},cid?el("a",{href:W.url("containers"),class:"button quiet"},"返回容器"):null,cid?button("引用已有文件",()=>openEditor(true)):null,primary)),el("section",{class:"card resource-manager"},el("div",{class:"resource-toolbar"},field("搜索当前页",search),refresh),status,tableBox,pager));
  async function openEditor(reference=false){
   const modal=W.dialog(reference?"引用已有文件":primary.textContent);
   const fields=[];let build;
   if(cid&&reference){const file=input("file_id");file.required=true;fields.push(field("已有文件 ID",file));build=()=>({operation:"containers.files.add",params:{container_id:cid,file_id:file.value}});}
   else if(feature==="files"||cid){const file=W.assetControl({label:"选择文件",multiple:false,onChange:()=>form.dispatchEvent(new Event("input",{bubbles:true}))});const purpose=select("purpose",["assistants","batch","user_data","vision"],"assistants");const name=input("display_name");fields.push(file.node);if(["openai","compatible","xai"].includes(vendor)&&!cid)fields.push(field("用途",purpose));if(vendor==="gemini")fields.push(field("显示名称（可选）",name));build=()=>{if(!file.getIDs().length)throw new Error("请从精选资产中选择文件。");return {operation:group+".upload",params:cid?{container_id:cid}:vendor==="gemini"?{display_name:name.value}:vendor==="anthropic"?{}:{purpose:purpose.value},asset_ids:{file:file.getIDs()}};};
   }else if(feature==="containers"){const name=input("name","workbench");name.required=true;const memory=select("memory_limit",["1g","4g","16g","64g"],"1g");const ids=input("file_ids");fields.push(field("名称",name),field("内存",memory),field("已有文件 ID（逗号分隔，可选）",ids));build=()=>({operation:"containers.create",params:{name:name.value,memory_limit:memory.value,...(ids.value?{file_ids:ids.value.split(",").map(x=>x.trim()).filter(Boolean)}:{})}});
   }else if(vendor==="anthropic"){const requests=el("textarea",{rows:10,required:true},'[\n  {"custom_id":"request-1","params":{"model":"","max_tokens":1024,"messages":[{"role":"user","content":"你好"}]}}\n]');fields.push(field("Message Batch 请求数组",requests));build=()=>({operation:"batches.create",params:{requests:JSON.parse(requests.value)}});
   }else if(vendor==="gemini"){const model=input("model",profile.models?.[0]||"");model.required=true;const file=input("file_name");file.required=true;const name=input("display_name","workbench");fields.push(field("实际模型",model),field("输入文件名（files/...）",file),field("显示名称",name));build=()=>({operation:"batches.create",params:{model:model.value,batch:{display_name:name.value,input_config:{file_name:file.value}}}});
   }else if(vendor==="xai"){const name=input("name","workbench");const file=input("input_file_id");fields.push(field("任务名称",name),field("输入 JSONL 文件 ID（可选）",file,"也可以先创建空任务，再用行内「添加请求」提交。"));build=()=>({operation:"batches.create",params:{name:name.value,...(file.value?{input_file_id:file.value}:{})}});
   }else {
     const file=input("input_file_id");file.required=true;file.setAttribute("list","batch-files");
     const choices=el("datalist",{id:"batch-files"}),key=cacheKey+":input-files",feedback=el("div",{});
     const renderFiles=rows=>choices.replaceChildren(...rows.map(f=>el("option",{value:f.id},f.filename)));
     const cached=W.readCache(key);if(Array.isArray(cached))renderFiles(cached);
     const refreshFiles=W.action("刷新文件",async()=>{
       const unlock=W.lock(modal.body);modal.setBusy(true);
       try{const data=await call("files.list",{purpose:"batch",limit:100});if(!Array.isArray(data.data))throw new Error("服务商返回的文件列表格式无法识别");renderFiles(data.data);W.writeCache(key,data.data);feedback.replaceChildren(W.notice(`已读取 ${data.data.length} 个输入文件。`));}
       finally{unlock();modal.setBusy(false);}
     },"secondary",feedback);
     const endpoint=select("endpoint",["/v1/responses","/v1/chat/completions","/v1/embeddings"],"/v1/responses");
     fields.push(field("输入 JSONL 文件 ID",file,"可直接填写 ID，或点击「刷新文件」加载 purpose=batch 的候选文件。"),choices,el("div",{class:"actions"},refreshFiles),feedback,field("请求端点",endpoint));
     build=()=>({operation:"batches.create",params:{input_file_id:file.value,endpoint:endpoint.value,completion_window:"24h"}});
   }
   const extra=el("textarea",{rows:4},"{}");const out=el("div",{});const submit=el("button",{type:"submit"},"提交");const preview=button("预览请求",()=>perform(true));
   const form=el("form",{class:"stack","data-resource-editor":""},fields,el("details",{},el("summary",{},"原生扩展参数"),field("额外 JSON 字段",extra)),el("div",{class:"actions"},submit,preview),out);
   let busy=false;
   async function perform(isPreview){
     if(busy||!W.validate(form))return;const trigger=document.activeElement;busy=true;const unlock=W.lock(form);modal.setBusy(true);
     try{
       const req=build(),extras=JSON.parse(extra.value||"{}");
       if(!extras||Array.isArray(extras)||typeof extras!=="object")throw new Error("扩展参数必须是 JSON 对象");
       for(const [key,value]of Object.entries(extras)){if(Object.hasOwn(req.params,key))throw new Error(`重复字段 ${key}`);req.params[key]=value;}
       const data=await W.native(req.operation,req.params,req.uploads,isPreview,undefined,{assets:req.asset_ids});
       if(isPreview){out.replaceChildren();W.showPreview(data,trigger);}
       else{modal.close();status.replaceChildren(W.notice("操作已完成，点击「刷新」更新列表。"));}
     }catch(error){out.replaceChildren(W.notice(error.message,true));}
     finally{busy=false;unlock();modal.setBusy(false);}
   }
   form.addEventListener("submit",event=>{event.preventDefault();perform(false);});
   form.addEventListener("input",()=>{if(out.childNodes.length)out.replaceChildren(W.notice("输入已变化，请重新预览。"));});
   modal.body.replaceChildren(form);form.querySelector("input,textarea,select")?.focus();

  }
  render();
 };
})();
