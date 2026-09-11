"use strict";

(() => {
  const css = document.createElement("link");
  css.rel = "stylesheet";
  css.href = "/assets/workbench.css";
  document.head.append(css);

  const root = document.querySelector("#app");
  const shell = document.querySelector(".app-shell");
  if (!root || !shell) return;
  const page = shell.dataset.page || "chat";
  const state = { config: null, sessions: [], session: null, operations: [], sending: false, controller: null, chatDrafts: new Map(), collapsed: new Set() };

  const el = (tag, attrs, ...children) => {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(attrs || {})) {
      if (value === undefined || value === null || value === false) continue;
      if (key === "class") node.className = value;
      else if (key === "text") node.textContent = value;
      else if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2), value);
      else if (key === "checked" || key === "disabled" || key === "open" || key === "required" || key === "multiple") node[key] = Boolean(value);
      else node.setAttribute(key, String(value));
    }
    for (const child of children.flat(Infinity)) {
      if (child === undefined || child === null || child === false) continue;
      node.append(child instanceof Node ? child : document.createTextNode(String(child)));
    }
    return node;
  };
  const clear = node => node.replaceChildren();
  const button = (label, action, kind = "") => el("button", { type: "button", class: kind, onclick: action }, label);
  const heading = (title, copy, actions) => el("div", { class: "page-heading" },
    el("div", {}, el("h1", {}, title), el("p", {}, copy)), actions ? el("div", { class: "actions" }, actions) : null);
  const field = (label, control, help) => el("label", { class: "field" }, el("span", {}, label), control, help ? el("small", { class: "muted" }, help) : null);
  const checkbox = (label, checked = false, attrs = {}) => el("label", { class: "check" }, el("input", { type: "checkbox", checked, ...attrs }), el("span", {}, label));
  const notice = (message, isError = false) => el("div", { class: "notice" + (isError ? " error" : ""), role: isError ? "alert" : "status" }, message);
  const identifier = prefix => {
    if (typeof crypto.randomUUID === "function") return prefix + "-" + crypto.randomUUID();
    const bytes = new Uint8Array(16); crypto.getRandomValues(bytes);
    return prefix + "-" + [...bytes].map(value => value.toString(16).padStart(2, "0")).join("");
  };
  const pretty = value => JSON.stringify(value, null, 2);
  const time = value => value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "medium" }).format(new Date(value)) : "—";
  const errorText = async response => {
    try { const body = await response.json(); return body.error || response.statusText; }
    catch { return response.statusText || `请求失败（${response.status}）`; }
  };
  async function api(path, options = {}) {
    const response = await fetch(path, { credentials: "same-origin", cache: "no-store", ...options,
      headers: options.body instanceof FormData ? options.headers : { "Content-Type": "application/json", ...(options.headers || {}) } });
    if (!response.ok) throw new Error(await errorText(response));
    if (response.status === 204) return null;
    return response.json();
  }
  async function loadConfig() { state.config = await api("/api/config"); return state.config; }
  function fail(error, target = root) {
    const current = target.querySelector("[data-error]");
    if (current) current.remove();
    target.prepend(el("div", { "data-error": "" }, notice(error.message || String(error), true)));
  }

  const labels = {
    id: "资源 ID", model: "模型", input: "输入", prompt: "提示词", purpose: "用途", filename: "文件名",
    container_id: "容器 ID", file_id: "文件 ID", batch_id: "批处理 ID", response_id: "响应 ID",
    endpoint: "端点", completion_window: "完成时限", size: "尺寸", quality: "质量", format: "格式",
    voice: "声音", instructions: "指令", metadata: "元数据", expires_after: "过期设置",
  };
  const friendlyLabel = key => labels[key] || key.replaceAll("_", " ");
  const operationVerbs = { upload: "上传", list: "列出", get: "查看", delete: "删除", download: "下载", create: "创建", add: "添加", cancel: "取消", stream: "流式执行", count: "计数", compact: "压缩", generate: "生成", edit: "编辑", speech: "语音合成", speech_stream: "流式语音", transcribe: "转写", transcribe_stream: "流式转写", translate: "翻译" };
  function operationTitle(operation) {
    const tail = operation.id.split(".").at(-1);
    const nouns = { files: "文件", containers: "容器", batches: "批处理", responses: "响应", chat: "Chat", images: "图片", audio: "音频" };
    return `${operationVerbs[tail] || operation.label || tail}${nouns[operation.group] || ""}`;
  }

  async function initSettings() {
    await loadConfig();
    renderSettings();
  }
  function configDraft() {
    const providers = [...root.querySelectorAll("[data-provider]")].map(card => ({
      id: card.dataset.provider,
      name: card.querySelector("[name=name]").value.trim(),
      base_url: card.querySelector("[name=base_url]").value.trim(),
      proxy_url: card.querySelector("[name=proxy_url]").value.trim(),
      api_key: card.querySelector("[name=api_key]").value,
      clear_key: card.querySelector("[name=clear_key]").checked,
      has_key: card.dataset.hasKey === "true",
      models: (card.dataset.models || "").split("\n").filter(Boolean),
    }));
    const models = [...root.querySelectorAll("[data-model]")].map(card => ({
      id: card.querySelector("[name=model_id]").value.trim(),
      name: card.querySelector("[name=model_name]").value.trim(),
      featured: card.querySelector("[name=featured]").checked,
      routes: [...card.querySelectorAll("[data-route-row]")].map(row => ({
        provider_id: row.querySelector("[name=provider_id]").value,
        model: row.querySelector("[name=route_model]").value.trim(),
        protocol: row.querySelector("[name=protocol]").value,
      })),
    }));
    return { revision: state.config.revision, providers, models,
      save_responses: root.querySelector("[name=save_responses]").checked };
  }
  function providerCard(provider) {
    const card = el("section", { class: "card provider-card", "data-provider": provider.id, "data-has-key": String(Boolean(provider.has_key)), "data-models": (provider.models || []).join("\n") });
    const name = el("input", { name: "name", value: provider.name || "", "aria-label": "服务商名称", required: true });
    const baseURL = el("input", { name: "base_url", value: provider.base_url || "", placeholder: "https://api.openai.com/v1", required: true });
    const proxy = el("input", { name: "proxy_url", value: provider.proxy_url || "", placeholder: "留空继承环境代理；填写 - 强制直连" });
    const key = el("input", { name: "api_key", type: "password", autocomplete: "new-password", placeholder: provider.has_key ? "已保存；留空保持不变" : "输入 API key" });
    const clearKey = checkbox("删除已保存的密钥", false);
    clearKey.querySelector("input").name = "clear_key";
    const catalog = el("pre", { class: "catalog" }, (provider.models || []).join("\n") || "尚未发现模型");
    const discover = button("发现全部模型", async () => {
      const status = card.querySelector("[data-card-status]");
      discover.disabled = true; status.textContent = "正在读取模型目录…";
      try {
        state.config = await api("/api/config", { method: "PUT", body: JSON.stringify(configDraft()) });
        await api(`/api/providers/${encodeURIComponent(provider.id)}/discover`, { method: "POST", body: "{}" });
        await loadConfig(); renderSettings();
      } catch (error) { status.textContent = ""; fail(error, card); }
      finally { discover.disabled = false; }
    }, "secondary");
    discover.textContent = "保存并发现模型";
    card.append(
      el("div", { class: "section-head" }, el("div", {}, el("h3", {}, provider.name || "新服务商"), el("span", { class: "badge " + (provider.has_key ? "good" : "") }, provider.has_key ? "密钥已保存" : "未设置密钥")),
        button("移除", () => card.remove(), "danger")),
      el("div", { class: "grid-2" }, field("服务商名称", name), field("API 前缀", baseURL), field("HTTP 代理", proxy), field("API key", key, "页面不会读取已经保存的明文密钥。")),
      el("div", { class: "actions" }, clearKey, discover, el("span", { class: "muted small", "data-card-status": "", role: "status" })),
      el("div", { class: "field" }, el("span", {}, `模型目录 · ${(provider.models || []).length} 个`), catalog),
    );
    return card;
  }
  function routeRow(route = {}) {
    const row = el("div", { class: "route-row", "data-route-row": "" });
    const provider = el("select", { name: "provider_id" }, el("option", { value: "" }, "选择服务商"));
    for (const p of state.config.providers) provider.append(el("option", { value: p.id }, p.name || p.id));
    provider.value = route.provider_id || "";
    const model = el("input", { name: "route_model", value: route.model || "", list: "provider-models", placeholder: "实际模型名" });
    const protocol = el("select", { name: "protocol" }, el("option", { value: "responses" }, "Responses"), el("option", { value: "chat" }, "Chat Completions"));
    protocol.value = route.protocol || "responses";
    row.append(field("服务商", provider), field("实际模型", model), field("协议", protocol),
      el("div", { class: "actions" }, button("上移", () => { const prev = row.previousElementSibling; if (prev?.matches("[data-route-row]")) prev.before(row); }, "quiet"), button("下移", () => { const next = row.nextElementSibling; if (next?.matches("[data-route-row]")) next.after(row); }, "quiet"), button("删除", () => row.remove(), "danger")));
    return row;
  }
  function modelCard(model) {
    const card = el("section", { class: "card model-card", "data-model": model.id });
    const routes = el("div", { "data-routes": "" }, ...(model.routes || []).map(routeRow));
    card.append(
      el("div", { class: "section-head" }, el("h3", {}, model.name || "新逻辑模型"), button("移除", () => card.remove(), "danger")),
      el("div", { class: "grid-2" }, field("显示名称", el("input", { name: "model_name", value: model.name || "", required: true })),
        field("模型 ID", el("input", { name: "model_id", value: model.id, required: true, pattern: "[A-Za-z0-9_-]{1,100}" }), "保存后作为稳定调用别名。")),
      checkbox("在对话页精选展示", Boolean(model.featured), { name: "featured" }),
      el("div", { class: "section-head" }, el("div", {}, el("strong", {}, "有序线路"), el("p", { class: "muted small" }, "第一条线路为默认线路；工作台不会自动切换。")), button("添加线路", () => routes.append(routeRow()), "secondary")), routes,
    );
    return card;
  }
  function renderSettings() {
    clear(root);
    const providers = el("div", { class: "stack", id: "providers" }, ...state.config.providers.map(providerCard));
    const models = el("div", { class: "stack", id: "models" }, ...state.config.models.map(modelCard));
    const datalist = el("datalist", { id: "provider-models" });
    for (const p of state.config.providers) for (const model of p.models || []) datalist.append(el("option", { value: model }, `${p.name}: ${model}`));
    const save = button("保存全部设置", async () => {
      save.disabled = true;
      try {
        state.config = await api("/api/config", { method: "PUT", body: JSON.stringify(configDraft()) });
        renderSettings(); root.prepend(notice("设置已保存。"));
      } catch (error) { fail(error); }
      finally { save.disabled = false; }
    });
    root.append(heading("设置", "管理服务商、密钥和对话使用的逻辑模型。密钥字段始终只写不读。", save), datalist,
      el("section", { class: "card" }, el("div", { class: "section-head" }, el("div", {}, el("h2", {}, "服务商"), el("p", { class: "muted" }, "模型发现会保存完整目录，也可在线路中手工填写模型。")), button("添加服务商", () => providers.append(providerCard({ id: identifier("provider"), models: [] })), "secondary")), providers),
      el("section", { class: "card" }, el("div", { class: "section-head" }, el("div", {}, el("h2", {}, "逻辑模型"), el("p", { class: "muted" }, "别名与线路分离，便于固定常用入口。")), button("添加逻辑模型", () => models.append(modelCard({ id: identifier("model"), routes: [], featured: true })), "secondary")), models),
      el("section", { class: "card" }, el("h2", {}, "日志正文"), checkbox("默认保存 provider 响应正文", Boolean(state.config.save_responses), { name: "save_responses" }), el("p", { class: "muted small" }, "请求正文和响应元数据始终记录；敏感认证头会脱敏。")),
    );
  }

  async function initChat() {
    [state.config, state.sessions] = await Promise.all([api("/api/config"), api("/api/sessions")]);
    if (state.sessions[0]) state.session = await api(`/api/sessions/${state.sessions[0].id}`);
    renderChat();
  }
  function featuredModels() { return state.config.models.filter(model => model.featured && model.routes?.length); }
  async function chooseSession(id) { if (state.sending) return; state.session = await api(`/api/sessions/${id}`); renderChat(); }
  async function refreshSessions(selectID) {
    state.sessions = await api("/api/sessions");
    if (selectID) state.session = await api(`/api/sessions/${selectID}`);
    renderChat();
  }
  function renderMessage(message, currentHead) {
    const details = el("details", { class: `message ${message.role}`, open: !state.collapsed.has(message.id) });
    details.addEventListener("toggle", () => details.open ? state.collapsed.delete(message.id) : state.collapsed.add(message.id));
    const status = message.status === "complete" ? "完成" : ({ pending: "生成中", error: "失败", cancelled: "已取消" }[message.status] || message.status);
    const generated = generatedImages(message.output);
    details.append(el("summary", {}, el("strong", {}, message.role === "user" ? "你" : "助手"), el("span", { class: "badge" }, status), el("span", {}, time(message.created_at))),
      el("div", { class: "message-text" }, message.text || ""), generated.length ? el("div", { class: "generated-assets" }, ...generated.map((src, index) => el("img", { src, alt: `生成图片 ${index + 1}`, loading: "lazy" }))) : null,
      el("div", { class: "message-meta" },
        message.model_id ? el("span", {}, state.config.models.find(model => model.id === message.model_id)?.name || "已删除的逻辑模型") : null,
        message.provider_id ? el("details", {}, el("summary", {}, "线路详情"), el("span", {}, `${message.provider_id} · ${message.actual_model || ""} · ${message.protocol || ""}`)) : null,
        message.error ? el("span", { class: "notice error" }, message.error) : null,
        el("button", { type: "button", class: message.id === currentHead ? "secondary" : "quiet", disabled: state.sending, onclick: () => { if (!state.sending) { state.session.head_id = message.id; renderChat(); } } }, "从这里继续"),
        el("button", { type: "button", class: "quiet", disabled: state.sending, onclick: async () => {
          try { const fork = await api(`/api/sessions/${state.session.id}/fork`, { method: "POST", body: JSON.stringify({ node_id: message.id, title: state.session.title + " · 分支" }) }); await refreshSessions(fork.id); }
          catch (error) { fail(error); }
        } }, "复制为新会话"),
        message.output !== undefined ? el("details", {}, el("summary", {}, "原生输出"), el("pre", { class: "operation-output" }, pretty(message.output))) : null));
    return details;
  }
  function generatedImages(value) {
    const images = [];
    const visit = item => {
      if (!item || images.length >= 12) return;
      if (Array.isArray(item)) { item.forEach(visit); return; }
      if (typeof item !== "object") return;
      if (item.type === "image_generation_call" && typeof item.result === "string" && /^[A-Za-z0-9+/]+={0,2}$/.test(item.result)) images.push("data:image/png;base64," + item.result);
      Object.values(item).forEach(visit);
    };
    visit(value);
    return images;
  }
  function renderChat() {
    clear(root);
    const modelOptions = featuredModels();
    const draftKey = state.session?.id || "__new__";
    const draft = state.chatDrafts.get(draftKey) || {};
    const modelSelect = el("select", { "aria-label": "对话模型", name: "model_id" }, ...modelOptions.map(model => el("option", { value: model.id, "data-protocol": model.routes[0].protocol }, model.name)));
    if (draft.model && modelOptions.some(model => model.id === draft.model)) modelSelect.value = draft.model;
    const web = checkbox("联网搜索", false); const image = checkbox("图片生成", false); const exec = checkbox("代码执行", false);
    web.querySelector("input").checked = Boolean(draft.web); image.querySelector("input").checked = Boolean(draft.image); exec.querySelector("input").checked = Boolean(draft.exec);
    const webType = el("input", { value: draft.webType || "web_search", placeholder: "web_search" });
    const imageType = el("input", { value: draft.imageType || "image_generation", placeholder: "image_generation" });
    const imageModel = el("input", { value: draft.imageModel || "", placeholder: "可选图片模型" });
    const containerID = el("input", { value: draft.containerID || "", placeholder: "留空自动创建" });
    const files = el("input", { value: draft.files || "", placeholder: "file_1, file_2" });
    const system = el("textarea", { rows: "2", placeholder: "可选系统指令" }, draft.system || "");
    const options = el("textarea", { rows: "5", spellcheck: "false" }, draft.options || "{}");
    const saveResponse = checkbox("本次保存响应正文", draft.saveResponse ?? Boolean(state.config.save_responses));
    const prompt = el("textarea", { "aria-label": "消息", rows: "3", placeholder: modelOptions.length ? "输入消息，Ctrl / ⌘ + Enter 发送" : "先在设置中配置精选模型", disabled: !modelOptions.length }, draft.text || "");
    const send = button("发送", () => sendMessage());
    const stop = button("停止", () => state.controller?.abort(), "danger"); stop.hidden = true;
    const updateTools = () => {
      const selected = modelOptions.find(m => m.id === modelSelect.value);
      const disabled = selected?.routes?.[0]?.protocol === "chat";
      for (const input of [web.querySelector("input"), image.querySelector("input"), exec.querySelector("input")]) { input.disabled = disabled; if (disabled) input.checked = false; }
    };
    modelSelect.addEventListener("change", updateTools); updateTools();
    prompt.addEventListener("keydown", event => { if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) { event.preventDefault(); sendMessage(); } });
    const sessionList = el("div", { class: "session-list" }, ...state.sessions.map(session => el("div", { class: "session-item " + (state.session?.id === session.id ? "active" : "") },
      el("button", { type: "button", class: "quiet", disabled: state.sending, onclick: () => chooseSession(session.id) }, session.title || "未命名会话"),
      button("…", async () => {
        const title = window.prompt("新的会话名称", session.title);
        if (title === null || !title.trim()) return;
        try { await api(`/api/sessions/${session.id}`, { method: "PATCH", body: JSON.stringify({ title: title.trim() }) }); await refreshSessions(state.session?.id); } catch (error) { fail(error); }
      }, "quiet"),
      button("×", async () => {
        if (!window.confirm(`删除“${session.title}”？`)) return;
        try { await api(`/api/sessions/${session.id}`, { method: "DELETE", body: "{}" }); state.session = null; await refreshSessions(); } catch (error) { fail(error); }
      }, "danger"))));
    const messages = el("div", { class: "messages", id: "messages" }, ...(state.session?.messages || []).map(message => renderMessage(message, state.session.head_id)));
    if (!state.session) messages.append(el("div", { class: "empty" }, el("h2", {}, "从一个新会话开始"), el("p", {}, "建立会话后即可沿任意消息节点继续或分支。")));
    const composer = el("section", { class: "card composer" },
      el("div", { class: "grid-2" }, field("对话模型", modelSelect), field("文件 ID", files, "多个 ID 用逗号分隔。")),
      field("消息", prompt),
      el("div", { class: "tool-grid" }, web, image, exec),
      el("details", { class: "advanced" }, el("summary", {}, "工具版本与原生参数"), el("div", { class: "grid-2" }, field("Web tool type", webType), field("Image tool type", imageType), field("Image model", imageModel), field("容器 ID", containerID)), field("系统指令", system), field("原生 JSON options", options, "对象内容将合并进核心请求。")),
      el("div", { class: "actions" }, saveResponse, send, stop, el("span", { class: "muted small", role: "status", "data-send-status": "" })));
    const captureDraft = () => state.chatDrafts.set(state.session?.id || draftKey, { model: modelSelect.value, text: prompt.value, files: files.value, system: system.value, options: options.value,
      web: web.querySelector("input").checked, image: image.querySelector("input").checked, exec: exec.querySelector("input").checked, webType: webType.value, imageType: imageType.value,
      imageModel: imageModel.value, containerID: containerID.value, saveResponse: saveResponse.querySelector("input").checked });
    for (const control of composer.querySelectorAll("input,select,textarea")) { control.addEventListener("input", captureDraft); control.addEventListener("change", captureDraft); }
    async function sendMessage() {
      if (state.sending || !prompt.value.trim()) return;
      state.sending = true; captureDraft();
      try {
        if (!state.session) {
          state.session = await api("/api/sessions", { method: "POST", body: JSON.stringify({ title: prompt.value.trim().slice(0, 36) }) });
          state.chatDrafts.set(state.session.id, state.chatDrafts.get("__new__")); state.chatDrafts.delete("__new__");
        }
        let nativeOptions;
        try { nativeOptions = JSON.parse(options.value || "{}"); } catch { throw new Error("原生 JSON options 不是有效对象。"); }
        if (!nativeOptions || Array.isArray(nativeOptions) || typeof nativeOptions !== "object") throw new Error("原生 JSON options 必须是对象。");
        const body = { parent_id: state.session.head_id || "", model_id: modelSelect.value, text: prompt.value, system: system.value,
          files: files.value.split(",").map(v => v.trim()).filter(Boolean),
          tools: { web: web.querySelector("input").checked, image: image.querySelector("input").checked, exec: exec.querySelector("input").checked,
            web_type: webType.value, image_type: imageType.value, image_model: imageModel.value, container_id: containerID.value },
          options: nativeOptions, save_response: saveResponse.querySelector("input").checked };
        state.controller = new AbortController(); send.disabled = true; stop.hidden = false;
        for (const action of root.querySelectorAll(".session-item button,.message-meta button,[data-new-session]")) action.disabled = true;
        const statusNode = composer.querySelector("[data-send-status]"); statusNode.textContent = "正在生成…";
        const response = await fetch(`/api/sessions/${state.session.id}/messages`, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), signal: state.controller.signal });
        if (!response.ok) throw new Error(await errorText(response));
        const streamMessage = el("div", { class: "message assistant" }, el("div", { class: "message-text", "data-stream": "" })); messages.append(streamMessage);
        const textNode = streamMessage.querySelector("[data-stream]");
        const reader = response.body.getReader(); const decoder = new TextDecoder(); let buffer = "";
        while (true) {
          const { value, done } = await reader.read(); buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
          const lines = buffer.split("\n"); buffer = done ? "" : lines.pop();
          for (const line of lines) if (line.trim()) {
            const event = JSON.parse(line);
            if (event.type === "delta") textNode.textContent += event.text || "";
            if (event.type === "start" && event.session) state.session = event.session;
            if (event.type === "done" && event.session) state.session = event.session;
            if (event.type === "error") throw new Error(event.error || "生成失败");
          }
          if (done) break;
        }
        prompt.value = ""; captureDraft(); state.sending = false; await refreshSessions(state.session.id);
      } catch (error) {
        let shown = error;
        state.sending = false;
        if (state.session?.id) { try { state.session = await api(`/api/sessions/${state.session.id}`); state.sessions = await api("/api/sessions"); renderChat(); } catch (reloadError) { shown = reloadError; } }
        if (error.name !== "AbortError" || shown !== error) fail(shown, root.querySelector(".composer") || root);
      } finally {
        state.sending = false; state.controller = null; send.disabled = false; stop.hidden = true;
        for (const action of root.querySelectorAll(".session-item button,.message-meta button,[data-new-session]")) action.disabled = false;
      }
    }
    const newSession = button("新建会话", async () => { if (state.sending) return; try { const s = await api("/api/sessions", { method: "POST", body: JSON.stringify({ title: "新会话" }) }); await refreshSessions(s.id); } catch (error) { fail(error); } }, "secondary");
    newSession.dataset.newSession = ""; newSession.disabled = state.sending;
    for (const action of sessionList.querySelectorAll("button")) action.disabled = state.sending;
    root.append(heading("对话", "每条消息都是可继续、可折叠、可复制成新会话的节点。", newSession),
      el("div", { class: "chat-layout" }, el("aside", { class: "card session-panel" }, el("h2", {}, "会话"), sessionList), el("div", {}, messages, composer)));
  }

  async function initOperations(group) {
    [state.config, state.operations] = await Promise.all([api("/api/config"), api("/api/operations")]);
    renderOperations(group);
  }
  function operationCard(operation) {
    const form = el("form", { "data-operation": operation.id });
    const provider = el("select", { name: "provider_id", required: true }, el("option", { value: "" }, "选择服务商"), ...state.config.providers.map(p => el("option", { value: p.id }, p.name || p.id)));
    const defaults = operation.body && typeof operation.body === "object" ? structuredClone(operation.body) : {};
    const fields = [...new Set([...Object.keys(defaults), ...(operation.fields || [])])];
    const controls = new Map();
    for (const name of fields) {
      const value = defaults[name];
      const input = typeof value === "boolean" ? el("select", { name }, el("option", { value: "true" }, "是"), el("option", { value: "false" }, "否"))
        : (value && typeof value === "object") ? el("textarea", { name, rows: "3", spellcheck: "false" }, pretty(value))
        : el("input", { name, value: value ?? "", placeholder: friendlyLabel(name) });
      controls.set(name, input); form.append(field(friendlyLabel(name), input));
    }
    for (const name of operation.upload_fields || []) form.append(field(friendlyLabel(name), el("input", { name, type: "file", multiple: name === "image" }), "可留空以使用原生 JSON 中的文件 ID 或 URL（操作支持时）。"));
    const advanced = el("textarea", { rows: "7", spellcheck: "false" }, pretty(defaults));
    const fullJSON = checkbox("完全使用这份 JSON（忽略上方字段）", false);
    const save = checkbox("保存本次响应正文", Boolean(state.config.save_responses));
    const output = el("pre", { class: "operation-output", hidden: true });
    const submit = el("button", { type: "submit" }, operation.binary ? "执行并下载" : "执行操作");
    form.append(el("details", { class: "advanced" }, el("summary", {}, "完整原生 JSON"), fullJSON, field("params", advanced, "启用整包模式后，可直接编辑所有原生参数。")), el("div", { class: "actions" }, save, submit), output);
    form.addEventListener("submit", async event => {
      event.preventDefault(); submit.disabled = true; output.hidden = true;
      try {
        let params;
        try { params = JSON.parse(advanced.value || "{}"); } catch { throw new Error("完整原生 JSON 无法解析。"); }
        if (!fullJSON.querySelector("input").checked) for (const [name, input] of controls) {
          const raw = input.value;
          if (raw === "" && !(name in defaults)) { delete params[name]; continue; }
          if (typeof defaults[name] === "boolean") params[name] = raw === "true";
          else if (defaults[name] && typeof defaults[name] === "object") { try { params[name] = JSON.parse(raw); } catch { throw new Error(`${friendlyLabel(name)} 不是有效 JSON。`); } }
          else if (typeof defaults[name] === "number" && raw !== "") params[name] = Number(raw);
          else params[name] = raw;
        }
        let body, headers;
        const uploads = [...form.querySelectorAll("input[type=file]")];
        if (uploads.length) {
          body = new FormData(); body.append("provider_id", provider.value); body.append("params", JSON.stringify(params)); body.append("save_response", String(save.querySelector("input").checked));
          for (const upload of uploads) for (const file of upload.files) body.append(upload.name, file, file.name);
        } else { headers = { "Content-Type": "application/json" }; body = JSON.stringify({ provider_id: provider.value, params, save_response: save.querySelector("input").checked }); }
        const response = await fetch(`/api/operations/${encodeURIComponent(operation.id)}`, { method: "POST", credentials: "same-origin", headers, body });
        if (!response.ok) throw new Error(await errorText(response));
        const contentType = response.headers.get("content-type") || "";
        if (operation.binary || !contentType.includes("json")) {
          const blob = await response.blob(); const url = URL.createObjectURL(blob); const link = el("a", { href: url, download: filenameFromDisposition(response.headers.get("content-disposition")) || operation.id, class: "button secondary" }, "保存下载");
          output.replaceChildren(link); output.hidden = false; setTimeout(() => URL.revokeObjectURL(url), 60000);
        } else { output.textContent = pretty(await response.json()); output.hidden = false; }
      } catch (error) { output.textContent = error.message || String(error); output.hidden = false; }
      finally { submit.disabled = false; }
    });
    form.prepend(field("服务商", provider));
    return el("article", { class: "card operation-card" }, el("div", { class: "section-head" }, el("h2", {}, operationTitle(operation)), el("span", { class: "badge" }, operation.id)), form);
  }
  function filenameFromDisposition(value) { const match = /filename\*?=(?:UTF-8''|\")?([^";]+)/i.exec(value || ""); return match ? decodeURIComponent(match[1].replaceAll('"', "")) : ""; }
  function renderOperations(group) {
    clear(root);
    const titles = { files: ["文件", "上传、查询、下载与删除 provider 文件。"], containers: ["容器", "管理容器及容器内文件。"], batches: ["批处理", "创建、查询、取消与下载批处理结果。"], native: ["原生操作", "直接使用 Responses、Chat、图片和音频能力。"] };
    const groups = group === "native" ? ["responses", "chat", "images", "audio"] : [group];
    const operations = state.operations.filter(op => groups.includes(op.group));
    root.append(heading(...titles[group]), operations.length ? el("div", { class: "operation-list" }, ...operations.map(operationCard)) : el("div", { class: "card empty" }, el("h2", {}, "没有可用操作"), el("p", {}, "操作目录尚未提供这一组能力。")));
  }

  async function initLogs() {
    const logs = await api("/api/logs"); renderLogs(logs);
  }
  function renderLogs(logs) {
    clear(root);
    const list = el("section", { class: "card" });
    if (!logs.length) list.append(el("div", { class: "empty" }, el("h2", {}, "还没有请求日志"), el("p", {}, "发起对话或原生操作后，这里会记录请求。")));
    for (const log of logs) list.append(el("div", { class: "log-row" }, el("div", {}, el("strong", {}, log.operation), el("div", { class: "muted small" }, log.id)), el("span", {}, log.provider_id), el("span", {}, time(log.started_at)), el("div", { class: "actions" }, el("span", { class: "badge " + (log.status === "complete" ? "good" : "") }, log.status), button("查看", () => showLog(log.id), "secondary"))));
    root.append(heading("请求日志", "请求正文始终记录；响应正文是否保存由全局默认和每次操作开关决定。", button("刷新", initLogs, "secondary")), list, el("section", { id: "log-detail", class: "log-detail" }));
  }
  async function showLog(id) {
    const target = root.querySelector("#log-detail"); clear(target); target.append(el("div", { class: "loading-card" }, "正在读取日志…"));
    try {
      const detail = await api(`/api/logs/${encodeURIComponent(id)}`); clear(target);
      const card = el("article", { class: "card" }, el("div", { class: "section-head" }, el("h2", {}, "日志详情"), el("span", { class: "badge" }, id)), el("h3", {}, "操作元数据"), el("pre", {}, pretty(detail.metadata)));
      for (const request of detail.requests || []) {
        const reqDownload = `/api/logs/${encodeURIComponent(id)}/${encodeURIComponent(request.id)}/request.body`;
        const resDownload = `/api/logs/${encodeURIComponent(id)}/${encodeURIComponent(request.id)}/response.body`;
        const responseSaved = Boolean(detail.metadata?.save_response || request.response_body || request.response_truncated);
        card.append(el("details", { open: true }, el("summary", {}, `HTTP 请求 ${request.id}`), el("h3", {}, "Request metadata"), el("pre", {}, pretty(request.request)), el("h3", {}, "Request body"), el("pre", {}, request.request_body || "（空或二进制内容，请下载原始正文查看）"), el("a", { href: reqDownload, class: "button secondary", download: "" }, request.request_truncated ? "下载完整请求正文" : "下载原始请求正文"),
          el("h3", {}, "Response metadata"), el("pre", {}, pretty(request.response)), el("h3", {}, "Response body"), el("pre", {}, request.response_body || "（未保存或为空）"), responseSaved ? el("a", { href: resDownload, class: "button secondary", download: "" }, request.response_truncated ? "下载完整响应正文" : "下载原始响应正文") : null));
      }
      target.append(card); target.scrollIntoView({ behavior: "smooth", block: "start" });
    } catch (error) { clear(target); fail(error, target); }
  }

  const starters = { settings: initSettings, chat: initChat, files: () => initOperations("files"), containers: () => initOperations("containers"), batches: () => initOperations("batches"), native: () => initOperations("native"), logs: initLogs };
  Promise.resolve(starters[page]?.()).catch(error => { clear(root); fail(error); root.append(el("div", { class: "card empty" }, el("h1", {}, "页面暂时无法载入"), el("p", {}, "你的输入尚未被清除，可以重试或刷新页面。"), button("重试", () => location.reload(), "secondary"))); });
})();
