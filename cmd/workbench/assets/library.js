"use strict";
(() => {
  const W = window.WB, {el, button, field, input, select} = W;
  const assetKey = "library:assets", favoriteKey = "library:favorites", taskKey = "library:tasks";
  const read = key => {
    const value = W.readCache(key);
    return {rows: Array.isArray(value?.rows) ? value.rows : [], loaded: value?.loaded === true};
  };
  let assets = read(assetKey), favorites = read(favoriteKey), tasks = read(taskKey);
  const list = value => (Array.isArray(value) ? value : value ? [value] : []).filter(item => item?.id);
  const merge = (rows, additions) => {
    const merged = new Map(rows.map(item => [item.id, item]));
    for (const item of additions) merged.set(item.id, {...merged.get(item.id), ...item});
    return [...merged.values()].sort((a, b) => String(b.created_at || "").localeCompare(String(a.created_at || "")));
  };
  function saveAssets() {
    W.writeCache(assetKey, assets);
    W.writeCache(favoriteKey, favorites);
  }
  W.rememberAssets = value => {
    const additions = list(value);
    assets.rows = merge(assets.rows, additions);
    const changed = new Set(additions.map(item => item.id));
    favorites.rows = merge(favorites.rows.filter(item => !changed.has(item.id)), additions.filter(item => item.favorite));
    saveAssets();
  };
  function forgetAsset(id) {
    assets.rows = assets.rows.filter(item => item.id !== id);
    favorites.rows = favorites.rows.filter(item => item.id !== id);
    saveAssets();
  }
  function taskSummary(task) {
    const {result, native_events, ...summary} = task;
    return summary;
  }
  W.rememberTasks = value => {
    tasks.rows = merge(tasks.rows, list(value).map(taskSummary));
    W.writeCache(taskKey, tasks);
  };
  const assetURL = (id, download = false) => `/api/assets/${encodeURIComponent(id)}/content${download ? "?download=1" : ""}`;
  const assetByID = id => assets.rows.find(item => item.id === id) || favorites.rows.find(item => item.id === id);
  const mime = asset => String(asset?.content_type || "").split(";")[0].trim().toLowerCase();
  const safeImage = asset => /^image\/(png|jpeg|gif|webp|avif|bmp|x-icon|vnd.microsoft.icon)$/.test(mime(asset));
  const kind = asset => mime(asset).startsWith("image/") ? "图片" : mime(asset).startsWith("audio/") ? "音频" : mime(asset).startsWith("video/") ? "视频" : "文件";
  const bytes = value => !Number.isFinite(Number(value)) ? "—" : value < 1024 ? `${value} B` : value < 1048576 ? `${(value / 1024).toFixed(1)} KB` : `${(value / 1048576).toFixed(1)} MB`;
  const time = value => {
    if (!value) return "—";
    const date = new Date(typeof value === "number" ? value * 1000 : value);
    return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
  };
  const profileName = id => W.config?.providers?.find(profile => profile.id === id)?.name || id || "—";
  const sourceName = asset => asset.source?.kind === "yt-dlp" ? "yt-dlp · " + (asset.source.url || "下载") : asset.source?.kind === "ffmpeg" ? "ffmpeg 音频抽取 · " + (asset.source.asset_id || "") : asset.source?.kind === "upload" ? "本地上传" : asset.source?.provider_id ? `${profileName(asset.source.provider_id)} · ${asset.source.operation || "生成"}` : "生成结果";
  const downloadLink = asset => el("a", {href: assetURL(asset.id, true), class: "button quiet", download: asset.name || ""}, "下载");
  function thumbnail(asset) {
    return safeImage(asset)
      ? el("img", {class: "asset-thumbnail", src: assetURL(asset.id), alt: "", loading: "lazy", decoding: "async"})
      : el("span", {class: "asset-file-kind", "aria-hidden": "true"}, kind(asset));
  }
  function preview(asset) {
    const type = mime(asset), url = assetURL(asset.id);
    if (safeImage(asset)) return el("img", {class: "asset-preview", src: url, alt: asset.name || "图片", decoding: "async"});
    if (type.startsWith("audio/")) return el("audio", {class: "asset-preview", src: url, controls: "", preload: "metadata", "aria-label": asset.name || "音频预览"});
    if (type.startsWith("video/")) return el("video", {class: "asset-preview", src: url, controls: "", preload: "metadata", "aria-label": asset.name || "视频预览"});
    return W.notice("此文件类型不支持页面预览，可以下载后查看。");
  }
  async function showAsset(asset, trigger) {
    const modal = W.dialog("资产预览", trigger);
    modal.dialog.classList.add("asset-dialog");
    modal.body.replaceChildren(W.notice("正在读取资产详情…"));
    try {
      const current = await W.api(`/api/assets/${encodeURIComponent(asset.id)}`);
      W.rememberAssets(current);
      if (!modal.dialog.isConnected) return;
      modal.body.replaceChildren(el("div", {class: "asset-detail"},
        el("strong", {class: "asset-name"}, current.name), preview(current),
        el("p", {class: "small muted"}, `${kind(current)} · ${bytes(current.size)} · ${time(current.created_at)}`),
        el("p", {class: "small muted"}, sourceName(current)),
        el("div", {class: "actions"}, downloadLink(current), ...(W.mediaActions?.(current) || []), el("a", {href: "/library", target: "_blank", rel: "noopener", class: "button quiet"}, "管理资产"))));
    } catch (error) {
      if (modal.dialog.isConnected) modal.body.replaceChildren(W.notice(error.message, true));
    }
  }
  const assetDetails = new Map();
  function loadAsset(id) {
    const cached = assetByID(id);
    if (cached?.content_type) return Promise.resolve(cached);
    if (!assetDetails.has(id)) {
      const request = W.api(`/api/assets/${encodeURIComponent(id)}`).then(asset => {
        W.rememberAssets(asset);
        return asset;
      }).finally(() => assetDetails.delete(id));
      assetDetails.set(id, request);
    }
    return assetDetails.get(id);
  }
  W.assetResults = (value, {lazyMedia = false} = {}) => {
    const entries = (Array.isArray(value) ? value : value ? [value] : []).map(item => typeof item === "string" ? assetByID(item) || {id: item} : item).filter(item => item?.id);
    return el("div", {class: "asset-results"}, entries.map(asset => {
      const render = onLoaded => {
        const card = el("article", {class: "asset-result", "data-asset-id": asset.id});
        const show = current => {
          const open = button("预览", () => showAsset(current, open), "quiet");
          const playable = safeImage(current) || /^(audio|video)\//.test(mime(current));
          card.replaceChildren(playable ? preview(current) : el("span", {class: "asset-file-kind", "aria-hidden": "true"}, kind(current)),
            el("div", {class: "asset-result-info"}, el("strong", {class: "asset-name"}, current.name || "已保存的素材"),
              el("div", {class: "actions"}, open, downloadLink(current))));
          onLoaded?.(current);
        };
        if (asset.content_type) show(asset);
        else {
          card.append(W.notice("正在读取素材…"), downloadLink(asset));
          loadAsset(asset.id).then(show).catch(error => card.replaceChildren(W.notice(error.message, true), downloadLink(asset)));
        }
        return card;
      };
      if (!lazyMedia) return render();
      let label = asset.content_type ? kind(asset) : "资源";
      const text = el("span", {}, "展开" + label);
      const details = el("details", {class: "media-preview", "data-media-kind": label}, el("summary", {}, W.mediaPlaceholder(text)));
      let rendered = false;
      details.addEventListener("toggle", () => {
        text.textContent = (details.open ? "收起" : "展开") + label;
        if (details.open && !rendered) {
          details.append(el("div", {class: "media-content"}, render(current => {
            label = kind(current);
            details.dataset.mediaKind = label;
            text.textContent = (details.open ? "收起" : "展开") + label;
          })));
          rendered = true;
        }
      });
      return details;
    }));
  };

  W.library = () => {
    const search = input("asset_search"), filter = select("asset_filter", [["all", "全部资产"], ["favorites", "仅精选"]]);
    const type = select("asset_type", [["", "全部类型"], ["image/", "图片"], ["audio/", "音频"], ["video/", "视频"], ["other", "其他文件"]]);
    search.placeholder = "搜索名称、ID 或来源";
    const status = el("div", {}), tableBox = el("div", {class: "table-scroll"}), count = el("p", {class: "asset-count small muted", role: "status"});
    const panel = el("section", {class: "card resource-manager library-panel"});
    let busy = false;
    async function refreshList() {
      if (busy) return;
      busy = true;
      const unlock = W.lock(panel);
      status.replaceChildren(W.notice("正在读取资产列表…"));
      try {
        const rows = await W.api("/api/assets");
        if (!Array.isArray(rows)) throw new Error("资产列表格式无法识别");
        assets = {rows, loaded: true};
        favorites = {rows: rows.filter(asset => asset.favorite), loaded: true};
        saveAssets();
        status.replaceChildren();
      } catch (error) { status.replaceChildren(W.notice(error.message, true)); }
      finally { busy = false; unlock(); render(); }
    }
    const refresh = W.action("刷新", refreshList, "secondary", status);
    refresh.setAttribute("aria-label", "刷新资产");
    const upload = button("上传资源", () => uploadDialog(upload));
    const importSessions = W.action("导入旧会话媒体", async () => {
      const unlock = W.lock(panel);
      try {const added = await W.api("/api/assets/import-sessions", {method:"POST",body:"{}"});W.rememberAssets(added);render();status.replaceChildren(W.notice(`已导入 ${added.length} 个资源；已导入的会话不会重复处理。`));}
      finally {unlock();}
    }, "quiet", status);
    function uploadDialog(trigger) {
      const modal = W.dialog("上传资源", trigger), files = input("files", "", "file"), feedback = el("div", {});
      files.multiple = true;
      files.required = true;
      const form = el("form", {class: "asset-editor"}, field("本地文件", files),
        el("p", {class: "small muted"}, "文件保存到资产库。上传后可点击「精选」，再从 AI 页面选择使用。"), feedback);
      form.append(el("div", {class: "actions"}, button("返回", modal.close, "quiet"), el("button", {type: "submit"}, "上传")));
      form.addEventListener("submit", async event => {
        event.preventDefault();
        if (!form.reportValidity()) return;
        const data = new FormData();
        for (const file of files.files) data.append("files", file);
        const unlock = W.lock(form);
        modal.setBusy(true);
        feedback.replaceChildren(W.notice("正在上传…"));
        try {
          const uploaded = await W.api("/api/assets", {method: "POST", body: data});
          if (!Array.isArray(uploaded)) throw new Error("上传结果格式无法识别");
          W.rememberAssets(uploaded);
          render();
          status.replaceChildren(W.notice(`已上传 ${uploaded.length} 个文件。`));
          modal.close();
        } catch (error) { feedback.replaceChildren(W.notice(error.message, true)); }
        finally { unlock(); modal.setBusy(false); }
      });
      modal.body.append(form);
      files.focus();
    }
    function rename(asset, trigger) {
      const modal = W.dialog("重命名资产", trigger), name = input("asset_name", asset.name), feedback = el("div", {});
      name.required = true;
      name.maxLength = 255;
      const form = el("form", {class: "asset-editor"}, field("名称", name), feedback,
        el("div", {class: "actions"}, button("返回", modal.close, "quiet"), el("button", {type: "submit"}, "保存")));
      form.addEventListener("submit", async event => {
        event.preventDefault();
        if (!form.reportValidity()) return;
        const unlock = W.lock(form);
        modal.setBusy(true);
        try {
          const updated = await W.api(`/api/assets/${encodeURIComponent(asset.id)}`, {method: "PATCH", body: JSON.stringify({name: name.value.trim()})});
          W.rememberAssets(updated);
          render();
          modal.close();
          status.replaceChildren(W.notice("名称已更新。"));
          focusRow(asset.id, "重命名");
        } catch (error) { feedback.replaceChildren(W.notice(error.message, true)); }
        finally { unlock(); modal.setBusy(false); }
      });
      modal.body.append(form);
      name.focus();
      name.select();
    }
    function focusRow(id, label) {
      const row = [...tableBox.querySelectorAll("[data-asset-id]")].find(node => node.dataset.assetId === id);
      const target = row && [...row.querySelectorAll("button")].find(node => node.textContent === label);
      (target || search).focus();
    }
    function rowActions(asset) {
      const open = button("预览", () => showAsset(asset, open), "quiet");
      const edit = button("重命名", () => rename(asset, edit), "quiet");
      const featured = W.action(asset.favorite ? "取消精选" : "精选", async () => {
        if (busy) return;
        busy = true;
        const unlock = W.lock(panel);
        try {
          const updated = await W.api(`/api/assets/${encodeURIComponent(asset.id)}`, {method: "PATCH", body: JSON.stringify({favorite: !asset.favorite})});
          W.rememberAssets(updated);
          status.replaceChildren(W.notice(updated.favorite ? "已加入精选，可在 AI 页面选择。" : "已取消精选。"));
        } finally { busy = false; unlock(); render(); focusRow(asset.id, asset.favorite ? "精选" : "取消精选"); }
      }, "quiet", status);
      featured.setAttribute("aria-pressed", String(Boolean(asset.favorite)));
      const remove = W.action("删除", async () => {
        const completed = await W.confirm(`删除「${asset.name}」？`, async () => {
          await W.api(`/api/assets/${encodeURIComponent(asset.id)}`, {method: "DELETE"});
          forgetAsset(asset.id);
          render();
          status.replaceChildren(W.notice("资产已删除。"));
        }, "删除资产库中的文件。已保存结果中的这个素材引用将无法继续预览或下载。");
        if (completed) search.focus();
      }, "danger", status);
      return el("div", {class: "row-actions"}, open, downloadLink(asset), ...(W.mediaActions?.(asset) || []), featured, edit, remove);
    }
    function render() {
      const query = search.value.trim().toLowerCase();
      const rows = assets.rows.filter(asset => (filter.value !== "favorites" || asset.favorite)
        && (!type.value || (type.value === "other" ? !/^(image|audio|video)\//.test(mime(asset)) : mime(asset).startsWith(type.value)))
        && [asset.name, asset.id, sourceName(asset)].some(value => String(value || "").toLowerCase().includes(query)));
      tableBox.replaceChildren(el("table", {class: "resource-table library-table"},
        el("thead", {}, el("tr", {}, ["资产", "类型 / 大小", "来源 / 时间", "操作"].map(label => el("th", {scope: "col"}, label)))),
        el("tbody", {}, rows.map(asset => el("tr", {"data-asset-id": asset.id},
          el("td", {}, el("div", {class: "asset-cell"}, thumbnail(asset), el("div", {}, el("strong", {}, asset.name),
            el("small", {class: "resource-id"}, asset.id), asset.favorite ? el("span", {class: "badge"}, "精选") : null))),
          el("td", {}, el("span", {}, kind(asset)), el("small", {class: "resource-id"}, bytes(asset.size))),
          el("td", {}, sourceName(asset), el("small", {class: "resource-id"}, time(asset.created_at))), el("td", {}, rowActions(asset)))))));
      if (!rows.length) tableBox.append(el("div", {class: "empty"}, !assets.loaded && !assets.rows.length ? "尚未加载资产列表，点击「刷新」读取，或先上传文件。" : assets.rows.length ? "没有匹配的资产。" : "资产库为空，可以先上传文件。"));
      count.textContent = `${rows.length} 项${assets.loaded ? " · 当前标签页缓存，点击刷新读取最新列表" : " · 尚未读取完整列表，点击刷新查看全部资产"}`;
    }
    for (const control of [search, filter, type]) control.addEventListener(control === search ? "input" : "change", render);
    panel.append(el("div", {class: "library-toolbar"}, field("搜索资产", search), field("范围", filter), field("类型", type), refresh, upload), status, tableBox, count);
    W.root.replaceChildren(W.heading("资产库", "集中保存上传文件和生成素材。精选后，可在各个 AI 页面选择使用。", importSessions), panel);
    render();
  };

  function accepts(asset, accept) {
    const types = (Array.isArray(accept) ? accept : String(accept || "").split(",")).map(value => value.trim().toLowerCase()).filter(Boolean);
    return !types.length || types.some(type => type.endsWith("/*") ? mime(asset).startsWith(type.slice(0, -1)) : type.endsWith("/") ? mime(asset).startsWith(type) : type.startsWith(".") ? String(asset.name || "").toLowerCase().endsWith(type) : mime(asset) === type);
  }
  W.pickAssets = ({accept = "", multiple = false, selected = [], label = "选择精选资产"} = {}) => new Promise(resolve => {
    const modal = W.dialog("选择精选资产"), search = input("favorite_search"), feedback = el("div", {}), rowsBox = el("div", {class: "asset-picker-list"});
    modal.dialog.classList.add("asset-dialog");
    search.placeholder = "搜索精选名称或 ID";
    const selectedIDs = new Set(selected.map(item => typeof item === "string" ? item : item.id));
    let result = null, busy = false;
    const chosen = () => favorites.rows.filter(asset => asset.favorite && accepts(asset, accept) && selectedIDs.has(asset.id));
    const use = button("使用所选", () => { if (busy || !chosen().length) return; result = multiple ? chosen() : chosen().slice(0, 1); modal.close(); });
    const refresh = W.action("刷新精选", async () => {
      if (busy) return;
      busy = true;
      const unlock = W.lock(modal.body);
      modal.setBusy(true);
      feedback.replaceChildren(W.notice("正在读取精选资产…"));
      try {
        const rows = await W.api("/api/assets?favorite=1");
        if (!Array.isArray(rows)) throw new Error("精选列表格式无法识别");
        const previous = new Set(favorites.rows.map(asset => asset.id));
        const ids = new Set(rows.map(asset => asset.id));
        assets.rows = assets.rows.map(asset => previous.has(asset.id) && !ids.has(asset.id) ? {...asset, favorite: false} : asset);
        assets.rows = merge(assets.rows, rows);
        favorites = {rows: rows.filter(asset => asset.favorite), loaded: true};
        saveAssets();
        feedback.replaceChildren();
      } catch (error) { feedback.replaceChildren(W.notice(error.message, true)); }
      finally { busy = false; unlock(); modal.setBusy(false); render(); }
    }, "secondary", feedback);
    function render() {
      const query = search.value.trim().toLowerCase();
      const available = favorites.rows.filter(asset => asset.favorite && accepts(asset, accept));
      const shown = available.filter(asset => [asset.name, asset.id].some(value => String(value || "").toLowerCase().includes(query)));
      rowsBox.replaceChildren(...shown.map(asset => {
        const control = input("favorite_asset", asset.id, multiple ? "checkbox" : "radio");
        control.checked = selectedIDs.has(asset.id);
        control.setAttribute("aria-label", asset.name || asset.id);
        control.addEventListener("change", () => {
          if (!multiple) selectedIDs.clear();
          if (control.checked) selectedIDs.add(asset.id); else selectedIDs.delete(asset.id);
          use.disabled = chosen().length === 0;
          count.textContent = `已选 ${chosen().length} 项`;
        });
        const open = button("预览", () => showAsset(asset, open), "quiet");
        return el("div", {class: "asset-picker-row", "data-asset-id": asset.id},
          el("label", {class: "asset-picker-choice"}, control, thumbnail(asset),
            el("span", {class: "asset-picker-info"}, el("strong", {class: "asset-name"}, asset.name), el("small", {class: "muted"}, `${kind(asset)} · ${bytes(asset.size)}`))), open);
      }));
      if (!shown.length) rowsBox.append(el("p", {class: "empty"}, !favorites.loaded && !favorites.rows.length ? "尚未加载精选，点击「刷新精选」读取。" : available.length ? "没有匹配的精选资产。" : "还没有符合类型的精选资产，请到资产库添加精选后手动刷新。"));
      cacheNote.textContent = favorites.loaded ? "显示当前标签页缓存；在资产库修改精选后，请点击「刷新精选」。" : "尚未读取完整精选列表；点击「刷新精选」查看最新内容。";
      count.textContent = `已选 ${chosen().length} 项`;
      use.disabled = chosen().length === 0;
    }
    const count = el("span", {class: "small muted", role: "status"}), cacheNote = el("p", {class: "small muted"});
    modal.body.append(el("div", {class: "asset-picker"}, el("p", {class: "small muted"}, `${label} · ${multiple ? "可多选" : "单选"}`),
      el("div", {class: "library-toolbar"}, field("搜索精选", search), refresh,
        el("a", {href: "/library", target: "_blank", rel: "noopener", class: "button quiet"}, "管理资产（新标签页）")),
      cacheNote, feedback, rowsBox, el("div", {class: "actions asset-picker-footer"}, count, button("返回", modal.close, "quiet"), use)));
    modal.dialog.addEventListener("close", () => resolve(result), {once: true});
    search.addEventListener("input", render);
    render();
    search.focus();
  });
  W.assetControl = ({label = "选择资产", accept = "", multiple = false, selected = [], onChange} = {}) => {
    let chosen = selected.map(item => typeof item === "string" ? assetByID(item) : item).filter(item => item?.id);
    const selections = el("div", {class: "asset-selection"}), node = el("div", {class: "asset-control"});
    const choose = W.action(label, async () => {
      const result = await W.pickAssets({accept, multiple, selected: chosen, label});
      if (result !== null) setAssets(result);
    }, "secondary", node);
    function render() {
      selections.replaceChildren(...chosen.map(asset => {
        const remove = button("×", () => { setAssets(chosen.filter(item => item.id !== asset.id)); choose.focus(); }, "quiet");
        remove.setAttribute("aria-label", "移除 " + (asset.name || asset.id));
        return el("span", {class: "asset-selection-item"}, el("span", {class: "asset-name"}, asset.name || asset.id), remove);
      }));
    }
    function setAssets(value) {
      chosen = list(value).filter(asset => accepts(asset, accept));
      if (!multiple) chosen = chosen.slice(0, 1);
      render();
      onChange?.([...chosen]);
    }
    node.append(choose, selections);
    render();
    return {node, getIDs: () => chosen.map(asset => asset.id), getAssets: () => [...chosen], setAssets};
  };

  const taskStatus = {queued: "排队中", running: "运行中", complete: "已完成", error: "失败", cancelled: "已取消", interrupted: "已中断"};
  const isRunning = task => task.status === "queued" || task.status === "running";
  W.taskResult = task => {
    const ids = task.asset_ids || [], result = el("div", {class: "library-task-result"});
    if (task.error) result.append(W.notice(task.error, true));
    if (task.result !== undefined && task.result !== null) result.append(W.result(task.result, {skipMedia: ids.length > 0}));
    if (ids.length) result.append(W.assetResults(task.assets || ids));
    if (isRunning(task)) result.append(W.notice("任务在后台运行，可离开此页。点击「刷新任务」查看最新状态。"));
    else if (task.result == null && !ids.length && !task.error) result.append(W.notice(taskStatus[task.status] || "暂无结果"));
    return result;
  };
  W.tasks = () => {
    const query = new URLSearchParams(location.search), search = input("task_search");
    const profileValues = new Map((W.config?.providers || []).map(profile => [profile.id, profile.name]));
    for (const id of [...tasks.rows.map(task => task.provider_id), query.get("provider_id")].filter(Boolean)) if (!profileValues.has(id)) profileValues.set(id, id);
    const profiles = select("task_profile", [["", "全部 profiles"], ...profileValues], query.get("provider_id") || "");
    const state = select("task_status", [["", "全部状态"], ...Object.entries(taskStatus)]);
    const featureNames = {download: "媒体下载", "extract-audio": "抽取音频", chat: "文字", image: "图片生成", "image-edit": "图片编辑", speech: "语音合成", transcribe: "音频转写", translate: "音频翻译", video: "视频"};
    const featureValues = new Set([...Object.keys(featureNames), ...tasks.rows.map(task => task.feature), query.get("feature")].filter(Boolean));
    const feature = select("task_feature", [["", "全部功能"], ...[...featureValues].map(value => [value, featureNames[value] || value])], query.get("feature") || "");
    const status = el("div", {}), tableBox = el("div", {class: "table-scroll"}), count = el("p", {class: "asset-count small muted", role: "status"});
    const panel = el("section", {class: "card resource-manager library-panel"});
    search.placeholder = "搜索任务名称、ID 或功能";
    let busy = false;
    const refresh = W.action("刷新任务", async () => {
      if (busy) return;
      busy = true;
      const unlock = W.lock(panel);
      status.replaceChildren(W.notice("正在读取后台任务…"));
      try {
        const rows = await W.api("/api/tasks");
        if (!Array.isArray(rows)) throw new Error("任务列表格式无法识别");
        tasks = {rows: rows.map(taskSummary), loaded: true};
        W.writeCache(taskKey, tasks);
        status.replaceChildren();
      } catch (error) { status.replaceChildren(W.notice(error.message, true)); }
      finally { busy = false; unlock(); render(); }
    }, "secondary", status);
    async function cancel(task, changed) {
      let updated;
      const completed = await W.confirm("取消这个后台任务？", async () => {
        updated = await W.api(`/api/tasks/${encodeURIComponent(task.id)}/cancel`, {method: "POST", body: "{}"});
        W.rememberTasks(updated);
        render();
        status.replaceChildren(W.notice("取消请求已提交。"));
      }, "停止本地请求并保存已有结果；已提交到服务商的视频或 Batch 不会因此取消。");
      if (completed) {
        if (changed) changed(updated); else search.focus();
      }
      return completed;
    }
    async function detail(task, trigger) {
      const modal = W.dialog("任务详情", trigger);
      modal.dialog.classList.add("library-task-dialog");
      modal.dialog.addEventListener("close", () => {
        const row = [...tableBox.querySelectorAll("[data-task-id]")].find(node => node.dataset.taskId === task.id);
        (row?.querySelector("button") || search).focus();
      }, {once: true});
      async function loadDetail() {
        const unlock = W.lock(modal.body);
        modal.setBusy(true);
        try {
          const current = await W.api(`/api/tasks/${encodeURIComponent(task.id)}`);
          W.rememberTasks(current);
          render();
          show(current);
        } catch (error) { modal.body.replaceChildren(W.notice(error.message, true), W.action("重试", loadDetail, "secondary", modal.body)); }
        finally { unlock(); modal.setBusy(false); }
      }
      function show(current) {
        if (!modal.dialog.isConnected) return;
        const actions = el("div", {class: "actions"}, W.action("刷新任务", loadDetail, "secondary", modal.body));
        if (isRunning(current)) actions.append(W.action("取消任务", () => cancel(current, show), "danger", modal.body));
        if (current.session_id && current.vendor && current.provider_id) actions.append(el("a", {href: `/ai/${encodeURIComponent(current.vendor)}/${encodeURIComponent(current.provider_id)}/chat?session=${encodeURIComponent(current.session_id)}&operation=${encodeURIComponent(current.operation || "")}`, class: "button quiet"}, "打开会话"));
        modal.body.replaceChildren(el("div", {class: "library-task-detail"},
          el("strong", {class: "asset-name"}, current.title || current.operation || current.id),
          el("p", {class: "small muted"}, `${taskStatus[current.status] || current.status} · ${profileName(current.provider_id)} · ${time(current.updated_at || current.created_at)}`),
          actions, W.taskResult(current)));
        actions.querySelector("button").focus();
      }
      modal.body.append(W.notice("正在读取任务详情…"));
      await loadDetail();
    }
    function render() {
      const needle = search.value.trim().toLowerCase();
      for (const task of tasks.rows) if (task.feature && !featureValues.has(task.feature)) {
        featureValues.add(task.feature);
        feature.append(el("option", {value: task.feature}, featureNames[task.feature] || task.feature));
      }
      for (const task of tasks.rows) if (task.provider_id && !profileValues.has(task.provider_id)) {
        profileValues.set(task.provider_id, task.provider_id);
        profiles.append(el("option", {value: task.provider_id}, task.provider_id));
      }
      const rows = tasks.rows.filter(task => (!profiles.value || task.provider_id === profiles.value) && (!state.value || task.status === state.value) && (!feature.value || task.feature === feature.value)
        && [task.title, task.id, task.operation, task.feature].some(value => String(value || "").toLowerCase().includes(needle)));
      tableBox.replaceChildren(el("table", {class: "resource-table library-table"},
        el("thead", {}, el("tr", {}, ["任务", "Profile / 功能", "状态 / 时间", "操作"].map(label => el("th", {scope: "col"}, label)))),
        el("tbody", {}, rows.map(task => {
          const open = button("详情", () => detail(task, open), "quiet");
          const actions = el("div", {class: "row-actions"}, open);
          if (isRunning(task)) actions.append(W.action("取消任务", () => cancel(task), "danger", status));
          else actions.append(W.action("删除", async () => {
            const completed = await W.confirm("删除这个任务记录？", async () => {
              await W.api(`/api/tasks/${encodeURIComponent(task.id)}`, {method: "DELETE"});
              tasks.rows = tasks.rows.filter(item => item.id !== task.id);
              W.writeCache(taskKey, tasks);
              render();
              status.replaceChildren(W.notice("任务记录已删除。"));
            }, "删除这条本地任务记录。生成的素材仍保留在资产库中。");
            if (completed) search.focus();
          }, "danger", status));
          return el("tr", {"data-task-id": task.id}, el("td", {}, el("strong", {}, task.title || task.operation || task.id), el("small", {class: "resource-id"}, task.id)),
            el("td", {}, profileName(task.provider_id), el("small", {class: "resource-id"}, featureNames[task.feature] || task.feature || task.operation)),
            el("td", {}, el("span", {class: "badge", "data-task-status": task.status}, taskStatus[task.status] || task.status), el("small", {class: "resource-id"}, time(task.updated_at || task.created_at))), el("td", {}, actions));
        }))));
      if (!rows.length) tableBox.append(el("div", {class: "empty"}, !tasks.loaded && !tasks.rows.length ? "尚未加载后台任务，点击「刷新任务」读取。" : tasks.rows.length ? "没有匹配的任务。" : "还没有后台任务。"));
      count.textContent = `${rows.length} 项 · ${tasks.loaded ? "当前标签页缓存，手动刷新查看进度" : "尚未读取完整列表，手动刷新查看全部任务"}`;
    }
    for (const control of [search, profiles, state, feature]) control.addEventListener(control === search ? "input" : "change", render);
    panel.append(el("div", {class: "library-toolbar"}, field("搜索任务", search), field("Profile", profiles), field("功能", feature), field("状态", state), refresh), status, tableBox, count);
    W.root.replaceChildren(W.heading("后台任务", "离开页面后任务继续执行，结果保存在本机。手动刷新查看最新进度。"), panel);
    render();
  };
})();
