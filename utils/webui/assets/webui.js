/* Optional request helpers. Forms and links keep their native behavior. */
(function (global) {
  "use strict";

  async function request(url, options) {
    const response = await fetch(url, options);
    if (!response.ok) {
      const error = new Error("HTTP " + response.status + " " + response.statusText);
      error.response = response;
      throw error;
    }
    return response;
  }

  async function json(url, options) {
    const init = Object.assign({}, options);
    const headers = new Headers(init.headers);
    if (!headers.has("Accept")) headers.set("Accept", "application/json");
    if (init.body !== undefined) {
      headers.set("Content-Type", "application/json");
      init.body = JSON.stringify(init.body);
    }
    init.headers = headers;
    const response = await request(url, init);
    if (response.status === 204 || response.status === 205) return null;
    return response.json();
  }

  async function form(url, data, options) {
    const init = Object.assign({ method: "POST" }, options);
    if (!(data instanceof FormData)) throw new TypeError("webui.form expects FormData");
    const method = init.method.toUpperCase();
    const target = new URL(url, global.location.href);
    const headers = new Headers(init.headers);
    headers.delete("Content-Type"); // The browser supplies the multipart boundary.
    init.headers = headers;
    if (method === "GET" || method === "HEAD") {
      for (const [key, value] of data) {
        target.searchParams.append(key, typeof value === "string" ? value : value.name);
      }
      delete init.body;
    } else {
      init.body = data;
    }
    return request(target.href, init);
  }

  global.webui = Object.freeze({ request, json, form });
})(window);
