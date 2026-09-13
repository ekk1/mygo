"use strict";
(() => {
  const W=window.WB,{el,input,select,field}=W;
  const labels={input:"输入",output:"输出",total:"合计",cache_read:"缓存命中",cache_write:"缓存写入",cache_write_5m:"5 分钟缓存写入",cache_write_1h:"1 小时缓存写入",reasoning:"推理",tool_input:"工具提示"};
  const number=n=>n===undefined?"未知":Number(n).toLocaleString("zh-CN");
  const line=t=>["input","output","total"].map(k=>labels[k]+" "+number(t?.[k])).join(" · ");
  W.usageLine=u=>{
    if(!u)return el("div",{class:"token-usage small muted"},"Token 用量：上游未报告");
    const extras=Object.keys(labels).filter(k=>!["input","output","total"].includes(k)&&u.tokens?.[k]!==undefined);
    return el("details",{class:"token-usage small"},
      el("summary",{},"Token · "+line(u.tokens)+(u.partial?" · 部分用量":"")),
      el("p",{class:"muted"},extras.length?extras.map(k=>labels[k]+" "+number(u.tokens[k])).join(" · "):"上游未报告缓存、推理等细分用量。"),
      el("p",{class:"muted"},"缓存与推理是输入／输出的细分项，不重复加入合计。"),
      u.raw?el("pre",{},W.pretty(u.raw)):null);
  };
  W.usageSummary=(s,title="已知累计")=>el("div",{class:"usage-summary"},
    el("strong",{},title+" · "+line(s?.tokens)),
    el("span",{class:"small muted"},Object.keys(labels).filter(k=>!["input","output","total"].includes(k)&&s?.tokens?.[k]!==undefined).map(k=>labels[k]+" "+number(s.tokens[k])).join(" · ")),
    el("span",{class:"small muted"},`请求 ${s?.requests||0} · 已报告 ${s?.reported||0} · 未报告 ${(s?.requests||0)-(s?.reported||0)} · 部分用量 ${s?.partial||0}`));
  W.sumUsage=messages=>{
    const s={tokens:{},requests:0,reported:0,partial:0};
    for(const m of messages){if(m.role!=="assistant")continue;s.requests++;if(!m.usage)continue;s.reported++;if(m.usage.partial)s.partial++;for(const [k,v]of Object.entries(m.usage.tokens||{}))s.tokens[k]=(s.tokens[k]||0)+v;}
    return s;
  };
  W.usage=()=>{
    const query=new URLSearchParams(location.search),cached=W.readCache("usage:view"),saved=query.size?Object.fromEntries(query):cached?.filters||{};
    const profile=select("usage_profile",[["","全部 profiles"],...W.config.providers.map(p=>[p.id,p.name])],saved.profile_id||"");
    if(saved.profile_id&&!W.config.providers.some(p=>p.id===saved.profile_id))profile.append(el("option",{value:saved.profile_id,selected:""},saved.profile_id+"（历史）"));
    const model=input("usage_model",saved.model||""),from=input("usage_from",saved.from||"","date"),to=input("usage_to",saved.to||"","date");
    const feedback=el("div",{}),result=el("div",{class:"usage-results stack"}),form=el("form",{class:"card stack"});
    const filters=()=>({profile_id:profile.value,model:model.value.trim(),from:from.value,to:to.value});
    const table=(head,rows)=>el("div",{class:"usage-table"},el("table",{},el("thead",{},el("tr",{},head.map(v=>el("th",{scope:"col"},v)))),el("tbody",{},rows)));
    function show(data){
      const profiles=new Map();
      for(const p of W.config.providers)if(!profile.value||profile.value===p.id)profiles.set(p.id,{name:p.name,summary:{tokens:{},requests:0,reported:0,partial:0}});
      for(const g of data.groups){
        if(!profiles.has(g.profile_id))profiles.set(g.profile_id,{name:g.profile_name||g.profile_id,summary:{tokens:{},requests:0,reported:0,partial:0}});
        const s=profiles.get(g.profile_id).summary;
        for(const k of ["requests","reported","partial"])s[k]+=g.usage[k];
        for(const [k,n]of Object.entries(g.usage.tokens||{}))s.tokens[k]=(s.tokens[k]||0)+n;
      }
      const json=W.download(new Blob([W.pretty({...data,filters:filters()})],{type:"application/json"}),"token-usage.json");json.textContent="导出 JSON";
      const columns=["id","profile_id","profile_name","vendor","base_url","operation","requested_model","model","service_tier","response_id","session_id","message_id","task_id","created_at","status","historical",...Object.keys(labels),"partial","raw_usage"];
      // Quote every cell and neutralize spreadsheet formulas in provider-supplied strings.
      const csvCell=v=>'"'+String(typeof v==="string"&&/^[=+\-@\t\r]/.test(v)?"'"+v:v??"").replaceAll('"','""')+'"';
      const csv=[columns,...data.records.map(r=>columns.map(k=>k==="raw_usage"?JSON.stringify(r.usage?.raw??null):k==="partial"?r.usage?.partial:k in labels?r.usage?.tokens?.[k]:r[k]))].map(row=>row.map(csvCell).join(",")).join("\r\n");
      const csvLink=W.download(new Blob(["\ufeff",csv],{type:"text/csv;charset=utf-8"}),"token-usage.csv");csvLink.textContent="导出 CSV";
      const state={complete:"已完成",error:"失败",pending:"进行中",interrupted:"已中断",cancelled:"已停止"};
      result.replaceChildren(
        el("section",{class:"card usage-total"},W.usageSummary(data.total),el("div",{class:"actions"},json,csvLink)),
        el("section",{class:"card stack"},el("h2",{},"Profile 累计"),table(["Profile","当前筛选范围的已知用量"],[...profiles].map(([id,p])=>el("tr",{},el("td",{},el("a",{href:"/usage?"+new URLSearchParams({...filters(),profile_id:id})},p.name),el("div",{class:"small muted"},id)),el("td",{},W.usageSummary(p.summary)))))),
        el("section",{class:"card stack"},el("h2",{},"模型明细"),table(["Profile / 模型","已知用量"],data.groups.map(g=>el("tr",{},el("td",{},g.profile_name||g.profile_id,el("div",{class:"small"},g.model||"模型未报告")),el("td",{},W.usageSummary(g.usage)))))),
        el("section",{class:"card stack"},el("h2",{},"每次请求"),data.records.length?table(["时间 / 请求","Token 用量"],data.records.map(r=>el("tr",{"data-usage-id":r.id},
          el("td",{},el("div",{},r.created_at||"时间未知"),el("div",{},(r.profile_name||r.profile_id)+" · "+(r.model||"模型未报告")),el("div",{class:"small muted"},r.operation+" · "+(state[r.status]||r.status)+(r.historical?" · 历史回填":"")),
            el("details",{class:"small"},el("summary",{},"请求标识"),el("pre",{},W.pretty(Object.fromEntries(Object.entries(r).filter(([k])=>k!=="usage")))))),
          el("td",{},W.usageLine(r.usage))))):el("p",{},"没有符合条件的请求。")));
    }
    form.append(el("div",{class:"parameter-grid"},field("Profile",profile),field("模型（精确匹配）",model),field("起始日期（UTC）",from),field("结束日期（UTC）",to)),el("div",{class:"actions"},el("button",{type:"submit"},"刷新用量")),feedback);
    form.addEventListener("submit",async event=>{
      event.preventDefault();const unlock=W.lock(form);feedback.replaceChildren(W.notice("正在读取用量…"));
      try{const selected=filters(),data=await W.api("/api/usage?"+new URLSearchParams(selected));W.writeCache("usage:view",{filters:selected,data});show(data);feedback.replaceChildren(W.notice("已刷新；统计包含结束日期当天（UTC）。"));}
      catch(error){feedback.replaceChildren(W.notice(error.message,true));}finally{unlock();}
    });
    W.root.replaceChildren(W.heading("Token 用量","本工作台发起的 LLM 请求，按上游实际报告累计。"),el("p",{class:"small muted"},"未报告的用量不估算。缓存、推理等细分保留原始字段；删除或复制会话不改变历史账本。金额与服务商账单以其计费规则为准。"),form,result);
    if(cached?.data&&JSON.stringify(cached.filters)===JSON.stringify(filters())){show(cached.data);feedback.append(W.notice("显示上次读取的结果，点击刷新用量更新。"));}
    else result.append(el("p",{class:"muted"},"点击「刷新用量」读取本地账本。"));
  };
})();
