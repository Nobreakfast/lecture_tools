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
    fetchJSON, fetchAny,
    toast, confirmModal, promptModal,
    escapeHTML, storage,
  };
})();
