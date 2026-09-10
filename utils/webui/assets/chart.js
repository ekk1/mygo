/* SVG geometry stays on the server; delegated events also support replaced charts. */
(function () {
  "use strict";
  for (const title of document.querySelectorAll(".chart-hit title")) title.remove();
  const chartFor = target => target instanceof Element ? target.closest(".chart") : null;
  function hide(chart) {
    const tip = chart && chart.querySelector(".chart-tooltip");
    if (tip) tip.hidden = true;
  }
  function show(event) {
    const chart = chartFor(event.target);
    if (!chart) return;
    const tip = chart.querySelector(".chart-tooltip");
    const hit = event.target.closest(".chart-hit");
    if (!tip) return;
    if (!hit) { tip.hidden = true; return; }
    const title = hit.querySelector("title");
    if (title) title.remove();
    tip.textContent = hit.getAttribute("aria-label");
    tip.hidden = false;
  }
  document.addEventListener("pointermove", show);
  document.addEventListener("click", show);
  document.addEventListener("focusin", show);
  document.addEventListener("pointerout", event => {
    const chart = chartFor(event.target);
    if (chart && !chart.contains(event.relatedTarget)) hide(chart);
  });
  document.addEventListener("focusout", event => hide(chartFor(event.target)));
  document.addEventListener("keydown", event => { if (event.key === "Escape") hide(chartFor(event.target)); });
})();
