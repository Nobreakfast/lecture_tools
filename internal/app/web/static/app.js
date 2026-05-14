// Copyright 2024-2026 course-assistant contributors. SPDX-License-Identifier: MIT
// Shared client helpers for course-assistant pages.
//
// Exposes (on window.App):
//   fetchJSON(url, opts)    – fetch + JSON, throws on !ok
//   fetchAny(url, opts)     – raw fetch (preserves cookies, path prefix)
//   toast(msg, type)        – non-blocking notification
//   confirmModal(title, msg)– Promise<boolean>
//   promptModal(title, def) – Promise<string|null>
//   escapeHTML(s)           – HTML-safe text
//   storage                 – small localStorage wrapper
//   prefix()                – optional URL prefix (set via window.__PREFIX)
(function () {
  "use strict";
  const prefix = () => (typeof window.__PREFIX === "string" ? window.__PREFIX : "");
  const abs = (url) => (url.startsWith("http") ? url : prefix() + url);

  async function fetchAny(url, opts) {
    opts = Object.assign({ credentials: "include" }, opts || {});
    return fetch(abs(url), opts);
  }

  async function fetchJSON(url, opts) {
    const res = await fetchAny(url, opts);
    if (!res.ok) {
      let detail;
      try { detail = await res.text(); } catch (_) { detail = res.statusText; }
      const err = new Error(detail || ("HTTP " + res.status));
      err.status = res.status;
      throw err;
    }
    const ct = res.headers.get("content-type") || "";
    if (ct.indexOf("application/json") >= 0) return res.json();
    return res.text();
  }

  // ── Upload progress ────────────────────────────────────────────
  function formatBytes(bytes) {
    const n = Number(bytes || 0);
    if (!Number.isFinite(n) || n <= 0) return "0 B";
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    return (n / (1024 * 1024)).toFixed(1) + " MB";
  }

  function makeHeaders(rawHeaders) {
    const map = {};
    String(rawHeaders || "").trim().split(/[\r\n]+/).forEach((line) => {
      const idx = line.indexOf(":");
      if (idx <= 0) return;
      map[line.slice(0, idx).trim().toLowerCase()] = line.slice(idx + 1).trim();
    });
    return {
      get(name) {
        return map[String(name || "").toLowerCase()] || null;
      },
    };
  }

  function ensureUploadProgressStack() {
    let stack = document.getElementById("upload-progress-stack");
    if (!stack) {
      stack = document.createElement("div");
      stack.id = "upload-progress-stack";
      document.body.appendChild(stack);
    }
    const host = document.createElement("div");
    host.className = "upload-progress-toast";
    stack.appendChild(host);
    return host;
  }

  function createUploadProgress(target, opts) {
    opts = opts || {};
    const host = typeof target === "string" ? document.getElementById(target) : target;
    if (!host) {
      return { update() {}, finish() {}, error() {}, clear() {} };
    }

    const label = document.createElement("div");
    label.className = "upload-progress-label";
    label.textContent = opts.label || "正在上传";

    const value = document.createElement("div");
    value.className = "upload-progress-value";
    value.textContent = "准备中";

    const row = document.createElement("div");
    row.className = "upload-progress-row";
    row.appendChild(label);
    row.appendChild(value);

    const fill = document.createElement("div");
    fill.className = "upload-progress-fill";

    const track = document.createElement("div");
    track.className = "upload-progress-track";
    track.setAttribute("role", "progressbar");
    track.setAttribute("aria-valuemin", "0");
    track.setAttribute("aria-valuemax", "100");
    track.appendChild(fill);

    const root = document.createElement("div");
    root.className = "upload-progress";
    root.appendChild(row);
    root.appendChild(track);

    if (host.classList && host.classList.contains("message")) {
      host.classList.add("show");
      if (!host.classList.contains("info") && !host.classList.contains("error") && !host.classList.contains("warn")) {
        host.classList.add("info");
      }
    }
    host.classList.add("upload-progress-host");
    host.replaceChildren(root);

    function setIndeterminate(text) {
      root.classList.add("is-indeterminate");
      track.removeAttribute("aria-valuenow");
      fill.style.width = "";
      value.textContent = text || "上传中...";
    }

    function setPercent(percent, text) {
      const pct = Math.max(0, Math.min(100, Math.round(percent || 0)));
      root.classList.remove("is-indeterminate");
      track.setAttribute("aria-valuenow", String(pct));
      fill.style.width = pct + "%";
      value.textContent = text || (pct + "%");
    }

    setPercent(0, "准备中");

    return {
      update(evt) {
        if (!evt || !evt.total) {
          setIndeterminate(evt && evt.loaded ? ("已上传 " + formatBytes(evt.loaded)) : "上传中...");
          return;
        }
        const percent = (evt.loaded / evt.total) * 100;
        setPercent(percent, Math.round(percent) + "% · " + formatBytes(evt.loaded) + " / " + formatBytes(evt.total));
      },
      finish(text) {
        root.classList.add("is-complete");
        setPercent(100, text || "上传完成");
      },
      error(text) {
        root.classList.add("is-error");
        label.textContent = "上传失败";
        value.textContent = text || "请重试";
      },
      clear() {
        host.classList.remove("upload-progress-host");
        if (opts.removeOnClear) host.remove();
        else host.replaceChildren();
      },
    };
  }

  function uploadForm(url, opts) {
    opts = opts || {};
    const method = opts.method || "POST";
    const body = opts.body || null;
    const showProgress = opts.progress !== false;
    const progressTarget = opts.progressTarget || (showProgress ? ensureUploadProgressStack() : null);
    const progress = showProgress ? createUploadProgress(progressTarget, {
      label: opts.progressLabel || "正在上传",
      removeOnClear: !opts.progressTarget,
    }) : null;

    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open(method, abs(url), true);
      xhr.withCredentials = opts.credentials !== "omit";

      const headers = opts.headers || {};
      Object.keys(headers).forEach((name) => xhr.setRequestHeader(name, headers[name]));

      const notifyProgress = (evt) => {
        const info = {
          loaded: evt.loaded || 0,
          total: evt.lengthComputable ? evt.total : 0,
          percent: evt.lengthComputable && evt.total ? (evt.loaded / evt.total) * 100 : null,
        };
        if (progress) progress.update(info);
        if (typeof opts.onProgress === "function") opts.onProgress(info);
      };

      if (xhr.upload) xhr.upload.onprogress = notifyProgress;

      xhr.onload = () => {
        const response = {
          ok: xhr.status >= 200 && xhr.status < 300,
          status: xhr.status,
          statusText: xhr.statusText,
          headers: makeHeaders(xhr.getAllResponseHeaders()),
          text: () => Promise.resolve(xhr.responseText || ""),
          json: () => Promise.resolve(JSON.parse(xhr.responseText || "null")),
        };
        if (progress) {
          if (response.ok) progress.finish("上传完成");
          else progress.error(xhr.responseText || xhr.statusText || ("HTTP " + xhr.status));
          if (!opts.progressTarget) setTimeout(() => progress.clear(), response.ok ? 900 : 2800);
        }
        resolve(response);
      };
      xhr.onerror = () => {
        if (progress) {
          progress.error("网络异常");
          if (!opts.progressTarget) setTimeout(() => progress.clear(), 2800);
        }
        reject(new Error("网络异常，请重试。"));
      };
      xhr.onabort = () => {
        if (progress) {
          progress.error("已取消");
          if (!opts.progressTarget) setTimeout(() => progress.clear(), 1200);
        }
        reject(new Error("上传已取消"));
      };
      if (opts.signal) {
        opts.signal.addEventListener("abort", () => xhr.abort(), { once: true });
      }
      xhr.send(body);
    });
  }

  async function uploadJSON(url, opts) {
    const res = await uploadForm(url, opts);
    if (!res.ok) {
      let detail;
      try { detail = await res.text(); } catch (_) { detail = res.statusText; }
      const err = new Error(detail || ("HTTP " + res.status));
      err.status = res.status;
      throw err;
    }
    const text = await res.text();
    if (!text) return null;
    try { return JSON.parse(text); } catch (_) { return text; }
  }

  // ── Toast ─────────────────────────────────────────────────────
  function ensureToastStack() {
    let el = document.getElementById("toast-stack");
    if (!el) {
      el = document.createElement("div");
      el.id = "toast-stack";
      document.body.appendChild(el);
    }
    return el;
  }
  function toast(msg, type) {
    const stack = ensureToastStack();
    const el = document.createElement("div");
    el.className = "toast" + (type ? " toast-" + type : "");
    el.textContent = msg;
    stack.appendChild(el);
    setTimeout(() => {
      el.style.transition = "opacity 0.2s";
      el.style.opacity = "0";
      setTimeout(() => el.remove(), 220);
    }, type === "error" ? 4500 : 2400);
  }

  // ── Modal ─────────────────────────────────────────────────────
  function openModal({ title, body, confirmText, cancelText, defaultValue, isPrompt }) {
    return new Promise((resolve) => {
      const backdrop = document.createElement("div");
      backdrop.className = "modal-backdrop";
      const modal = document.createElement("div");
      modal.className = "modal";

      const h3 = document.createElement("h3");
      h3.textContent = title || "";
      modal.appendChild(h3);

      if (body) {
        const p = document.createElement("div");
        p.style.color = "var(--text-muted)";
        p.style.fontSize = "14px";
        p.textContent = body;
        modal.appendChild(p);
      }

      let input;
      if (isPrompt) {
        input = document.createElement("input");
        input.type = "text";
        input.value = defaultValue != null ? defaultValue : "";
        input.style.marginTop = "12px";
        modal.appendChild(input);
      }

      const actions = document.createElement("div");
      actions.className = "modal-actions";

      const cancelBtn = document.createElement("button");
      cancelBtn.className = "btn btn-secondary";
      cancelBtn.textContent = cancelText || "取消";

      const okBtn = document.createElement("button");
      okBtn.className = "btn";
      okBtn.textContent = confirmText || "确定";

      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      modal.appendChild(actions);
      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);

      if (input) setTimeout(() => input.focus(), 0);

      const close = (v) => {
        backdrop.remove();
        resolve(v);
      };
      cancelBtn.addEventListener("click", () => close(isPrompt ? null : false));
      okBtn.addEventListener("click", () => close(isPrompt ? (input.value || "") : true));
      backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) close(isPrompt ? null : false);
      });
      document.addEventListener("keydown", function onKey(e) {
        if (e.key === "Escape") { close(isPrompt ? null : false); document.removeEventListener("keydown", onKey); }
        if (e.key === "Enter" && isPrompt) { close(input.value || ""); document.removeEventListener("keydown", onKey); }
      });
    });
  }
  function confirmModal(title, body, opts) {
    opts = opts || {};
    return openModal({ title: title, body: body, confirmText: opts.confirm, cancelText: opts.cancel });
  }
  function promptModal(title, defaultValue, opts) {
    opts = opts || {};
    return openModal({ title: title, body: opts.body, isPrompt: true, defaultValue: defaultValue, confirmText: opts.confirm, cancelText: opts.cancel });
  }

  // ── Misc helpers ──────────────────────────────────────────────
  function escapeHTML(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  const storage = {
    get(key, fallback) {
      try {
        const v = localStorage.getItem(key);
        return v == null ? (fallback == null ? null : fallback) : JSON.parse(v);
      } catch (_) { return fallback == null ? null : fallback; }
    },
    set(key, value) {
      try { localStorage.setItem(key, JSON.stringify(value)); } catch (_) { /* quota */ }
    },
    remove(key) { try { localStorage.removeItem(key); } catch (_) {} },
  };

  window.App = {
    prefix, abs,
    fetchJSON, fetchAny, uploadForm, uploadJSON, createUploadProgress,
    toast, confirmModal, promptModal,
    escapeHTML, storage,
  };
})();
