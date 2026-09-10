/* Manual position controls; storage and authorization belong to the application. */
(function () {
  "use strict";
  for (const controls of document.querySelectorAll("[data-webui-video]")) {
    controls.hidden = false;
    const status = controls.querySelector('[role="status"]');
    const buttons = controls.querySelectorAll("button[data-video-action]");
    let busy = false;
    for (const button of buttons) button.addEventListener("click", async () => {
      if (busy) return;
      busy = true;
      controls.setAttribute("aria-busy", "true");
      for (const b of buttons) b.setAttribute("aria-disabled", "true");
      const abort = new AbortController();
      const timer = setTimeout(() => abort.abort(), 15000);
      try {
        const video = document.getElementById(controls.dataset.webuiVideo);
        if (!(video instanceof HTMLVideoElement)) throw new Error("找不到视频。");
        const source = video.getAttribute("src") + Array.from(video.querySelectorAll("source"), el => el.src).join("\n");
        const sameSource = () => source === video.getAttribute("src") + Array.from(video.querySelectorAll("source"), el => el.src).join("\n");
        const save = button.dataset.videoAction === "save";
        const url = new URL(save ? controls.dataset.saveUrl : controls.dataset.restoreUrl, location.href);
        if (url.origin !== location.origin) throw new Error("回调地址必须同源。");
        status.textContent = save ? "正在保存…" : "正在恢复…";
        if (save) {
          if (!video.readyState || !Number.isFinite(video.currentTime)) throw new Error("请先加载视频。");
          await webui.request(url.href, {
            method: "POST", headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ seconds: video.currentTime }), signal: abort.signal, redirect: "error"
          });
          status.textContent = "位置已保存。";
        } else {
          const result = await webui.json(url.href, { cache: "no-store", signal: abort.signal, redirect: "error" });
          if (result === null) { status.textContent = "尚未保存位置。"; return; }
          if (typeof result.seconds !== "number" || !Number.isFinite(result.seconds) || result.seconds < 0) throw new Error("返回的播放位置无效。");
          if (!sameSource()) throw new Error("视频已切换，请重新恢复。");
          if (!video.readyState) await mediaEvent(video, "loadedmetadata", abort.signal);
          if (!sameSource()) throw new Error("视频已切换，请重新恢复。");
          if (!Number.isFinite(video.duration) || video.duration <= 0) throw new Error("当前视频不支持恢复位置。");
          const seconds = Math.min(result.seconds, video.duration);
          if (Math.abs(video.currentTime - seconds) > .01) {
            await mediaEvent(video, "seeked", abort.signal, () => { video.currentTime = seconds; });
          }
          if (Math.abs(video.currentTime - seconds) > .1) throw new Error("当前视频无法定位到保存位置。");
          if (!sameSource()) throw new Error("视频已切换，请重新恢复。");
          status.textContent = "位置已恢复。";
        }
      } catch (error) {
        status.textContent = error.name === "AbortError" ? "操作超时，请重试。" : "操作失败：" + error.message;
      } finally {
        clearTimeout(timer);
        busy = false;
        controls.removeAttribute("aria-busy");
        for (const b of buttons) b.removeAttribute("aria-disabled");
      }
    });
  }

  function mediaEvent(video, event, signal, trigger) {
    return new Promise((resolve, reject) => {
      const done = e => {
        video.removeEventListener(event, done);
        video.removeEventListener("error", done);
        video.removeEventListener("emptied", done);
        signal.removeEventListener("abort", done);
        if (e.type === event) resolve();
        else if (e.type === "abort") reject(new DOMException("Aborted", "AbortError"));
        else reject(e.error || new Error("视频加载失败或已切换。"));
      };
      video.addEventListener(event, done);
      video.addEventListener("error", done);
      video.addEventListener("emptied", done);
      signal.addEventListener("abort", done, { once: true });
      if (signal.aborted) done({ type: "abort" });
      else if (trigger) {
        try { trigger(); } catch (error) { done({ type: "error", error }); }
      }
    });
  }
})();
