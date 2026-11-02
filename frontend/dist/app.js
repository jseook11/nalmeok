(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);

  const appEl = $("app");
  const modeToggle = $("mode-toggle");
  const addButton = $("add");
  const search = $("search");
  const list = $("list");
  const empty = $("empty");
  const helpButton = $("help");
  const gear = $("gear");

  const keysSheet = $("keys-sheet");
  const keysIntro = $("keys-intro");
  const keySource = $("key-source");
  const keyList = $("key-list");
  const keyForm = $("key-form");
  const keyTier = $("key-tier");
  const keyValue = $("key-value");
  const keyError = $("key-error");
  const keySave = $("key-save");
  const helpSheet = $("help-sheet");

  const menu = $("menu");
  const toast = $("toast");
  const toastText = $("toast-text");
  const toastAction = $("toast-action");

  const KEY_SOURCE_URL = "https://aistudio.google.com/apikey";
  const IS_WINDOWS = /Windows/i.test(navigator.userAgent);
  const IS_MAC = /Macintosh|Mac OS X/i.test(navigator.userAgent);
  document.documentElement.classList.toggle("is-mac", IS_MAC);
  const REVEAL_LABEL = IS_WINDOWS ? "탐색기에서 보기" : "Finder에서 보기";
  const ACTION_WIDTH = 78; // px per swipe action button
  const UNDO_MS = 4000;

  const state = {
    items: [],
    keys: [],
    mode: "free",
    query: "",
    keyTier: "free",
  };

  const GLYPHS = {
    file: `<svg class="glyph" viewBox="0 0 22 22" fill="none" stroke="currentColor" stroke-width="1.2"
      stroke-linejoin="round" aria-hidden="true">
      <path d="M5.5 2.5h7l4 4v13h-11z"/><path d="M12.5 2.5v4h4"/></svg>`,
    folder: `<svg class="glyph" viewBox="0 0 22 22" fill="none" stroke="currentColor" stroke-width="1.2"
      stroke-linejoin="round" aria-hidden="true">
      <path d="M2.5 5.5h6l2 2h9v10h-17z"/></svg>`,
    reveal: `<svg width="17" height="17" viewBox="0 0 18 18" fill="none" stroke="currentColor"
      stroke-width="1.2" stroke-linejoin="round" stroke-linecap="round" aria-hidden="true">
      <path d="M2.5 4.5h4.5l1.5 1.5h7v8.5h-13z"/><path d="M6.5 10.3h5M9.7 8.5l1.8 1.8-1.8 1.8"/></svg>`,
    close: `<svg width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor"
      stroke-width="1.4" stroke-linecap="round" aria-hidden="true"><path d="m2 2 6 6M8 2 2 8"/></svg>`,
  };

  // ── Backend ───────────────────────────────────────────────

  function call(method, ...args) {
    const api = window.go && window.go.app && window.go.app.App;
    if (!api || typeof api[method] !== "function") {
      return Promise.reject(new Error("아직 준비 중이에요"));
    }
    return api[method](...args);
  }

  function errorText(error) {
    let text = "";
    if (typeof error === "string") text = error;
    else if (error && typeof error.message === "string") text = error.message;
    else if (error != null) text = String(error);
    return text.replace(/^Error:\s*/, "").trim() || "문제가 생겼어요";
  }

  function esc(value) {
    return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    })[ch]);
  }

  // ── Toast ─────────────────────────────────────────────────

  let toastTimer = null;
  let toastOnAction = null;

  function showToast(message, options = {}) {
    clearTimeout(toastTimer);
    toastText.textContent = message;
    toastOnAction = options.onAction || null;
    toastAction.hidden = !toastOnAction;
    if (toastOnAction) toastAction.textContent = options.action || "실행 취소";
    toast.classList.add("is-visible");
    toastTimer = setTimeout(hideToast, options.duration || 1800);
  }

  function hideToast() {
    clearTimeout(toastTimer);
    toast.classList.remove("is-visible");
    toastOnAction = null;
  }

  // ── Mode ──────────────────────────────────────────────────

  function tiers() {
    return {
      free: state.keys.some((key) => key.Tier === "free"),
      paid: state.keys.some((key) => key.Tier === "paid"),
    };
  }

  function renderMode() {
    const { free, paid } = tiers();
    modeToggle.hidden = !(free && paid);
    for (const button of modeToggle.querySelectorAll("button")) {
      button.setAttribute("aria-selected", String(button.dataset.mode === state.mode));
    }
  }

  async function setMode(mode) {
    if (mode === state.mode) return;
    const previous = state.mode;
    state.mode = mode;
    renderMode();
    try {
      await call("SetMode", mode);
    } catch (error) {
      state.mode = previous;
      renderMode();
      showToast(errorText(error));
    }
  }

  async function syncMode() {
    const { free, paid } = tiers();
    if (!free && paid) await setMode("batch");
    else if (free && !paid) await setMode("free");
  }

  // ── Rows ──────────────────────────────────────────────────

  const AUDIO_EXT = /\.(m4a|mp4|mp3|wav)$/i;

  // Display names drop the audio extension; folder names stay as they are.
  function displayName(item) {
    const name = item.Name || "";
    return item.Kind === "folder" ? name : name.replace(AUDIO_EXT, "") || name;
  }

  const pad2 = (n) => String(n).padStart(2, "0");

  // When a finished item finished: "오늘 21:08", "어제 14:02", "11월 2일",
  // "2025년 11월 2일".
  function finishedAt(stamp) {
    const date = new Date(Date.parse(stamp));
    if (Number.isNaN(date.getTime())) return "";
    const now = new Date();
    const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const time = `${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
    if (date >= startOfToday) return `오늘 ${time}`;
    if (date >= new Date(startOfToday.getTime() - 86400000)) return `어제 ${time}`;
    const day = `${date.getMonth() + 1}월 ${date.getDate()}일`;
    return date.getFullYear() === now.getFullYear() ? day : `${date.getFullYear()}년 ${day}`;
  }

  function minutesSince(stamp) {
    const then = Date.parse(stamp);
    if (!Number.isFinite(then)) return 0;
    return Math.max(0, Math.floor((Date.now() - then) / 60000));
  }

  function progressHTML(progress) {
    if (progress > 0) {
      const percent = Math.min(100, Math.round(progress * 100));
      return `<div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax="100"
        aria-valuenow="${percent}"><span class="progress-fill" style="width:${percent}%"></span></div>`;
    }
    return `<div class="progress is-indeterminate" role="progressbar"><span class="progress-fill"></span></div>`;
  }

  // One state line (or a progress bar) under the name, and at most one
  // visible action on the right.
  function rowParts(item) {
    const id = esc(item.ID);
    switch (item.State) {
      case "running": {
        const progress = Number(item.Progress) || 0;
        const detail = item.Detail || "";
        if (progress <= 0 && detail && detail !== "전사 중") {
          return { status: `<div class="status"><span>${esc(detail)}</span></div>` };
        }
        return { status: progressHTML(progress) };
      }
      case "batch_waiting": {
        const minutes = minutesSince(item.CreatedAt);
        const text = minutes < 1 ? "기다리는 중" : `기다리는 중 · ${minutes}분`;
        return { status: `<div class="status"><span>${text}</span></div>` };
      }
      case "free_exhausted":
        return {
          status: `<div class="status"><span>오늘 무료 사용량을 다 썼어요</span></div>`,
          aside: `<button class="pill-button" type="button" data-action="batch" data-id="${id}">유료로 이어서</button>`,
        };
      case "done":
        return {
          status: `<div class="status"><span>${finishedAt(item.UpdatedAt || item.CreatedAt)}</span></div>`,
          aside: `<button class="icon-action" type="button" data-action="reveal" data-id="${id}"
            title="${REVEAL_LABEL}" aria-label="${REVEAL_LABEL}">${GLYPHS.reveal}</button>`,
        };
      case "failed": {
        const reason = item.Detail ? ` · ${esc(item.Detail)}` : "";
        return {
          status: `<div class="status"><span title="${esc(item.Detail)}">실패${reason}</span></div>`,
          aside: `<button class="accent-button" type="button" data-action="retry" data-id="${id}">다시 시도</button>`,
        };
      }
      case "canceled":
        return { status: `<div class="status"><span>취소됨</span></div>` };
      default:
        return { status: `<div class="status"><span>${esc(item.Detail || "대기 중")}</span></div>` };
    }
  }

  function isActive(item) {
    return item.State === "running" || item.State === "batch_waiting";
  }

  function rowHTML(item) {
    const id = esc(item.ID);
    const parts = rowParts(item);
    const first = isActive(item)
      ? `<button class="act-rename" type="button" data-action="cancel" data-id="${id}">취소</button>`
      : `<button class="act-rename" type="button" data-action="rename" data-id="${id}">이름 변경</button>`;
    return `
      <div class="row-actions">
        ${first}
        <button class="act-delete" type="button" data-action="delete" data-id="${id}">삭제</button>
      </div>
      <div class="row-main">
        ${item.Kind === "folder" ? GLYPHS.folder : GLYPHS.file}
        <div class="row-body">
          <div class="name" title="${esc(item.Source || item.Name)}">${esc(displayName(item))}</div>
          ${parts.status}
        </div>
        ${parts.aside ? `<div class="row-aside">${parts.aside}</div>` : ""}
      </div>`;
  }

  // Rows are kept and patched in place so an open swipe, a rename field or a
  // running animation survives the frequent "items:changed" refreshes.
  const rowCache = new Map(); // id -> { el, html }
  const hidden = new Set(); // ids removed from view while undo is possible
  let renaming = null; // { id, input }

  function visibleItems() {
    return state.items.filter((item) => !hidden.has(item.ID));
  }

  function renderItems() {
    const items = visibleItems();
    const isEmpty = items.length === 0 && !state.query;
    appEl.classList.toggle("is-empty", isEmpty);

    if (items.length === 0) {
      rowCache.clear();
      list.innerHTML = state.query ? `<p class="list-note">결과 없음</p>` : "";
      return;
    }
    const note = list.querySelector(".list-note");
    if (note) note.remove();

    const keep = new Set();
    let previous = null;
    for (const item of items) {
      keep.add(item.ID);
      const html = rowHTML(item);
      let entry = rowCache.get(item.ID);
      if (!entry) {
        const el = document.createElement("div");
        el.className = "row";
        el.dataset.id = item.ID;
        entry = { el, html: "" };
        rowCache.set(item.ID, entry);
      }
      const el = entry.el;
      const isRenaming = renaming && renaming.id === item.ID;
      if (entry.html !== html && !isRenaming) {
        entry.html = html;
        el.innerHTML = html;
        el.classList.toggle("is-done", item.State === "done");
        const offset = swipe.offsets.get(item.ID);
        if (offset) setOffset(el, offset, false);
      }
      const next = previous ? previous.nextSibling : list.firstChild;
      if (next !== el) list.insertBefore(el, next);
      previous = el;
    }
    for (const [id, entry] of rowCache) {
      if (!keep.has(id)) {
        entry.el.remove();
        rowCache.delete(id);
        swipe.offsets.delete(id);
      }
    }
  }

  function itemByID(id) {
    return state.items.find((item) => item.ID === id) || null;
  }

  let listRequest = 0;

  async function loadItems() {
    const token = ++listRequest;
    const items = await call("ListItems", state.query);
    if (token !== listRequest) return; // a newer search won
    state.items = Array.isArray(items) ? items : [];
    renderItems();
  }

  async function refreshItems() {
    try {
      await loadItems();
    } catch (error) {
      showToast(errorText(error));
    }
  }

  // ── Swipe ─────────────────────────────────────────────────
  //
  // Trackpad: horizontal wheel events (WKWebView reports two-finger swipes as
  // wheel deltaX). Mouse: pointer drag. Both uncover the actions on the right;
  // swiping past most of the row deletes it.

  const swipe = {
    offsets: new Map(), // id -> current translateX (<= 0)
    wheel: null, // { el, raw, timer }
    drag: null, // { el, startX, startY, base, active }
    suppressClick: false,
    quietUntil: 0,
  };

  function actionsWidth(el) {
    return el.querySelectorAll(".row-actions button").length * ACTION_WIDTH;
  }

  function displayed(el, raw) {
    if (raw > 0) return Math.min(24, raw * 0.25); // rubber band to the right
    const width = el.clientWidth;
    if (-raw > width) return -width;
    return raw;
  }

  function setOffset(el, x, animate) {
    const main = el.querySelector(".row-main");
    if (!main) return;
    el.classList.toggle("is-snapping", Boolean(animate));
    main.style.transform = x ? `translateX(${x}px)` : "";
    el.classList.toggle("is-full", -x > el.clientWidth * 0.6);
    const buttons = el.querySelector(".row-actions");
    if (buttons) buttons.style.width = `${Math.max(actionsWidth(el), -x)}px`;
    if (x) {
      swipe.offsets.set(el.dataset.id, x);
      el.classList.add("is-swiped");
    } else {
      swipe.offsets.delete(el.dataset.id);
      // Keep the actions visible while the row slides shut.
      const hide = () => {
        if (!swipe.offsets.has(el.dataset.id)) el.classList.remove("is-swiped");
      };
      if (animate) setTimeout(hide, 160);
      else hide();
    }
  }

  function closeRow(el, animate = true) {
    if (el && swipe.offsets.has(el.dataset.id)) setOffset(el, 0, animate);
  }

  function closeOtherRows(except) {
    for (const id of [...swipe.offsets.keys()]) {
      const entry = rowCache.get(id);
      if (entry && entry.el !== except) closeRow(entry.el);
    }
  }

  function settle(el, raw) {
    const x = displayed(el, raw);
    const open = actionsWidth(el);
    if (-x > el.clientWidth * 0.6) {
      setOffset(el, -el.clientWidth, true);
      setTimeout(() => removeWithUndo(el.dataset.id), 150);
    } else if (-x > open / 2) {
      closeOtherRows(el);
      setOffset(el, -open, true);
    } else {
      setOffset(el, 0, true);
    }
  }

  function onWheel(event) {
    const el = event.target.closest(".row");
    const horizontal = Math.abs(event.deltaX) > Math.abs(event.deltaY);
    const current = swipe.wheel;

    if (!current && !horizontal) {
      if (swipe.offsets.size && Math.abs(event.deltaY) > 2) closeOtherRows(null);
      return;
    }
    if (!current && (!el || renaming || Date.now() < swipe.quietUntil)) {
      if (horizontal) event.preventDefault();
      return;
    }
    event.preventDefault();

    let gesture = current;
    if (!gesture) {
      gesture = { el, raw: swipe.offsets.get(el.dataset.id) || 0, timer: null };
      swipe.wheel = gesture;
      closeOtherRows(el);
    }
    gesture.raw -= event.deltaX;
    gesture.raw = Math.min(gesture.raw, 120);
    setOffset(gesture.el, displayed(gesture.el, gesture.raw), false);

    clearTimeout(gesture.timer);
    gesture.timer = setTimeout(() => {
      swipe.wheel = null;
      // Momentum events keep arriving after the fingers lift; ignore them.
      swipe.quietUntil = Date.now() + 350;
      settle(gesture.el, gesture.raw);
    }, 120);
  }

  function onPointerDown(event) {
    if (event.button !== 0 || event.pointerType === "touch") return;
    if (event.target.closest("button, input")) return;
    const main = event.target.closest(".row-main");
    if (!main || renaming) return;
    const el = main.parentElement;
    swipe.drag = {
      el,
      startX: event.clientX,
      startY: event.clientY,
      base: swipe.offsets.get(el.dataset.id) || 0,
      active: false,
      pointer: event.pointerId,
    };
  }

  function onPointerMove(event) {
    const drag = swipe.drag;
    if (!drag || event.pointerId !== drag.pointer) return;
    const dx = event.clientX - drag.startX;
    const dy = event.clientY - drag.startY;
    if (!drag.active) {
      if (Math.abs(dx) < 6 || Math.abs(dx) < Math.abs(dy)) return;
      drag.active = true;
      closeOtherRows(drag.el);
      try { drag.el.setPointerCapture(event.pointerId); } catch (_) { /* ignore */ }
    }
    setOffset(drag.el, displayed(drag.el, drag.base + dx), false);
  }

  function onPointerUp(event) {
    const drag = swipe.drag;
    if (!drag || event.pointerId !== drag.pointer) return;
    swipe.drag = null;
    if (!drag.active) return;
    swipe.suppressClick = true;
    setTimeout(() => { swipe.suppressClick = false; }, 0);
    settle(drag.el, drag.base + (event.clientX - drag.startX));
  }

  // ── Delete with undo ──────────────────────────────────────

  let pendingDelete = null; // { id, timer }

  function commitDelete() {
    if (!pendingDelete) return;
    const { id, timer } = pendingDelete;
    clearTimeout(timer);
    pendingDelete = null;
    call("Remove", id)
      .catch((error) => showToast(errorText(error)))
      .finally(() => {
        hidden.delete(id);
        refreshItems();
      });
  }

  function removeWithUndo(id) {
    commitDelete();
    hidden.add(id);
    swipe.offsets.delete(id);
    renderItems();
    pendingDelete = { id, timer: setTimeout(commitDelete, UNDO_MS) };
    showToast("목록에서 지웠어요", {
      action: "실행 취소",
      duration: UNDO_MS,
      onAction: () => {
        if (!pendingDelete || pendingDelete.id !== id) return;
        clearTimeout(pendingDelete.timer);
        pendingDelete = null;
        hidden.delete(id);
        renderItems();
      },
    });
  }

  // ── Rename ────────────────────────────────────────────────

  function renameValue(item) {
    return displayName(item);
  }


  function startRename(id) {
    const item = itemByID(id);
    const entry = rowCache.get(id);
    if (!item || !entry) return;
    finishRename(false);
    closeRow(entry.el, false);

    const nameEl = entry.el.querySelector(".name");
    const input = document.createElement("input");
    input.className = "name-input";
    input.type = "text";
    input.spellcheck = false;
    input.value = renameValue(item);
    input.setAttribute("aria-label", "이름");
    nameEl.replaceWith(input);
    renaming = { id, input, original: input.value };
    input.focus();
    input.select();

    input.addEventListener("keydown", (event) => {
      if (event.isComposing) return;
      if (event.key === "Enter") {
        event.preventDefault();
        finishRename(true);
      } else if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        finishRename(false);
      }
    });
    input.addEventListener("blur", () => finishRename(true));
  }

  async function finishRename(save) {
    const current = renaming;
    if (!current) return;
    renaming = null;
    const value = current.input.value.trim();
    const entry = rowCache.get(current.id);
    if (entry) entry.html = ""; // force a fresh row on the next render
    if (save && value && value !== current.original) {
      try {
        await call("Rename", current.id, value);
      } catch (error) {
        showToast(errorText(error));
      }
    }
    await refreshItems();
  }

  // ── Row actions ───────────────────────────────────────────

  async function runAction(action, id) {
    try {
      switch (action) {
        case "open":
          await call("OpenOutput", id);
          break;
        case "copy":
          await call("CopyOutput", id);
          showToast("복사했어요");
          break;
        case "reveal":
          await call("RevealOutput", id);
          break;
        case "retry":
          await call("Retry", id);
          break;
        case "batch":
          if (!tiers().paid) {
            openKeys("paid");
            return;
          }
          await call("SendToBatch", id);
          break;
        case "cancel":
          await call("Cancel", id);
          break;
        case "rename":
          startRename(id);
          return;
        case "delete":
          removeWithUndo(id);
          return;
        default:
          return;
      }
      await refreshItems();
    } catch (error) {
      showToast(errorText(error));
    }
  }

  // ── Context menu ──────────────────────────────────────────

  let menuRow = null;

  function menuEntries(item) {
    const entries = [];
    switch (item.State) {
      case "done":
        entries.push(["open", "열기"], ["copy", "복사"], ["reveal", REVEAL_LABEL]);
        break;
      case "running":
      case "batch_waiting":
        entries.push(["cancel", "취소"]);
        break;
      case "free_exhausted":
        entries.push(["batch", "유료로 이어서"], ["cancel", "취소"]);
        break;
      case "failed":
      case "canceled":
        entries.push(["retry", "다시 시도"]);
        break;
      default:
        break;
    }
    if (entries.length) entries.push(null);
    entries.push(["rename", "이름 변경"], ["delete", "삭제", true]);
    return entries;
  }

  function openMenu(event, item, rowEl) {
    closeMenu();
    menu.innerHTML = menuEntries(item).map((entry) => {
      if (!entry) return "<hr>";
      const [action, label, danger] = entry;
      return `<button type="button" role="menuitem" data-action="${action}"
        ${danger ? 'class="is-danger"' : ""}>${label}</button>`;
    }).join("");
    menu.dataset.id = item.ID;
    menu.hidden = false;
    menuRow = rowEl;
    rowEl.classList.add("is-menu");

    const { innerWidth, innerHeight } = window;
    const rect = menu.getBoundingClientRect();
    const x = Math.min(event.clientX, innerWidth - rect.width - 6);
    const y = event.clientY + rect.height > innerHeight - 6
      ? Math.max(6, event.clientY - rect.height)
      : event.clientY;
    menu.style.left = `${Math.max(6, x)}px`;
    menu.style.top = `${y}px`;
  }

  function closeMenu() {
    if (menu.hidden) return;
    menu.hidden = true;
    if (menuRow) menuRow.classList.remove("is-menu");
    menuRow = null;
  }

  // ── Keys ──────────────────────────────────────────────────

  function keyHTML(key) {
    const tier = key.Tier === "free" ? "무료" : "유료";
    const isEnv = key.Source === "env";
    const showTier = !String(key.Label || "").includes(tier);
    return `
      <div class="key-row">
        <span class="key-label" title="${esc(key.Label)}">${esc(key.Label)}</span>
        ${showTier ? `<span class="key-meta">${tier}</span>` : ""}
        ${isEnv && key.Label !== "환경변수" ? `<span class="key-meta">환경변수</span>` : ""}
        <span class="key-mask">${esc(key.Masked)}</span>
        ${isEnv
          ? `<span class="key-delete-spacer"></span>`
          : `<button class="key-delete" type="button" data-key-delete="${esc(key.ID)}"
              aria-label="키 삭제" title="삭제">${GLYPHS.close}</button>`}
      </div>`;
  }

  function renderKeys() {
    keyList.innerHTML = state.keys.map(keyHTML).join("");
    keysIntro.hidden = state.keys.length > 0;
    for (const button of keyTier.querySelectorAll("button")) {
      button.setAttribute("aria-selected", String(button.dataset.tier === state.keyTier));
    }
  }

  async function loadKeys() {
    const keys = await call("Keys");
    state.keys = Array.isArray(keys) ? keys : [];
    renderKeys();
    await syncMode();
    renderMode();
  }

  function showKeyError(message) {
    keyError.textContent = message;
    keyError.hidden = false;
  }

  // ── Sheets ────────────────────────────────────────────────

  function openKeys(tier) {
    closeMenu();
    if (tier) state.keyTier = tier;
    keyError.hidden = true;
    renderKeys();
    keysSheet.hidden = false;
    keyValue.focus();
  }

  function openHelp() {
    closeMenu();
    helpSheet.hidden = false;
  }

  function closeSheets() {
    keysSheet.hidden = true;
    helpSheet.hidden = true;
  }

  function sheetOpen() {
    return !keysSheet.hidden || !helpSheet.hidden;
  }

  // ── Events ────────────────────────────────────────────────

  function pickFiles() {
    call("PickFiles").catch((error) => showToast(errorText(error)));
  }

  function bindDragHighlight() {
    let depth = 0;
    const isFiles = (event) => event.dataTransfer && Array.from(event.dataTransfer.types || []).includes("Files");
    window.addEventListener("dragenter", (event) => {
      if (!isFiles(event)) return;
      depth += 1;
      appEl.classList.add("is-dragover");
    });
    window.addEventListener("dragleave", (event) => {
      if (!isFiles(event)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0) appEl.classList.remove("is-dragover");
    });
    window.addEventListener("dragover", (event) => {
      event.preventDefault();
      if (isFiles(event)) appEl.classList.add("is-dragover");
    });
    window.addEventListener("drop", (event) => {
      event.preventDefault();
      depth = 0;
      appEl.classList.remove("is-dragover");
    });
  }

  function bindEvents() {
    bindDragHighlight();

    addButton.addEventListener("click", pickFiles);
    empty.addEventListener("click", pickFiles);

    modeToggle.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-mode]");
      if (button) setMode(button.dataset.mode);
    });

    let searchTimer = null;
    search.addEventListener("input", () => {
      clearTimeout(searchTimer);
      searchTimer = setTimeout(() => {
        state.query = search.value.trim();
        refreshItems();
      }, 120);
    });
    search.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && search.value) {
        event.stopPropagation();
        search.value = "";
        state.query = "";
        refreshItems();
      }
    });

    list.addEventListener("wheel", onWheel, { passive: false });
    list.addEventListener("pointerdown", onPointerDown);
    list.addEventListener("pointermove", onPointerMove);
    list.addEventListener("pointerup", onPointerUp);
    list.addEventListener("pointercancel", onPointerUp);

    list.addEventListener("click", (event) => {
      if (swipe.suppressClick) return;
      const rowEl = event.target.closest(".row");
      if (!rowEl) return;
      const id = rowEl.dataset.id;
      const button = event.target.closest("button[data-action]");

      if (button) {
        if (button.closest(".row-actions")) closeRow(rowEl, false);
        runAction(button.dataset.action, button.dataset.id);
        return;
      }
      if (event.target.closest("input")) return;
      // A click while any row is swiped open only closes it.
      if (swipe.offsets.size) {
        closeOtherRows(null);
        return;
      }
      const item = itemByID(id);
      if (item && item.State === "done") runAction("open", id);
    });

    list.addEventListener("contextmenu", (event) => {
      const rowEl = event.target.closest(".row");
      if (!rowEl || event.target.closest("input")) return;
      event.preventDefault();
      const item = itemByID(rowEl.dataset.id);
      if (!item) return;
      closeOtherRows(null);
      openMenu(event, item, rowEl);
    });

    menu.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-action]");
      if (!button) return;
      const id = menu.dataset.id;
      closeMenu();
      runAction(button.dataset.action, id);
    });

    document.addEventListener("pointerdown", (event) => {
      if (!menu.hidden && !menu.contains(event.target)) closeMenu();
      if (swipe.offsets.size && !event.target.closest(".row")) closeOtherRows(null);
    }, true);
    list.addEventListener("scroll", closeMenu, { passive: true });
    window.addEventListener("blur", closeMenu);

    toastAction.addEventListener("click", () => {
      const action = toastOnAction;
      hideToast();
      if (action) action();
    });

    helpButton.addEventListener("click", openHelp);
    gear.addEventListener("click", () => openKeys());
    for (const button of document.querySelectorAll("[data-close]")) {
      button.addEventListener("click", closeSheets);
    }
    keySource.addEventListener("click", () => {
      if (window.runtime && typeof window.runtime.BrowserOpenURL === "function") {
        window.runtime.BrowserOpenURL(KEY_SOURCE_URL);
      } else {
        window.open(KEY_SOURCE_URL, "_blank");
      }
    });

    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        if (!menu.hidden) closeMenu();
        else if (sheetOpen()) closeSheets();
        else if (swipe.offsets.size) closeOtherRows(null);
      } else if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "f" && !sheetOpen()) {
        event.preventDefault();
        search.focus();
        search.select();
      }
    });

    keyTier.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-tier]");
      if (!button) return;
      state.keyTier = button.dataset.tier;
      renderKeys();
    });

    keyList.addEventListener("click", async (event) => {
      const button = event.target.closest("[data-key-delete]");
      if (!button) return;
      button.disabled = true;
      try {
        await call("DeleteKey", button.dataset.keyDelete);
        await loadKeys();
      } catch (error) {
        button.disabled = false;
        showKeyError(errorText(error));
      }
    });

    keyForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      keyError.hidden = true;
      const value = keyValue.value.trim();
      if (!value) {
        keyValue.focus();
        return;
      }
      keySave.disabled = true;
      keySave.textContent = "확인 중";
      try {
        await call("AddKey", "", state.keyTier, value);
        keyValue.value = "";
        await loadKeys();
        showToast("키를 저장했어요");
      } catch (error) {
        showKeyError(errorText(error));
      } finally {
        keySave.disabled = false;
        keySave.textContent = "저장";
      }
    });
  }

  async function init() {
    bindEvents();

    try {
      const mode = await call("GetMode");
      if (mode === "free" || mode === "batch") state.mode = mode;
    } catch (_) {
      // Keep the default; the first drop reports the real error.
    }

    try {
      await loadKeys();
    } catch (error) {
      showToast(errorText(error));
    }
    renderMode();

    try {
      await loadItems();
    } catch (error) {
      showToast(errorText(error));
    }

    if (state.keys.length === 0) openKeys("free");

    if (window.runtime && typeof window.runtime.EventsOn === "function") {
      window.runtime.EventsOn("items:changed", refreshItems);
    }

    // Keep "기다리는 중 · N분" current without a backend round trip.
    setInterval(() => {
      if (state.items.some((item) => item.State === "batch_waiting")) renderItems();
    }, 30000);
  }

  init();
})();
