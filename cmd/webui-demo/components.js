(function () {
  "use strict";
  const input = document.getElementById("video-file");
  const video = document.getElementById("demo-video");
  let source;
  input.addEventListener("change", () => {
    if (source) URL.revokeObjectURL(source);
    video.removeAttribute("src");
    if (input.files[0]) {
      source = URL.createObjectURL(input.files[0]);
      video.src = source;
    }
    video.load();
  });
  window.addEventListener("pagehide", event => { if (source && !event.persisted) URL.revokeObjectURL(source); });

  const panel = document.getElementById("chart-panel");
  const status = document.getElementById("chart-update-status");
  let sequence = 0, pending;
  function restoreChartURL() {
    const url = new URL(document.getElementById("chart-content").dataset.url, location.href);
    if (url.href !== location.href) history.replaceState(null, "", url.href);
  }
  async function updateChart(url, pushHistory) {
    const current = ++sequence;
    if (pending) pending.abort();
    const abort = pending = new AbortController();
    const timer = setTimeout(() => abort.abort(), 15000);
    panel.setAttribute("aria-busy", "true");
    status.setAttribute("role", "status");
    status.hidden = true;
    try {
      const endpoint = new URL("/components/chart", location.href);
      endpoint.search = url.search;
      const response = await webui.request(endpoint.href, { signal: abort.signal, cache: "no-store", redirect: "error" });
      const html = await response.text();
      if (current !== sequence) return;
      const parsed = new DOMParser().parseFromString(html, "text/html");
      const replacement = parsed.getElementById("chart-content");
      if (!replacement || !replacement.querySelector("svg")) throw new Error("图表响应不完整。");
      const targetURL = new URL(replacement.dataset.url, location.href);
      if (targetURL.origin !== location.origin || targetURL.pathname !== "/components") throw new Error("图表地址无效。");
      const old = document.getElementById("chart-content");
      const active = document.activeElement;
      let focus;
      if (old.contains(active)) {
        focus = Array.from(replacement.querySelectorAll("input, button")).find(el => active.id ? el.id === active.id : el.name === active.name && el.value === active.value);
        if (active.matches(".chart-hit")) {
          focus = replacement.querySelectorAll(".chart-hit")[Array.from(old.querySelectorAll(".chart-hit")).indexOf(active)];
        }
        if (!focus || focus.disabled) focus = replacement.querySelector("button:not(:disabled)");
      }
      const x = scrollX, y = scrollY;
      old.replaceWith(replacement);
      status.hidden = true;
      if (focus) focus.focus({ preventScroll: true });
      window.scrollTo(x, y);
      if (pushHistory && targetURL.href !== location.href) history.pushState(null, "", targetURL.href);
    } catch (error) {
      let message = error.name === "AbortError" ? "更新超时，请重试。" : "更新失败，请重试。";
      if (error.response && error.response.status === 422) {
        try { message = (await error.response.json()).error || message; } catch (_) { /* Keep the fallback. */ }
      }
      if (current !== sequence) return;
      restoreChartURL();
      status.setAttribute("role", "alert");
      status.textContent = message;
      status.hidden = false;
    } finally {
      clearTimeout(timer);
      if (current === sequence) { pending = null; panel.removeAttribute("aria-busy"); }
    }
  }
  panel.addEventListener("submit", event => {
    event.preventDefault();
    const data = new FormData(event.target);
    const button = event.submitter;
    if (button && button.name) data.append(button.name, button.value);
    const url = new URL("/components", location.href);
    url.search = new URLSearchParams(data).toString();
    updateChart(url, true);
  });
  panel.addEventListener("keydown", event => {
    if (!event.target.closest(".chart") || !["ArrowLeft", "ArrowRight"].includes(event.key) ||
        event.altKey || event.ctrlKey || event.metaKey) return;
    event.preventDefault();
    // Keep keyboard panning sequential; held keys resume when rendering finishes.
    if (pending) return;
    const direction = event.key === "ArrowLeft" ? "left" : "right";
    const boundary = panel.querySelector('button[value="' + (direction === "left" ? "prev" : "next") + '"]');
    if (boundary.disabled) return;
    const url = new URL(document.getElementById("chart-content").dataset.url, location.href);
    url.searchParams.set("move", direction);
    url.searchParams.set("step", event.shiftKey ? "5" : "1");
    updateChart(url, true);
  });
  // Typing a new range must not be overwritten by an older response.
  panel.addEventListener("input", () => {
    if (!pending) return;
    sequence++; pending.abort(); pending = null;
    restoreChartURL();
    panel.removeAttribute("aria-busy"); status.hidden = true;
  });
  window.addEventListener("popstate", () => updateChart(new URL(location.href), false));
})();
