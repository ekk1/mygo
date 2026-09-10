/* Local theme selection; ordinary forms keep their native behavior. */
(function () {
  "use strict";
  let sheet = document.querySelector("link[data-webui-theme-sheet]");
  if (!sheet) return;
  const names = ["rose", "sand", "sage", "dusk"];
  const buttons = Array.from(document.querySelectorAll("button[data-webui-theme]"));
  const groups = document.querySelectorAll("[data-webui-theme-controls]");
  const status = document.querySelector("[data-webui-theme-status]");
  let busy = false;

  function update() {
    buttons.forEach(button => {
      button.setAttribute("aria-disabled", String(busy));
      button.setAttribute("aria-pressed", String(button.dataset.webuiTheme === sheet.dataset.webuiThemeSheet));
    });
    groups.forEach(group => group.setAttribute("aria-busy", String(busy)));
  }

  function select(name) {
    if (busy || !names.includes(name) || name === sheet.dataset.webuiThemeSheet) return;
    busy = true;
    if (status) status.textContent = "";
    update();
    const next = document.createElement("link");
    next.rel = "stylesheet";
    // Load without applying; keep the current palette if loading fails.
    next.media = "not all";
    next.href = new URL(name + ".css", sheet.href).href;
    const timer = setTimeout(failed, 10000);
    function failed() {
      clearTimeout(timer);
      next.onload = next.onerror = null;
      next.remove();
      busy = false;
      if (status) status.textContent = "配色加载失败，请重试。";
      update();
    }
    next.onerror = failed;
    next.onload = function () {
      clearTimeout(timer);
      next.onload = next.onerror = null;
      next.dataset.webuiThemeSheet = name;
      next.media = "all";
      sheet.replaceWith(next);
      sheet = next;
      busy = false;
      try { localStorage.setItem("webui-theme", name); } catch (_) { /* Storage is optional. */ }
      update();
    };
    sheet.after(next);
  }

  buttons.forEach(button => button.addEventListener("click", () => select(button.dataset.webuiTheme)));
  groups.forEach(group => { group.hidden = false; });
  update();
  try { select(localStorage.getItem("webui-theme")); } catch (_) { /* Keep the server default. */ }
})();
