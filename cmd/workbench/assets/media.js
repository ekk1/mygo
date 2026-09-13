"use strict";
(() => {
  const W = window.WB, {el, field, input, button} = W;
  const active = task => ["queued", "running"].includes(task.status);
  const states = {queued:"排队中", running:"运行中", complete:"已完成", error:"失败", cancelled:"已取消", interrupted:"已中断"};
  W.mediaTaskPanel = (initial, poll = false) => {
    const panel = el("section", {class:"media-task stack"}), status = el("div", {}), result = el("div", {});
    let task = initial, busy = false, imported = false;
    const refresh = W.action("刷新任务", () => load(), "secondary", status);
    const cancel = W.action("停止任务", async () => {
      task = await W.api(`/api/tasks/${encodeURIComponent(task.id)}/cancel`, {method:"POST", body:"{}"});
      show();
    }, "danger", status);
    function show() {
      W.rememberTasks(task);
      if (poll && !imported && task.result?.assets) {
        W.rememberAssets(task.result.assets);imported = true;
      }
      status.replaceChildren(W.notice(states[task.status] || task.status));
      cancel.hidden = !active(task);
      result.replaceChildren(W.taskResult(task));
    }
    async function load() {
      if (busy || !panel.isConnected) return;
      busy = true;
      try {
        const current = await W.api(`/api/tasks/${encodeURIComponent(task.id)}`);
        if (panel.isConnected) {task = current; show();}
      } finally {busy = false;}
    }
    panel.append(el("div", {class:"actions"}, refresh, cancel, el("a", {href:"/tasks", class:"button quiet"}, "后台任务")), status, result);
    show();
    // Only a newly submitted job polls. Restored pages read one detail then wait for clicks.
    setTimeout(async () => {
      try {
        do {
          if (!panel.isConnected) return;
          await load();
          if (!poll || !active(task)) return;
          await new Promise(resolve => setTimeout(resolve, 1000));
        } while (panel.isConnected);
      } catch (error) {if (panel.isConnected) status.replaceChildren(W.notice(error.message, true));}
    }, 0);
    return panel;
  };
  W.mediaActions = asset => {
    if (!/^(audio|video)\//.test(asset.content_type || "")) return [];
    const player = el("a", {href:`/media/player/${encodeURIComponent(asset.id)}`, class:"button quiet"}, "播放器");
    const extract = W.action("抽取音频", async () => {
      const modal = W.dialog("抽取音频", extract);
      modal.body.append(W.notice("正在提交 MP3 音频抽取任务…"));modal.setBusy(true);
      try {
        const {task} = await W.api(`/api/assets/${encodeURIComponent(asset.id)}/extract-audio`, {method:"POST", body:"{}"});
        W.rememberTasks(task);
        modal.body.replaceChildren(W.mediaTaskPanel(task, true));
      } catch (error) {modal.body.replaceChildren(W.notice(error.message, true));}
      finally {modal.setBusy(false);}
    }, "quiet");
    return [player, extract];
  };
  W.media = () => {
    const cached = W.readCache("media:draft") || {};
    const url = input("media_url", cached.url || "", "url"), proxy = input("media_proxy", cached.proxy || "");
    const format = input("media_format", cached.format || "bv*+ba/b");url.required = true;
    url.placeholder = "https://…";proxy.placeholder = "http://127.0.0.1:7890";
    const form = el("form", {class:"card media-form stack"}), status = el("div", {}), formats = el("div", {class:"media-formats"}), taskBox = el("div", {});
    let info = null, discovered = "";
    const snapshot = () => ({url:url.value.trim(), proxy:proxy.value.trim(), format:format.value.trim()});
    const identity = () => JSON.stringify([url.value.trim(), proxy.value.trim()]);
    const remember = () => W.writeCache("media:draft", snapshot());
    function clearDiscovery() {info = null; formats.replaceChildren(W.notice("点击「发现格式」读取可用格式。"));}
    for (const control of [url, proxy, format]) control.addEventListener("input", () => {remember(); if (control !== format) clearDiscovery();});
    const amount = (value, unit = "") => typeof value === "number" && Number.isFinite(value) && value > 0 ? `${Number(value.toFixed(1))}${unit}` : "未知";
    const size = f => {
      const n = f.filesize || f.filesize_approx;
      return n > 0 ? `${f.filesize ? "" : "约 "}${amount(n / 1048576, " MiB")}` : "未知";
    };
    const group = f => f.vcodec === "none" && f.acodec && f.acodec !== "none" ? "纯音频" : f.acodec === "none" && f.vcodec && f.vcodec !== "none" ? "纯视频" : f.acodec && f.vcodec && f.acodec !== "none" && f.vcodec !== "none" ? "音视频" : "其他 / 未知";
    function renderFormats() {
      const header = el("div", {class:"stack"}, el("h2", {}, info.title || "可用格式"), el("p", {class:"muted"}, `时长：${amount(info.duration, " 秒")} · 纯视频不含声音，可选择「配最佳音频」合并下载。`));
      formats.replaceChildren(header);
      for (const name of ["音视频", "纯音频", "纯视频", "其他 / 未知"]) {
        const rows = (info.formats || []).filter(f => group(f) === name);
        if (!rows.length) continue;
        const tbody = el("tbody", {}, rows.map(f => {
          const choose = value => {if (identity() !== discovered) return;format.value = value;remember();status.replaceChildren(W.notice(`已选择格式：${value}`));};
          const actions = el("div", {class:"row-actions"}, button("选择", () => choose(f.format_id), "quiet"));
          if (name === "纯视频") actions.append(button("配最佳音频", () => choose(`${f.format_id}+ba`), "quiet"));
          return el("tr", {"data-format-id":f.format_id},
            el("td", {}, el("strong", {}, f.format_id), el("small", {class:"muted asset-name"}, `${f.ext || "未知容器"} · ${f.format_note || name}`)),
            el("td", {"data-label":"画质 / 帧率"}, name === "纯音频" ? "—" : `${amount(f.height, "p")} / ${amount(f.fps, " fps")}`),
            el("td", {"data-label":"视频 / 音频编码"}, `${f.vcodec || "未知"} / ${f.acodec || "未知"}`),
            el("td", {"data-label":"码率"}, amount(f.abr || f.tbr, " kbps")), el("td", {"data-label":"大小"}, size(f)), el("td", {}, actions));
        }));
        formats.append(el("section", {class:"media-format-group"}, el("h3", {}, `${name} · ${rows.length}`), el("div", {class:"table-scroll"}, el("table", {class:"resource-table library-table"},
          el("thead", {}, el("tr", {}, ["格式", "画质 / 帧率", "视频 / 音频编码", "码率", "大小", "选择"].map(v => el("th", {}, v)))), tbody))));
      }
      if (!info.formats?.length) formats.append(W.notice("未返回格式列表，可使用默认格式直接下载。"));
    }
    const discover = W.action("发现格式", async () => {
      if (!W.validate(form)) return;
      const unlock = W.lock(form), key = identity();status.replaceChildren(W.notice("正在发现格式…"));
      try {
        const response = await W.api("/api/media/formats", {method:"POST", body:JSON.stringify(snapshot())});
        if (identity() !== key || !form.isConnected) return;
        info = response; discovered = key;renderFormats();status.replaceChildren();
      } finally {unlock();}
    }, "secondary", status);
    form.addEventListener("submit", async event => {
      event.preventDefault();if (!W.validate(form)) return;
      const unlock = W.lock(form);remember();
      try {
        const {task} = await W.api("/api/media/download", {method:"POST", body:JSON.stringify(snapshot())});
        W.writeCache("media:task", {id:task.id});W.rememberTasks(task);
        taskBox.replaceChildren(W.mediaTaskPanel(task, true));status.replaceChildren(W.notice("下载已提交，完成后自动入资产库。"));
      } catch (error) {status.replaceChildren(W.notice(error.message, true));}
      finally {unlock();}
    });
    const preset = W.select("media_preset", [["", "选择常用格式…"], ["bv*+ba/b", "最佳视频与音频"], ["ba/b", "优先纯音频"], ["b", "单文件音视频"]]);
    preset.addEventListener("change", () => {if (preset.value) {format.value = preset.value;remember();}});
    form.append(field("媒体链接", url, "一次下载单条媒体，支持 yt-dlp 可处理的网站及播客链接。"),
      field("代理", proxy, "留空沿用服务进程环境代理，- 表示直连。"),
      el("div", {class:"media-options"}, field("常用格式", preset), field("格式表达式", format, "可手填格式 ID、ID+ID 或 yt-dlp 格式表达式。")),
      el("div", {class:"actions"}, discover, el("button", {type:"submit"}, "开始下载")), status);
    clearDiscovery();
    W.root.replaceChildren(W.heading("媒体下载器", "下载播客或视频 → 资产库 → 抽取音频 → STT → 总结分析"), form, formats, taskBox);
    const current = W.readCache("media:task");
    if (current?.id) taskBox.append(W.mediaTaskPanel({id:current.id, status:"queued"}));
  };
})();
