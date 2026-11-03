(async () => {
  "use strict";

  // Windows: Wails only forwards dropped file paths to Go (OnFileDrop in
  // internal/app) once the page registers its JS drop listener. macOS
  // delivers drops natively, so this is a no-op there. The callback is
  // empty because Go handles the paths; false = no drop-target check, so a
  // drop lands anywhere in the window, sheets and the PDF card included.
  // Registered before anything awaits (the catalog load below included), so
  // a drop while the window is still loading is not lost.
  if (window.runtime && typeof window.runtime.OnFileDrop === "function") {
    window.runtime.OnFileDrop(() => {}, false);
  }

  // ── Strings ───────────────────────────────────────────────

  // All user-facing text lives in i18n/<lang>.json, the same file the Go side
  // reads. A key missing from LANG falls back to BASE_LANG, then to the key.
  const BASE_LANG = "ko";
  const LANG = "ko";
  const STRINGS = Object.assign(
    {},
    await loadStrings(BASE_LANG),
    LANG === BASE_LANG ? {} : await loadStrings(LANG)
  );

  async function loadStrings(lang) {
    try {
      const response = await fetch(`i18n/${lang}.json`);
      if (response.ok) return await response.json();
    } catch (_) {
      // Keys show instead of text; nothing else breaks.
    }
    return {};
  }

  // t returns key's text with "{name}" placeholders filled from vars.
  function t(key, vars) {
    const text = Object.prototype.hasOwnProperty.call(STRINGS, key) ? STRINGS[key] : key;
    if (!vars) return text;
    return text.replace(/\{(\w+)\}/g, (match, name) => (name in vars ? String(vars[name]) : match));
  }

  // Static text in index.html: data-i18n fills the text, data-i18n-aria-label,
  // data-i18n-title and data-i18n-placeholder fill those attributes.
  function applyStrings(root) {
    for (const el of root.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);
    for (const attr of ["aria-label", "title", "placeholder"]) {
      for (const el of root.querySelectorAll(`[data-i18n-${attr}]`)) {
        el.setAttribute(attr, t(el.getAttribute(`data-i18n-${attr}`)));
      }
    }
  }

  applyStrings(document);

  const $ = (id) => document.getElementById(id);

  const appEl = $("app");
  const modeToggle = $("mode-toggle");
  const addButton = $("add");
  const search = $("search");
  const list = $("list");
  const empty = $("empty");
  const helpButton = $("help");
  const gear = $("gear");
  const paneToggle = $("pane-toggle");
  const paneHead = $("pane-head");
  const paneBody = $("pane-body");

  const keysSheet = $("keys-sheet");
  const keysIntro = $("keys-intro");
  const keySource = $("key-source");
  const keyList = $("key-list");
  const keyCaption = $("key-caption");
  const keyPicker = $("key-picker");
  const keyPickerLabel = $("key-picker-label");
  const keyForm = $("key-form");
  const keyTier = $("key-tier");
  const keyValue = $("key-value");
  const keyError = $("key-error");
  const keySave = $("key-save");
  const helpSheet = $("help-sheet");
  const pdfSheet = $("pdf-sheet");
  const pdfForm = $("pdf-form");
  const pdfSub = $("pdf-sub");
  const pdfList = $("pdf-list");
  const pdfNone = $("pdf-none");
  const pdfCancel = $("pdf-cancel");

  const menu = $("menu");
  const toast = $("toast");
  const toastText = $("toast-text");
  const toastAction = $("toast-action");
  const announcePolite = $("announce-polite");
  const announceAssertive = $("announce-assertive");

  const KEY_SOURCE_URL = "https://aistudio.google.com/apikey";
  const IS_WINDOWS = /Windows/i.test(navigator.userAgent);
  const IS_MAC = /Macintosh|Mac OS X/i.test(navigator.userAgent);
  document.documentElement.classList.toggle("is-mac", IS_MAC);
  const REVEAL_LABEL = IS_WINDOWS ? t("menu.revealExplorer") : t("menu.revealFinder");
  const ACTION_WIDTH = 78; // px per swipe action button
  const UNDO_MS = 4000;

  const state = {
    items: [],
    keys: [],
    mode: "free",
    available: { free: false, paid: false },
    query: "",
    keyTier: "free",
    newItemKey: "", // toolbar choice for new files; "" = automatic
    openKey: "", // key whose settings panel is expanded
    pane: false, // transcript pane shown
    selected: "", // item shown in the pane
  };

  const GLYPHS = {
    file: `<svg class="glyph" viewBox="0 0 22 22" fill="none" stroke="currentColor" stroke-width="1.2"
      stroke-linejoin="round" aria-hidden="true">
      <path d="M5.5 2.5h7l4 4v13h-11z"/><path d="M12.5 2.5v4h4"/></svg>`,
    folder: `<svg class="glyph" viewBox="0 0 22 22" fill="none" stroke="currentColor" stroke-width="1.2"
      stroke-linejoin="round" aria-hidden="true">
      <path d="M2.5 5.5h6l2 2h9v10h-17z"/></svg>`,
    handle: `<svg width="14" height="10" viewBox="0 0 14 10" fill="none" stroke="currentColor"
      stroke-width="1.2" stroke-linecap="round" aria-hidden="true"><path d="M1.5 1.5h11M1.5 5h11M1.5 8.5h11"/></svg>`,
    chevron: `<svg width="7" height="11" viewBox="0 0 7 11" fill="none" stroke="currentColor"
      stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m1.5 1.5 4 4-4 4"/></svg>`,
    check: `<svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor"
      stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m2 6.4 2.6 2.6L10 3.4"/></svg>`,
  };

  // ── Backend ───────────────────────────────────────────────

  function call(method, ...args) {
    const api = window.go && window.go.app && window.go.app.App;
    if (!api || typeof api[method] !== "function") {
      return Promise.reject(new Error(t("error.notReady")));
    }
    return api[method](...args);
  }

  function errorText(error) {
    let text = "";
    if (typeof error === "string") text = error;
    else if (error && typeof error.message === "string") text = error.message;
    else if (error != null) text = String(error);
    return text.replace(/^Error:\s*/, "").trim() || t("error.generic");
  }

  function esc(value) {
    return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    })[ch]);
  }

  // ── Focus & announcer ─────────────────────────────────────
  //
  // Every surface that opens over the window (sheets, the PDF card, menus)
  // opens and closes through here, so focus is saved, moved in, kept in and
  // given back by one set of rules. Screen-reader messages go through
  // announce(), the only writer of the two live regions in index.html.

  // Open surfaces, innermost last: { el, returnTo, trap }.
  const surfaces = [];

  const FOCUSABLE = "button, input, select, textarea, a[href], [tabindex]:not([tabindex='-1'])";

  // Visible and enabled. display:none (the hidden attribute, or sitting in a
  // hidden sheet) leaves an element without client rects.
  function canFocus(el) {
    return Boolean(el && el.isConnected && !el.disabled && el.getClientRects().length);
  }

  // openSurface shows el and moves focus to `focus`: a first control, or el
  // itself (tabindex=-1). trap keeps Tab inside el until it closes. Opening
  // an open surface again only moves focus; it still returns to whatever
  // had focus when it first opened.
  function openSurface(el, { focus = el, trap = true } = {}) {
    if (!surfaces.some((s) => s.el === el)) {
      surfaces.push({ el, returnTo: document.activeElement, trap });
    }
    el.hidden = false;
    focus.focus();
  }

  // closeSurface hides el and, if focus was inside it (or already nowhere),
  // gives focus back to what had it before el opened. Focus the user has
  // since moved elsewhere, e.g. by clicking another control, stays there.
  function closeSurface(el) {
    const index = surfaces.findIndex((s) => s.el === el);
    if (index < 0) return;
    const [{ returnTo }] = surfaces.splice(index, 1);
    const active = document.activeElement;
    const lost = !active || active === document.body || el.contains(active);
    el.hidden = true;
    if (lost) restoreFocus(returnTo);
  }

  // Focus that started nowhere (body: WebKit does not focus a clicked
  // button) goes back to nowhere rather than jumping to a field. A saved
  // element that is gone or hidden falls back to the innermost open modal
  // surface, then the search field, then the empty-state button.
  function restoreFocus(target) {
    if (!target || target === document.body) {
      document.activeElement?.blur();
      return;
    }
    const modal = trapSurface();
    const next = [target, modal && modal.el, search, empty].find(canFocus);
    if (next) next.focus();
  }

  function trapSurface() {
    for (let i = surfaces.length - 1; i >= 0; i -= 1) {
      if (surfaces[i].trap) return surfaces[i];
    }
    return null;
  }

  // trapTab keeps Tab and Shift+Tab inside the innermost modal surface,
  // wrapping at either end. Focus on no control of it (outside, or on the
  // surface itself) goes to its first or, with Shift, last control. The
  // keydown handler in Events calls it.
  function trapTab(event) {
    const top = trapSurface();
    if (!top) return;
    const items = Array.from(top.el.querySelectorAll(FOCUSABLE)).filter(canFocus);
    const at = items.indexOf(document.activeElement);
    const last = items.length - 1;
    let next = null;
    if (!items.length) next = top.el;
    else if (at < 0) next = items[event.shiftKey ? last : 0];
    else if (event.shiftKey && at === 0) next = items[last];
    else if (!event.shiftKey && at === last) next = items[0];
    if (!next) return;
    event.preventDefault();
    next.focus();
  }

  // announce reads a short message aloud: polite by default, assertive only
  // for what must interrupt. The region is emptied first and filled a moment
  // later, so the same message twice in a row is read twice.
  const announceTimers = new Map();

  function announce(message, { assertive = false } = {}) {
    const region = assertive ? announceAssertive : announcePolite;
    clearTimeout(announceTimers.get(region));
    region.textContent = "";
    announceTimers.set(region, setTimeout(() => {
      region.textContent = message;
    }, 100));
  }

  // ── Toast ─────────────────────────────────────────────────

  let toastTimer = null;
  let toastOnAction = null;

  // The toast is visual only; screen readers hear the message through
  // announce(), so it is not read twice.
  function showToast(message, options = {}) {
    clearTimeout(toastTimer);
    toastText.textContent = message;
    announce(message);
    toastOnAction = options.onAction || null;
    toastAction.hidden = !toastOnAction;
    if (toastOnAction) toastAction.textContent = options.action || t("toast.undo");
    toast.classList.add("is-visible");
    toastTimer = setTimeout(hideToast, options.duration || 1800);
  }

  function hideToast() {
    clearTimeout(toastTimer);
    toast.classList.remove("is-visible");
    toastOnAction = null;
  }

  // ── Mode ──────────────────────────────────────────────────

  // Which tiers have keys, and the mode in effect, come from the backend so
  // the GUI and CLI agree. A tier without keys looks dimmed but stays
  // clickable, so a click can explain why instead of doing nothing.
  function renderMode() {
    const { free, paid } = state.available;
    modeToggle.hidden = !free && !paid;
    for (const button of modeToggle.querySelectorAll("button")) {
      const selected = button.dataset.mode === state.mode;
      button.setAttribute("aria-selected", String(selected));
      const missing = button.dataset.mode === "batch" ? !paid : !free;
      button.setAttribute("aria-disabled", String(missing));
    }
    renderPicker();
  }

  // First click on a tier without keys: nudge the control and say why, with
  // a way to add one. Clicking it again while that note is up opens the key
  // sheet on that tier.
  let missingTierNote = null; // { mode, until }

  function explainMissingTier(mode) {
    const tier = mode === "batch" ? "paid" : "free";
    if (missingTierNote && missingTierNote.mode === mode && Date.now() < missingTierNote.until) {
      missingTierNote = null;
      hideToast();
      openKeys(tier);
      return;
    }
    modeToggle.classList.remove("is-nudging");
    void modeToggle.offsetWidth; // restart the animation
    modeToggle.classList.add("is-nudging");
    const duration = 3200;
    missingTierNote = { mode, until: Date.now() + duration };
    showToast(tier === "paid" ? t("error.noPaidKey") : t("error.noFreeKey"), {
      action: t("toast.addKey"),
      duration,
      onAction: () => {
        missingTierNote = null;
        openKeys(tier);
      },
    });
  }

  // A drop or "+" with no key at all: the same note, and doing it again
  // while the note is up opens the key sheet.
  function explainMissingKey() {
    if (missingTierNote && missingTierNote.mode === "any" && Date.now() < missingTierNote.until) {
      missingTierNote = null;
      hideToast();
      openKeys("free");
      return;
    }
    const duration = 3200;
    missingTierNote = { mode: "any", until: Date.now() + duration };
    showToast(t("error.keyRequired"), {
      action: t("toast.addKey"),
      duration,
      onAction: () => {
        missingTierNote = null;
        openKeys("free");
      },
    });
  }

  const hasKeys = () => state.available.free || state.available.paid;

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

  async function loadModeState() {
    try {
      const modeState = await call("ModeState");
      state.available = {
        free: Boolean(modeState.Free),
        paid: Boolean(modeState.Paid),
      };
      if (modeState.Mode === "free" || modeState.Mode === "batch") {
        state.mode = modeState.Mode;
      }
    } catch (error) {
      showToast(errorText(error));
    }
    renderMode();
  }

  // ── Rows ──────────────────────────────────────────────────

  const AUDIO_EXT = /\.(m4a|mp4|mp3|wav)$/i;

  // Display names drop the audio extension; folder names stay as they are.
  function displayName(item) {
    const name = item.Name || "";
    return item.Kind === "folder" ? name : name.replace(AUDIO_EXT, "") || name;
  }

  const pad2 = (n) => String(n).padStart(2, "0");

  function parseDate(stamp) {
    const date = new Date(Date.parse(stamp || ""));
    return Number.isNaN(date.getTime()) || date.getFullYear() < 2000 ? null : date;
  }

  function startOfToday() {
    const now = new Date();
    return new Date(now.getFullYear(), now.getMonth(), now.getDate());
  }

  function dayName(date) {
    const day = t("date.monthDay", { month: date.getMonth() + 1, day: date.getDate() });
    return date.getFullYear() === new Date().getFullYear()
      ? day
      : t("date.withYear", { year: date.getFullYear(), date: day });
  }

  // When a finished item finished: "오늘 21:08", "어제 14:02", "11월 2일",
  // "2025년 11월 2일".
  function finishedAt(stamp) {
    const date = parseDate(stamp);
    if (!date) return "";
    const today = startOfToday();
    const time = `${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
    if (date >= today) return t("date.today", { time });
    if (date >= new Date(today.getTime() - 86400000)) return t("date.yesterday", { time });
    return dayName(date);
  }

  // Finished items keep the time they finished; renaming does not move it.
  // Items from before FinishedAt existed use their last change.
  function finishStamp(item) {
    return parseDate(item.FinishedAt) ? item.FinishedAt : item.UpdatedAt || item.CreatedAt;
  }

  // Day headers group by the day an item was added (the list order).
  function dayKey(item) {
    const date = parseDate(item.CreatedAt);
    return date ? `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}` : "";
  }

  function dayLabel(item) {
    const date = parseDate(item.CreatedAt);
    if (!date) return "";
    const today = startOfToday();
    if (date >= today) return t("date.todaySection");
    if (date >= new Date(today.getTime() - 86400000)) return t("date.yesterdaySection");
    return dayName(date);
  }

  function minutesSince(stamp) {
    const date = parseDate(stamp);
    if (!date) return 0;
    return Math.max(0, Math.floor((Date.now() - date.getTime()) / 60000));
  }

  // "진행 중 · 4분"; under a minute, the state alone.
  function withElapsed(text, stamp) {
    const minutes = minutesSince(stamp);
    return minutes < 1 ? text : t("row.elapsed", { status: text, minutes });
  }

  // "22분", "1시간 5분", "2시간".
  function audioLength(seconds) {
    if (!(seconds > 0)) return "";
    const total = Math.max(1, Math.round(seconds / 60));
    const hours = Math.floor(total / 60);
    const minutes = total % 60;
    if (!hours) return t("duration.minutes", { minutes });
    return minutes ? t("duration.hoursMinutes", { hours, minutes }) : t("duration.hours", { hours });
  }

  const won = (amount) => Number(amount).toLocaleString("ko-KR");

  const isPaid = (item) => item.Mode === "batch";

  // The cost shown for an item: what it cost once done (paid requests only),
  // otherwise the paid estimate. Free items have none; their tier says it.
  function costText(item) {
    if (!isPaid(item)) return "";
    if (item.State === "done" && item.CostKRW != null) return t("cost.won", { amount: won(item.CostKRW) });
    if (item.EstimateKRW > 0) return t("cost.estimate", { amount: won(item.EstimateKRW) });
    return "";
  }

  const isQueued = (item) => item.State === "running" && item.Detail === t("status.queued");
  const isRetrying = (item) => item.State === "running" && item.Detail === t("status.retrying");

  // Each state as one line: tone (dot colour), text and the moving line
  // under the row. Free or paid shows in the pane, not here. What the row
  // can do comes from rowActions.
  function rowState(item) {
    switch (item.State) {
      case "running": {
        if (isQueued(item)) return { tone: "quiet", dot: false, text: t("status.queued") };
        if (isRetrying(item)) {
          return { tone: "waiting", dot: true, text: item.Detail, line: "waiting" };
        }
        const progress = Number(item.Progress) || 0;
        return {
          tone: "running",
          dot: true,
          text: withElapsed(t("row.running"), item.StartedAt || item.CreatedAt),
          line: progress > 0 ? "progress" : "moving",
          progress,
        };
      }
      case "batch_waiting": {
        const since = (item.Batch && item.Batch.SubmittedAt) || item.StartedAt || item.CreatedAt;
        return { tone: "waiting", dot: true, text: withElapsed(t("row.waiting"), since), line: "waiting" };
      }
      case "free_exhausted":
        return { tone: "alert", dot: true, text: t("row.freeExhausted") };
      case "failed":
        return {
          tone: "failed",
          dot: true,
          text: item.Detail ? t("row.failedReason", { reason: item.Detail }) : t("row.failed"),
          title: item.Detail,
        };
      case "canceled":
        return { tone: "quiet", dot: false, text: t("status.canceled") };
      case "done":
        return { tone: "", dot: false, text: finishedAt(finishStamp(item)) };
      default:
        return { tone: "quiet", dot: false, text: item.Detail || t("status.queued") };
    }
  }

  function lineHTML(s) {
    if (s.line === "progress") {
      const percent = Math.min(100, Math.round(s.progress * 100));
      return `<div class="row-line" role="progressbar" aria-valuemin="0" aria-valuemax="100"
        aria-valuenow="${percent}"><span style="width:${percent}%"></span></div>`;
    }
    if (s.line === "moving") return `<div class="row-line is-moving" role="progressbar"><span></span></div>`;
    if (s.line === "waiting") return `<div class="row-line is-moving is-waiting" role="progressbar"><span></span></div>`;
    return "";
  }

  // The row's own button: how to get a halted item going again (its lead
  // "resume" action). It takes the red of a failed row.
  function rowActionHTML(item, s) {
    const entry = rowActions(item).find((a) => a.lead && a.group === "resume");
    if (!entry) return "";
    return `<button class="row-action${s.tone === "failed" ? " is-danger" : ""}" type="button"
      data-action="${entry.action}" data-id="${esc(item.ID)}">${esc(entry.label)}</button>`;
  }

  function isActive(item) {
    return item.State === "running" || item.State === "batch_waiting";
  }

  function rowHTML(item) {
    const id = esc(item.ID);
    const s = rowState(item);
    const swiped = swipeActions(item).map((entry) =>
      `<button class="act-${entry.danger ? "delete" : "rename"}" type="button" data-action="${entry.action}"
        data-id="${id}">${esc(entry.label)}</button>`).join("");
    return `
      <div class="row-actions">${swiped}</div>
      <div class="row-main">
        ${item.Kind === "folder" ? GLYPHS.folder : GLYPHS.file}
        <div class="row-body">
          <div class="name" title="${esc(item.Source || item.Name)}">${esc(displayName(item))}</div>
          <div class="status${s.tone ? ` tone-${s.tone}` : ""}">${s.dot ? `<i class="dot"></i>` : ""}<span
            ${s.title ? `title="${esc(s.title)}"` : ""}>${esc(s.text)}</span></div>
        </div>
        ${rowActionHTML(item, s)}
        ${lineHTML(s)}
      </div>`;
  }

  // Rows are kept and patched in place so an open swipe, a rename field or a
  // running animation survives the frequent "items:changed" refreshes.
  const rowCache = new Map(); // id -> { el, html }
  const sectionCache = new Map(); // day key -> header element
  const hidden = new Set(); // ids removed from view while undo is possible
  let renaming = null; // { id, input }
  let shownOrder = []; // ids as last rendered, for picking a neighbour

  // The list is a listbox: rows are options it points at through
  // aria-activedescendant, so focus stays on the list while rows are patched.
  const rowDomID = (id) => `row-${id}`;

  function visibleItems() {
    return state.items.filter((item) => !hidden.has(item.ID));
  }

  // The search field gets a hairline only while the list is scrolled; the
  // footer gets one only while more rows are hidden below.
  function syncScrolled() {
    appEl.classList.toggle("is-scrolled", list.scrollTop > 0);
    appEl.classList.toggle(
      "is-more-below",
      list.scrollTop + list.clientHeight < list.scrollHeight - 1
    );
  }

  function renderItems() {
    const items = visibleItems();
    const isEmpty = items.length === 0 && !state.query;
    appEl.classList.toggle("is-empty", isEmpty);
    ensureSelection(items);
    shownOrder = items.map((item) => item.ID);

    if (items.length === 0) {
      rowCache.clear();
      sectionCache.clear();
      list.innerHTML = state.query ? `<p class="list-note">${t("list.noResults")}</p>` : "";
      paintSelection();
      syncScrolled();
      renderPane();
      return;
    }
    const note = list.querySelector(".list-note");
    if (note) note.remove();

    const multiDay = new Set(items.map(dayKey)).size > 1;
    const keep = new Set();
    const keepSections = new Set();
    let previous = null;
    let lastDay = null;
    const place = (el) => {
      const next = previous ? previous.nextSibling : list.firstChild;
      if (next !== el) list.insertBefore(el, next);
      previous = el;
    };
    for (const item of items) {
      if (multiDay) {
        const day = dayKey(item);
        if (day !== lastDay) {
          lastDay = day;
          let header = sectionCache.get(day);
          if (!header) {
            header = document.createElement("div");
            header.className = "list-section";
            // A visual divider: the rows are the only options.
            header.setAttribute("role", "presentation");
            sectionCache.set(day, header);
          }
          const label = dayLabel(item);
          if (header.textContent !== label) header.textContent = label;
          keepSections.add(day);
          place(header);
        }
      }
      keep.add(item.ID);
      const html = rowHTML(item);
      let entry = rowCache.get(item.ID);
      if (!entry) {
        const el = document.createElement("div");
        el.className = "row";
        el.dataset.id = item.ID;
        el.id = rowDomID(item.ID);
        el.setAttribute("role", "option");
        entry = { el, html: "" };
        rowCache.set(item.ID, entry);
      }
      const el = entry.el;
      const isRenaming = renaming && renaming.id === item.ID;
      if (entry.html !== html && !isRenaming) {
        entry.html = html;
        el.innerHTML = html;
        const offset = swipe.offsets.get(item.ID);
        if (offset) setOffset(el, offset, false);
      }
      place(el);
    }
    for (const [id, entry] of rowCache) {
      if (!keep.has(id)) {
        entry.el.remove();
        rowCache.delete(id);
        swipe.offsets.delete(id);
      }
    }
    for (const [day, header] of sectionCache) {
      if (!keepSections.has(day)) {
        header.remove();
        sectionCache.delete(day);
      }
    }
    paintSelection();
    syncScrolled();
    renderPane();
  }

  function itemByID(id) {
    return state.items.find((item) => item.ID === id) || null;
  }

  // "items:changed" arrives once per change (a folder of files, a running
  // job), so refreshing on every event would run ListItems once per event.
  // Every refresh funnels through here: at most one request in flight, at most
  // one trailing request, and never two closer than REFRESH_MIN_GAP_MS. A user
  // action still lands at once, because the gap is counted from the last load.
  const REFRESH_MIN_GAP_MS = 120;

  let listRequest = 0;

  async function loadItems() {
    const token = ++listRequest;
    const items = await call("ListItems", state.query);
    if (token !== listRequest) return; // a newer search won
    state.items = Array.isArray(items) ? items : [];
    renderItems();
  }

  let itemsLoading = null; // the load in flight, shared by every refreshItems
  let itemsDirty = false; // refreshed during a load: run one trailing load
  let itemsTimer = null; // trailing load waiting for the minimum gap
  let itemsLastEnd = 0; // when the last load settled
  let itemsWaiters = []; // callers waiting for a load that starts after them

  function startRefreshItems() {
    clearTimeout(itemsTimer);
    itemsTimer = null;
    itemsDirty = false;
    const waiters = itemsWaiters;
    itemsWaiters = [];
    const loading = (async () => {
      try {
        await loadItems();
      } catch (error) {
        // One toast per failed load, not one per caller waiting on it.
        showToast(errorText(error));
      }
      itemsLastEnd = Date.now();
      itemsLoading = null;
      for (const resolve of waiters) resolve();
      if (itemsDirty) {
        const wait = REFRESH_MIN_GAP_MS - (Date.now() - itemsLastEnd);
        if (wait > 0) itemsTimer = setTimeout(startRefreshItems, wait);
        else startRefreshItems();
      }
    })();
    itemsLoading = loading;
    return loading;
  }

  // The returned promise settles when a load that started after this call
  // finishes, so awaiting refreshItems() shows the latest list.
  function refreshItems() {
    if (itemsLoading || itemsTimer) {
      itemsDirty = true;
      return new Promise((resolve) => itemsWaiters.push(resolve));
    }
    const wait = REFRESH_MIN_GAP_MS - (Date.now() - itemsLastEnd);
    if (wait > 0) {
      itemsDirty = true;
      itemsTimer = setTimeout(startRefreshItems, wait);
      return new Promise((resolve) => itemsWaiters.push(resolve));
    }
    return startRefreshItems();
  }

  // ── Selection and the transcript pane ─────────────────────
  //
  // The pane shows the selected item: its transcript when done, otherwise
  // its state. With the pane open something is always selected while the
  // list has rows (the first row by default), like a mail list.

  // A selected item that leaves the list (deleted, filtered out) hands the
  // selection to the row that takes its place, or to the new last row.
  function ensureSelection(items) {
    if (state.selected && items.some((item) => item.ID === state.selected)) return;
    const gone = shownOrder.indexOf(state.selected);
    if (gone >= 0 && items.length) state.selected = items[Math.min(gone, items.length - 1)].ID;
    else state.selected = state.pane && items.length ? items[0].ID : "";
  }

  // The selected row is also the list's active option.
  function paintSelection() {
    for (const [id, entry] of rowCache) {
      const on = id === state.selected;
      entry.el.classList.toggle("is-selected", on);
      entry.el.setAttribute("aria-selected", String(on));
    }
    const active = rowCache.get(state.selected);
    if (active) list.setAttribute("aria-activedescendant", active.el.id);
    else list.removeAttribute("aria-activedescendant");
  }

  function select(id) {
    if (state.selected === id) return;
    state.selected = id;
    paintSelection();
    renderPane();
  }

  // Keyboard selection: the active row, scrolled into view.
  function selectAt(items, index) {
    const item = items[index];
    if (!item) return;
    select(item.ID);
    rowCache.get(item.ID)?.el.scrollIntoView({ block: "nearest" });
  }

  function setPane(open, save = true) {
    state.pane = open;
    appEl.classList.toggle("has-pane", open);
    paneToggle.setAttribute("aria-pressed", String(open));
    renderItems();
    if (save) call("SetPane", open).catch((error) => showToast(errorText(error)));
  }

  let paneHeadHTML = "";
  let paneBodyKey = ""; // what the body shows: "text:<id>|<output>", "state:<html>" or "empty"

  function paneMeta(item) {
    const parts = [];
    if (item.State === "done") parts.push(finishedAt(finishStamp(item)));
    parts.push(audioLength(item.AudioSec));
    parts.push(isPaid(item) ? t("tier.paid") : t("tier.free"));
    if (item.KeyLabel) parts.push(item.KeyLabel);
    parts.push(costText(item));
    return parts.filter(Boolean).map(esc).join(" · ");
  }

  // The pane offers the item's lead actions. Copying a finished transcript
  // is the main one; resuming a halted item is accented.
  const PANE_BUTTON = { copy: "primary-button", resume: "plain-button is-accent" };

  function paneActionsHTML(item) {
    const id = esc(item.ID);
    return rowActions(item).filter((entry) => entry.lead).map((entry) => {
      const cls = PANE_BUTTON[entry.action] || PANE_BUTTON[entry.group] || "plain-button";
      return `<button class="${cls}" type="button" data-action="${entry.action}" data-id="${id}">${esc(entry.label)}</button>`;
    }).join("");
  }

  // A non-finished item: its state line, then what else is known.
  function paneStateHTML(item) {
    const s = rowState(item);
    const notes = [];
    switch (item.State) {
      case "running":
        if (!isQueued(item) && !isRetrying(item) && item.Detail) notes.push(item.Detail);
        if (item.Progress > 0) notes.push(t("pane.progress", { percent: Math.round(item.Progress * 100) }));
        break;
      case "batch_waiting":
        notes.push(t("pane.waitingNote"));
        break;
      case "free_exhausted":
        if (item.Detail) notes.push(item.Detail);
        notes.push(t("pane.exhaustedNote"));
        break;
      default:
        break;
    }
    return `<p class="pane-state${s.tone ? ` tone-${s.tone}` : ""}">${s.dot ? `<i class="dot"></i>` : ""}<span>${esc(s.text)}</span></p>`
      + notes.map((note) => `<p class="pane-note">${esc(note)}</p>`).join("");
  }

  // Paragraphs split on blank lines; single line breaks stay (pre-wrap).
  function transcriptHTML(out) {
    const paragraphs = String(out.Text || "")
      .replace(/\r\n?/g, "\n")
      .split(/\n[ \t]*\n+/)
      .map((p) => p.replace(/^\n+|\s+$/g, ""))
      .filter(Boolean);
    if (!paragraphs.length) return `<p class="pane-note">${t("pane.emptyText")}</p>`;
    const note = out.Truncated ? `<p class="pane-note">${t("pane.truncated")}</p>` : "";
    return `<div class="pane-text">${paragraphs.map((p) => `<p>${esc(p)}</p>`).join("")}</div>${note}`;
  }

  async function loadTranscript(item, key) {
    paneBody.innerHTML = "";
    try {
      const out = await call("ReadOutput", item.ID);
      if (paneBodyKey !== key) return; // selection moved on
      paneBody.innerHTML = transcriptHTML(out || {});
    } catch (error) {
      if (paneBodyKey !== key) return;
      paneBody.innerHTML = `<p class="pane-note">${esc(errorText(error))}</p>`;
    }
    paneBody.scrollTop = 0;
  }

  function renderPane() {
    if (!state.pane) return;
    const item = state.selected ? itemByID(state.selected) : null;
    const head = item ? `
      <h1 class="pane-title" title="${esc(item.Source || item.Name)}">${esc(displayName(item))}</h1>
      <p class="pane-meta">${paneMeta(item)}</p>
      <div class="pane-actions">${paneActionsHTML(item)}</div>` : "";
    if (head !== paneHeadHTML) {
      paneHeadHTML = head;
      paneHead.innerHTML = head;
    }
    if (!item) {
      if (paneBodyKey !== "empty") {
        paneBodyKey = "empty";
        paneBody.innerHTML = state.items.length || state.query
          ? `<p class="pane-empty">${t("pane.empty")}</p>`
          : "";
      }
      return;
    }
    if (item.State === "done") {
      const key = `text:${item.ID}|${item.Output}`;
      if (paneBodyKey !== key) {
        paneBodyKey = key;
        loadTranscript(item, key);
      }
      return;
    }
    const html = paneStateHTML(item);
    const key = `state:${item.ID}|${html}`;
    if (paneBodyKey !== key) {
      paneBodyKey = key;
      paneBody.innerHTML = html;
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
    showToast(t("toast.removed"), {
      action: t("toast.undo"),
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
    input.setAttribute("aria-label", t("field.name"));
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
    // Enter or Escape (not a blur elsewhere): the field is about to be
    // replaced by the name, so focus goes back to the list it sits in.
    const keepFocus = document.activeElement === current.input;
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
    if (keepFocus) list.focus();
    await refreshItems();
  }

  // ── Row actions ───────────────────────────────────────────
  //
  // rowActions(item) lists every action an item offers in its state, in
  // menu order; runAction(action, id) performs one. The context menu shows
  // the whole list. The swipe buttons, the row button and the pane each pick
  // from it by group or lead, so anything a gesture offers is also in the
  // menu, and a keyboard path can open that same menu.
  //   group  "output" (finished transcript), "resume" (start a halted item
  //          again or move it to paid), "stop", "edit" (rename, delete)
  //   lead   the state's main actions, shown in the pane
  //   danger destructive

  function rowActions(item) {
    const actions = [];
    const add = (action, label, group, extra) => actions.push({ action, label, group, lead: false, ...extra });
    // "유료로 이어서" needs a paid key.
    const toPaid = (extra) => {
      if (state.available.paid) add("batch", t("action.continuePaid"), "resume", extra);
    };
    switch (item.State) {
      case "done":
        add("open", t("menu.open"), "output");
        add("copy", t("menu.copy"), "output", { lead: true });
        add("reveal", REVEAL_LABEL, "output", { lead: true });
        break;
      case "running":
        // A free item can move to paid mid-run (e.g. while the server is busy).
        if (item.Mode !== "batch") toPaid();
        add("cancel", t("action.cancel"), "stop", { lead: true });
        break;
      case "batch_waiting":
        add("cancel", t("action.cancel"), "stop", { lead: true });
        break;
      case "free_exhausted":
        // Paid is the only way on, so it is offered even without a paid key:
        // runAction then opens the key sheet to add one.
        add("batch", t("action.continuePaid"), "resume", { lead: true });
        add("cancel", t("action.cancel"), "stop");
        break;
      case "failed":
      case "canceled":
        add("retry", t("action.retry"), "resume", { lead: true });
        if (item.Mode !== "batch") toPaid();
        break;
      default:
        break;
    }
    add("rename", t("action.rename"), "edit");
    add("delete", t("action.delete"), "edit", { danger: true });
    return actions;
  }

  // Swiping reveals two buttons: stopping a working item (or else renaming
  // it), then deleting it.
  function swipeActions(item) {
    const actions = rowActions(item);
    const first = actions.find((a) => a.lead && a.group === "stop") || actions.find((a) => a.action === "rename");
    return [first, actions.find((a) => a.danger)];
  }

  async function runAction(action, id) {
    try {
      switch (action) {
        case "open":
          await call("OpenOutput", id);
          break;
        case "copy":
          await call("CopyOutput", id);
          showToast(t("toast.copied"));
          break;
        case "reveal":
          await call("RevealOutput", id);
          break;
        case "retry":
          await call("Retry", id);
          break;
        case "batch":
          if (!state.available.paid) {
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

  // Double-click and Enter open a finished transcript in its default app.
  function openRow(id) {
    const item = itemByID(id);
    if (item && item.State === "done") runAction("open", id);
  }

  // ── Context menu ──────────────────────────────────────────

  let menuRow = null;

  // menuRow is the trigger of the open menu: a row, or the key picker. The
  // picker stays on screen and says whether its menu is open with
  // aria-expanded, the way paneToggle says its state with aria-pressed; a row
  // menu has no persistent trigger to carry it.
  function setMenuRow(el) {
    menuRow = el;
    keyPicker.setAttribute("aria-expanded", String(el === keyPicker));
  }

  // The whole list, with a rule before rename and delete.
  function menuHTML(item) {
    return rowActions(item).map((entry, i, all) => {
      const rule = i > 0 && entry.group === "edit" && all[i - 1].group !== "edit" ? "<hr>" : "";
      return `${rule}<button type="button" role="menuitem" data-action="${entry.action}"
        ${entry.danger ? 'class="is-danger"' : ""}>${esc(entry.label)}</button>`;
    }).join("");
  }

  // `at` is the point to open at: the contextmenu event, or { clientX,
  // clientY } under the row when the keyboard opens the menu (rowMenuPoint).
  function openMenu(at, item, rowEl) {
    closeMenu();
    menu.innerHTML = menuHTML(item);
    menu.dataset.id = item.ID;
    openSurface(menu, { trap: false });
    setMenuRow(rowEl);
    rowEl.classList.add("is-menu");

    const { innerWidth, innerHeight } = window;
    const rect = menu.getBoundingClientRect();
    const x = Math.min(at.clientX, innerWidth - rect.width - 6);
    const y = at.clientY + rect.height > innerHeight - 6
      ? Math.max(6, at.clientY - rect.height)
      : at.clientY;
    menu.style.left = `${Math.max(6, x)}px`;
    menu.style.top = `${y}px`;
  }

  // Under the row's name, where a pointer would have opened it.
  function rowMenuPoint(rowEl) {
    const body = rowEl.querySelector(".row-body") || rowEl;
    return { clientX: body.getBoundingClientRect().left, clientY: rowEl.getBoundingClientRect().bottom };
  }

  // Arrow keys, Home and End walk the menu's items, wrapping at either end.
  // The menu itself has focus when it opens; ↓ then goes to the first item
  // and ↑ to the last. Enter and Space are the items' own button clicks.
  function moveMenuFocus(key) {
    const items = Array.from(menu.querySelectorAll("button")).filter(canFocus);
    if (!items.length) return;
    const at = items.indexOf(document.activeElement);
    const last = items.length - 1;
    let next = 0;
    if (key === "End" || (key === "ArrowUp" && at <= 0)) next = last;
    else if (key === "ArrowUp") next = at - 1;
    else if (key === "ArrowDown" && at >= 0 && at < last) next = at + 1;
    items[next].focus();
  }

  function closeMenu() {
    if (menu.hidden) return;
    closeSurface(menu);
    menu.classList.remove("has-checks");
    menuRow?.classList.remove("is-menu");
    setMenuRow(null);
  }

  // WKWebView can leave :hover stuck on rows the pointer already left (seen
  // when moving straight up the key list), so rows get an is-hover class from
  // JS instead, one row at a time.
  function trackHover(container, selector) {
    let current = null;
    const set = (row) => {
      if (row === current) return;
      current?.classList.remove("is-hover");
      current = row;
      row?.classList.add("is-hover");
    };
    container.addEventListener("pointermove", (event) => {
      set(event.target.closest(selector));
    });
    container.addEventListener("pointerleave", () => set(null));
  }

  // ── Keys ──────────────────────────────────────────────────

  const tierName = (tier) => (tier === "free" ? t("tier.free") : t("tier.paid"));

  function keyPanelHTML(key) {
    if (key.Source === "env") {
      return `
        <div class="key-panel">
          <p class="key-panel-note">${t("error.envKeyLocked")}</p>
        </div>`;
    }
    const tierButton = (tier) => `<button type="button" role="tab" data-key-tier="${tier}"
      aria-selected="${key.Tier === tier}">${tierName(tier)}</button>`;
    return `
      <div class="key-panel">
        <label class="key-field-label" for="key-name">${t("field.name")}</label>
        <input class="field key-name" id="key-name" type="text" spellcheck="false"
               autocomplete="off" value="${esc(key.Label)}" data-original="${esc(key.Label)}">
        <span class="key-field-label">${t("keys.tier")}</span>
        <div class="segmented segmented-wide" role="tablist" aria-label="${t("keys.tier")}">
          ${tierButton("free")}${tierButton("paid")}
        </div>
        <button class="danger-button" type="button" data-key-delete="${esc(key.ID)}">${t("keys.delete")}</button>
      </div>`;
  }

  function keyHTML(key) {
    const tier = tierName(key.Tier);
    const isEnv = key.Source === "env";
    const isOpen = state.openKey === key.ID;
    const showTier = !String(key.Label || "").includes(tier);
    return `
      <div class="key-item${isOpen ? " is-open" : ""}${isEnv ? " is-env" : ""}" data-key-id="${esc(key.ID)}">
        <div class="key-row" role="button" tabindex="0" aria-expanded="${isOpen}">
          ${isEnv
            ? `<span class="key-handle-spacer"></span>`
            : `<span class="key-handle" title="${t("keys.dragHandle")}">${GLYPHS.handle}</span>`}
          <span class="key-label" title="${esc(key.Label)}">${esc(key.Label)}</span>
          ${showTier ? `<span class="key-meta">${tier}</span>` : ""}
          ${isEnv && key.Label !== t("key.envLabel") ? `<span class="key-meta">${t("key.envLabel")}</span>` : ""}
          <span class="key-mask">${esc(key.Masked)}</span>
          <span class="key-chevron">${GLYPHS.chevron}</span>
        </div>
        ${isOpen ? keyPanelHTML(key) : ""}
      </div>`;
  }

  const hiddenKeys = new Set(); // keys removed from view while undo is possible
  let pendingKeyDelete = null; // { id, timer }

  function commitKeyDelete() {
    if (!pendingKeyDelete) return;
    const { id, timer } = pendingKeyDelete;
    clearTimeout(timer);
    pendingKeyDelete = null;
    call("DeleteKey", id)
      .catch((error) => showToast(errorText(error)))
      .finally(async () => {
        hiddenKeys.delete(id);
        await reloadKeys();
      });
  }

  function deleteKeyWithUndo(id) {
    commitKeyDelete();
    hiddenKeys.add(id);
    if (state.openKey === id) state.openKey = "";
    renderKeys();
    renderPicker();
    pendingKeyDelete = { id, timer: setTimeout(commitKeyDelete, UNDO_MS) };
    showToast(t("toast.keyDeleted"), {
      action: t("toast.undo"),
      duration: UNDO_MS,
      onAction: () => {
        if (!pendingKeyDelete || pendingKeyDelete.id !== id) return;
        clearTimeout(pendingKeyDelete.timer);
        pendingKeyDelete = null;
        hiddenKeys.delete(id);
        renderKeys();
        renderPicker();
      },
    });
  }

  function visibleKeys() {
    return state.keys.filter((key) => !hiddenKeys.has(key.ID));
  }

  function keyByID(id) {
    return state.keys.find((key) => key.ID === id) || null;
  }

  function renderKeys() {
    if (keyDrag) return; // the drag owns the list until it drops
    const keys = visibleKeys();
    if (state.openKey && !keys.some((key) => key.ID === state.openKey)) state.openKey = "";

    // A refresh while the name field has focus keeps what was typed.
    const active = document.activeElement;
    const typing = active && active.classList.contains("key-name") && keyList.contains(active)
      ? { value: active.value, start: active.selectionStart, end: active.selectionEnd }
      : null;

    keyList.innerHTML = keys.map(keyHTML).join("");
    keyCaption.hidden = keys.length < 2;
    // Handles only make sense when there is something to reorder.
    keyList.classList.toggle("no-order", keys.filter((key) => key.Source !== "env").length < 2);
    keysIntro.hidden = state.keys.length > 0;
    for (const button of keyTier.querySelectorAll("button")) {
      button.setAttribute("aria-selected", String(button.dataset.tier === state.keyTier));
    }

    if (typing) {
      const input = keyList.querySelector(".key-name");
      if (input) {
        input.value = typing.value;
        input.focus();
        input.setSelectionRange(typing.start, typing.end);
      }
    }
  }

  async function loadKeys() {
    const [keys, newItemKey] = await Promise.all([
      call("Keys"),
      call("NewItemKey").catch(() => ""),
    ]);
    state.keys = Array.isArray(keys) ? keys : [];
    state.newItemKey = typeof newItemKey === "string" ? newItemKey : "";
    renderKeys();
    await loadModeState(); // also renders the picker
    // Availability decides whether "유료로 이어서" shows on exhausted rows.
    renderItems();
  }

  // "keys:changed" can arrive right after a call that already reloads, so
  // overlapping reloads collapse into one more run.
  let keysLoading = null;
  let keysReloadAgain = false;

  function reloadKeys() {
    if (keysLoading) {
      keysReloadAgain = true;
      return keysLoading;
    }
    keysLoading = (async () => {
      try {
        do {
          keysReloadAgain = false;
          await loadKeys();
        } while (keysReloadAgain);
      } catch (error) {
        showToast(errorText(error));
      } finally {
        keysLoading = null;
      }
    })();
    return keysLoading;
  }

  function showKeyError(message) {
    keyError.textContent = message;
    keyError.hidden = false;
  }

  // ── Key settings panel ────────────────────────────────────

  function toggleKeyPanel(id) {
    state.openKey = state.openKey === id ? "" : id;
    keyError.hidden = true;
    renderKeys();
  }

  function collapseKeyPanel() {
    if (!state.openKey) return false;
    state.openKey = "";
    renderKeys();
    return true;
  }

  async function commitKeyName(input) {
    const id = input.closest(".key-item")?.dataset.keyId;
    const original = input.dataset.original || "";
    const value = input.value.trim();
    if (!input.isConnected) return; // removed by a refresh, not a real blur
    if (!id || !value || value === original) {
      input.value = original;
      return;
    }
    input.dataset.original = value; // a second blur does not resend
    try {
      await call("RenameKey", id, value);
      keyError.hidden = true;
    } catch (error) {
      input.dataset.original = original;
      showKeyError(errorText(error));
    }
    await reloadKeys();
  }

  async function setKeyTier(id, tier) {
    const key = keyByID(id);
    if (!key || key.Tier === tier) return;
    keyError.hidden = true;
    key.Tier = tier; // show the choice at once; the reload confirms it
    renderKeys();
    try {
      await call("SetKeyTier", id, tier);
    } catch (error) {
      showKeyError(errorText(error));
    }
    await reloadKeys();
  }

  // ── Key order (drag) ──────────────────────────────────────
  //
  // Pointer events instead of HTML5 drag and drop, which is unreliable in
  // WKWebView. The item follows the pointer; the list reorders under it.

  let keyDrag = null; // { el, pointer, startY, originTop, active, before }

  function savedKeyItems() {
    return [...keyList.querySelectorAll(".key-item:not(.is-env)")];
  }

  // Siblings slide into their new place instead of jumping.
  function slideFrom(tops) {
    for (const [el, top] of tops) {
      const delta = top - el.offsetTop;
      if (!delta) continue;
      el.style.transition = "none";
      el.style.transform = `translateY(${delta}px)`;
      void el.offsetWidth; // apply the start position before animating
      el.style.transition = "transform 0.15s ease";
      el.style.transform = "";
    }
  }

  function onKeyDragStart(event) {
    if (event.button !== 0) return;
    const handle = event.target.closest(".key-handle");
    if (!handle) return;
    event.preventDefault();
    const el = handle.closest(".key-item");
    const active = document.activeElement;
    if (active && active.classList.contains("key-name")) active.blur(); // commits
    if (state.openKey) {
      // Panels change row heights; close them before measuring.
      state.openKey = "";
      renderKeys();
    }
    const fresh = keyList.querySelector(`.key-item[data-key-id="${CSS.escape(el.dataset.keyId)}"]`);
    if (!fresh) return;
    const items = savedKeyItems();
    const last = items[items.length - 1];
    keyDrag = {
      el: fresh,
      minTop: items[0].offsetTop,
      maxBottom: last.offsetTop + last.offsetHeight,
      gap: items.length > 1 ? items[1].offsetTop - (items[0].offsetTop + items[0].offsetHeight) : 0,
      pointer: event.pointerId,
      startY: event.clientY,
      originTop: fresh.offsetTop,
      active: false,
      before: savedKeyItems().map((item) => item.dataset.keyId).join("\n"),
    };
    try { keyList.setPointerCapture(event.pointerId); } catch (_) { /* ignore */ }
  }

  function onKeyDragMove(event) {
    const drag = keyDrag;
    if (!drag || event.pointerId !== drag.pointer) return;
    let dy = event.clientY - drag.startY;
    if (!drag.active) {
      if (Math.abs(dy) < 3) return;
      drag.active = true;
      drag.el.classList.add("is-dragging");
      keyList.classList.add("is-sorting");
    }
    const items = savedKeyItems();
    const height = drag.el.offsetHeight;
    // Stay within the saved keys; the environment key keeps the top.
    const top = Math.max(drag.minTop, Math.min(drag.maxBottom - height, drag.originTop + dy));
    dy = top - drag.originTop;

    const others = items.filter((item) => item !== drag.el);
    // Pick the slot whose top is closest to the dragged row's top. Slot tops
    // come from the other rows' heights, not their live DOM positions (which
    // move as the dragged row moves). Half a row in either direction swaps;
    // clamped to the top it lands first, clamped to the bottom it lands last.
    let slotTop = drag.minTop;
    let index = 0;
    let best = Math.abs(top - slotTop);
    others.forEach((item, i) => {
      slotTop += item.offsetHeight + drag.gap;
      const distance = Math.abs(top - slotTop);
      if (distance < best) { best = distance; index = i + 1; }
    });
    const target = others[index] || null;
    const next = target || (others.length ? others[others.length - 1].nextSibling : null);
    if (drag.el.nextSibling !== next && drag.el !== next) {
      const tops = new Map(others.map((item) => [item, item.offsetTop]));
      keyList.insertBefore(drag.el, next);
      slideFrom(tops);
    }
    drag.el.style.transform = `translateY(${drag.originTop + dy - drag.el.offsetTop}px)`;
  }

  async function onKeyDragEnd(event) {
    const drag = keyDrag;
    if (!drag || event.pointerId !== drag.pointer) return;
    keyDrag = null;
    try { keyList.releasePointerCapture(event.pointerId); } catch (_) { /* ignore */ }
    if (!drag.active) return;

    drag.el.style.transition = "transform 0.15s ease";
    drag.el.style.transform = "";
    keyList.classList.remove("is-sorting");
    setTimeout(() => {
      drag.el.classList.remove("is-dragging");
      drag.el.style.transition = "";
    }, 150);

    const order = savedKeyItems().map((item) => item.dataset.keyId);
    if (order.join("\n") === drag.before) return;
    // Keys waiting on an undo still exist in the backend; keep them listed.
    for (const id of hiddenKeys) if (!order.includes(id)) order.push(id);

    const position = new Map(order.map((id, index) => [id, index]));
    state.keys.sort((a, b) => {
      if (a.Source === "env" || b.Source === "env") return a.Source === "env" ? -1 : 1;
      return (position.get(a.ID) ?? 0) - (position.get(b.ID) ?? 0);
    });
    keyError.hidden = true;
    try {
      await call("SetKeyOrder", order);
    } catch (error) {
      showKeyError(errorText(error));
    }
    await reloadKeys();
  }

  // ── Toolbar key picker ────────────────────────────────────

  function pickerTier() {
    return state.mode === "batch" ? "paid" : "free";
  }

  function pickerKeys() {
    return visibleKeys().filter((key) => key.Tier === pickerTier());
  }

  // Shown only when the current tier has a real choice. A choice that no
  // longer fits the tier (mode switched, key retiered) goes back to automatic.
  function renderPicker() {
    const keys = pickerKeys();
    const show = !modeToggle.hidden && keys.length >= 2;
    const chosen = keys.find((key) => key.ID === state.newItemKey) || null;
    if (state.newItemKey && (!chosen || !show)) setNewItemKey("");
    keyPicker.hidden = !show;
    keyPickerLabel.textContent = chosen && show ? chosen.Label : t("picker.auto");
    if (!show && menuRow === keyPicker) closeMenu();
  }

  async function setNewItemKey(id) {
    const previous = state.newItemKey;
    state.newItemKey = id;
    if (id !== previous) renderPicker();
    try {
      await call("SetNewItemKey", id);
    } catch (error) {
      state.newItemKey = previous;
      renderPicker();
      showToast(errorText(error));
    }
  }

  function openPickerMenu() {
    if (menuRow === keyPicker) {
      closeMenu();
      return;
    }
    closeMenu();
    const entry = (id, label) => {
      const selected = state.newItemKey === id;
      return `<button type="button" role="menuitemradio" aria-checked="${selected}"
        data-pick="${esc(id)}"><span class="menu-check">${selected ? GLYPHS.check : ""}</span>${esc(label)}</button>`;
    };
    menu.innerHTML = entry("", t("picker.autoByPriority")) + "<hr>"
      + pickerKeys().map((key) => entry(key.ID, key.Label)).join("");
    menu.classList.add("has-checks");
    delete menu.dataset.id;
    openSurface(menu, { trap: false });
    setMenuRow(keyPicker);
    keyPicker.classList.add("is-menu");

    const anchor = keyPicker.getBoundingClientRect();
    const rect = menu.getBoundingClientRect();
    const x = Math.min(anchor.left - 6, window.innerWidth - rect.width - 6);
    menu.style.left = `${Math.max(6, x)}px`;
    menu.style.top = `${anchor.bottom + 4}px`;
  }


  // ── Sheets ────────────────────────────────────────────────

  function openKeys(tier) {
    closeMenu();
    if (tier) state.keyTier = tier;
    keyError.hidden = true;
    state.openKey = "";
    renderKeys();
    // With no key yet the field is the only thing to do; otherwise the sheet
    // itself takes focus and Tab walks it from the top.
    openSurface(keysSheet, { focus: state.keys.length === 0 ? keyValue : keysSheet });
  }

  function openHelp() {
    closeMenu();
    openSurface(helpSheet);
  }

  function closeSheets() {
    closeSurface(keysSheet);
    closeSurface(helpSheet);
  }

  // A modal surface (a sheet or the PDF card) is open.
  function sheetOpen() {
    return trapSurface() !== null;
  }

  // ── PDF choice ────────────────────────────────────────────

  // A folder with audio and two or more PDFs waits in Go until the user
  // picks the PDFs ("items:choosePDF"). Folders dropped together, or while
  // the card is open, queue up and are asked one after another.
  const pdfQueue = [];
  let pdfCurrent = null;

  function queuePDFChoice(choice) {
    if (!choice || !choice.Token) return;
    const known = (c) => c.Token === choice.Token;
    if ((pdfCurrent && known(pdfCurrent)) || pdfQueue.some(known)) return;
    pdfQueue.push(choice);
    if (!pdfCurrent) showNextPDFChoice();
  }

  function showNextPDFChoice() {
    pdfCurrent = pdfQueue.shift() || null;
    if (!pdfCurrent) {
      closeSurface(pdfSheet);
      return;
    }
    closeMenu();
    const files = pdfCurrent.PDFs || [];
    pdfSub.textContent = t("pdf.subline", { folder: pdfCurrent.Name, count: files.length });
    pdfList.textContent = "";
    for (const file of files) {
      const row = document.createElement("label");
      row.className = "pdf-row";
      const box = document.createElement("input");
      box.type = "checkbox";
      box.className = "check";
      box.checked = true;
      box.value = file.Path;
      const name = document.createElement("span");
      name.className = "pdf-name";
      name.textContent = file.Name;
      name.title = file.Name;
      row.append(box, name);
      if (file.Pages > 0) {
        const pages = document.createElement("span");
        pages.className = "pdf-pages";
        pages.textContent = t("pdf.pages", { count: file.Pages });
        row.append(pages);
      }
      pdfList.append(row);
    }
    // Focus the card, not the button: Enter still starts (see keydown) and
    // no focus ring shows until the keyboard moves it.
    openSurface(pdfSheet, { focus: pdfForm });
  }

  // answerPDF sends the choice for the folder on the card: a list of paths
  // (empty = no PDF), or null to not add the folder. The next queued folder
  // shows at once; the answer's result only matters when it fails.
  function answerPDF(selected) {
    const choice = pdfCurrent;
    if (!choice) return;
    const done = selected === null
      ? call("CancelPDFs", choice.Token)
      : call("ConfirmPDFs", choice.Token, selected);
    Promise.resolve(done).catch((error) => showToast(errorText(error)));
    showNextPDFChoice();
  }

  function checkedPDFs() {
    return Array.from(pdfList.querySelectorAll("input.check:checked"), (box) => box.value);
  }

  async function loadPendingPDFChoices() {
    try {
      const pending = await call("PendingPDFChoices");
      for (const choice of pending || []) queuePDFChoice(choice);
    } catch (_) {
      // Older backend or not ready: drops still raise the event.
    }
  }

  // ── Events ────────────────────────────────────────────────

  function pickFiles() {
    if (!hasKeys()) {
      explainMissingKey();
      return;
    }
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

  // Keyboard scopes, each called from the one keydown listener in bindEvents.

  // A sheet or the PDF card. The card is modal: Escape cancels this folder,
  // other shortcuts wait.
  function modalKeys(event) {
    if (!pdfSheet.hidden) {
      if (event.key === "Escape") {
        event.preventDefault();
        answerPDF(null);
      } else if (event.key === "Enter" && !event.isComposing && event.target === pdfForm) {
        event.preventDefault();
        answerPDF(checkedPDFs());
      }
      return;
    }
    if (event.key !== "Escape") return;
    if (!keysSheet.hidden && collapseKeyPanel()) return;
    closeSheets();
  }

  // The context menu or the key picker's menu, which take every key while
  // open, like a native menu.
  function menuKeys(event) {
    if (event.key === "Escape") {
      closeMenu();
    } else if (["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      moveMenuFocus(event.key);
    }
  }

  // The list itself has focus (not a field or button inside it). The
  // selected row is the active one, so moving it selects, as a click does.
  // Returns whether the key was the list's.
  function listKeys(event) {
    const items = visibleItems();
    const at = items.findIndex((item) => item.ID === state.selected);
    const rowEl = rowCache.get(state.selected)?.el;
    const command = event.metaKey || event.ctrlKey;
    const plain = !command && !event.altKey && !event.shiftKey;
    const key = event.key;
    if (plain && key === "ArrowDown") selectAt(items, at < 0 ? 0 : Math.min(at + 1, items.length - 1));
    else if (plain && key === "ArrowUp") selectAt(items, at < 0 ? items.length - 1 : Math.max(at - 1, 0));
    else if (plain && key === "Home") selectAt(items, 0);
    else if (plain && key === "End") selectAt(items, items.length - 1);
    else if (plain && key === "Enter" && at >= 0) openRow(state.selected);
    // Space shows the transcript; focus stays here (F6 moves to it).
    else if (plain && key === " ") {
      if (!state.pane) setPane(true);
    }
    // ⌘⌫ is the Mac delete, Delete the Windows one; both work on both.
    else if (at >= 0 && (key === "Delete" || (key === "Backspace" && command))) runAction("delete", state.selected);
    else if (rowEl && (key === "ContextMenu" || (key === "F10" && event.shiftKey))) {
      openMenu(rowMenuPoint(rowEl), items[at], rowEl);
    } else return false;
    event.preventDefault();
    return true;
  }

  // F6 and Shift+F6 cycle the list, the transcript (when the pane is open)
  // and the search field; from anywhere else F6 starts at the list.
  function cycleRegion(back) {
    const regions = [list, paneBody, search].filter(canFocus);
    if (!regions.length) return;
    const at = regions.findIndex((el) => el.contains(document.activeElement));
    const step = back ? regions.length - 1 : 1;
    regions[at < 0 ? 0 : (at + step) % regions.length].focus();
  }

  function globalKeys(event) {
    const command = event.metaKey || event.ctrlKey;
    if (event.key === "Escape") {
      if (swipe.offsets.size) closeOtherRows(null);
    } else if (command && event.key.toLowerCase() === "f") {
      event.preventDefault();
      search.focus();
      search.select();
    } else if (event.key === "F6") {
      event.preventDefault();
      cycleRegion(event.shiftKey);
    }
  }

  function bindEvents() {
    bindDragHighlight();

    addButton.addEventListener("click", pickFiles);
    empty.addEventListener("click", pickFiles);

    modeToggle.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-mode]");
      if (!button) return;
      if (button.getAttribute("aria-disabled") === "true") {
        explainMissingTier(button.dataset.mode);
        return;
      }
      setMode(button.dataset.mode);
    });
    keyPicker.addEventListener("click", openPickerMenu);

    // Search runs on every input, also while the Korean IME is composing:
    // the backend matches by jamo (internal/store/match.go), so a half-built
    // syllable such as 컴픁 (on the way to 컴퓨터) keeps its results.
    // compositionend searches once more in case the final text arrives
    // without another input event.
    let searchTimer = null;
    const runSearch = () => {
      clearTimeout(searchTimer);
      searchTimer = setTimeout(() => {
        const query = search.value.trim();
        if (query === state.query) return;
        state.query = query;
        refreshItems();
      }, 120);
    };
    search.addEventListener("input", runSearch);
    search.addEventListener("compositionend", runSearch);
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
      select(id);
    });

    list.addEventListener("dblclick", (event) => {
      const rowEl = event.target.closest(".row");
      if (!rowEl || event.target.closest("button, input")) return;
      openRow(rowEl.dataset.id);
    });

    paneHead.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-action]");
      if (button) runAction(button.dataset.action, button.dataset.id);
    });
    paneToggle.addEventListener("click", () => setPane(!state.pane));

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
      const pick = event.target.closest("button[data-pick]");
      if (pick) {
        closeMenu();
        setNewItemKey(pick.dataset.pick);
        return;
      }
      const button = event.target.closest("button[data-action]");
      if (!button) return;
      const id = menu.dataset.id;
      closeMenu();
      runAction(button.dataset.action, id);
    });

    document.addEventListener("pointerdown", (event) => {
      if (!menu.hidden && !menu.contains(event.target) && !keyPicker.contains(event.target)) closeMenu();
      if (swipe.offsets.size && !event.target.closest(".row")) closeOtherRows(null);
    }, true);
    list.addEventListener("scroll", closeMenu, { passive: true });
    list.addEventListener("scroll", syncScrolled, { passive: true });
    window.addEventListener("resize", syncScrolled);
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

    pdfForm.addEventListener("submit", (event) => {
      event.preventDefault();
      answerPDF(checkedPDFs());
    });
    pdfNone.addEventListener("click", () => answerPDF([]));
    pdfCancel.addEventListener("click", () => answerPDF(null));
    trackHover(pdfList, ".pdf-row");

    // The only document keydown listener. Each scope takes the keys while it
    // is the innermost one: a modal surface, then the menu, then the focused
    // list, then the window.
    document.addEventListener("keydown", (event) => {
      if (event.key === "Tab") trapTab(event);
      else if (sheetOpen()) modalKeys(event);
      else if (!menu.hidden) menuKeys(event);
      else if (event.target === list && listKeys(event)) return;
      else globalKeys(event);
    });

    keyTier.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-tier]");
      if (!button) return;
      state.keyTier = button.dataset.tier;
      renderKeys();
    });

    trackHover(keyList, ".key-row");
    trackHover(list, ".row");
    // Buttons outside the list get the same JS hover (see trackHover).
    trackHover(document.body, ".tool-button, .foot-button, .picker-button, .row-action, .plain-button, .text-button, .empty");

    keyList.addEventListener("click", (event) => {
      const del = event.target.closest("[data-key-delete]");
      if (del) {
        deleteKeyWithUndo(del.dataset.keyDelete);
        return;
      }
      const tier = event.target.closest("button[data-key-tier]");
      if (tier) {
        setKeyTier(tier.closest(".key-item").dataset.keyId, tier.dataset.keyTier);
        return;
      }
      if (event.target.closest(".key-handle")) return;
      const row = event.target.closest(".key-row");
      if (row) toggleKeyPanel(row.parentElement.dataset.keyId);
    });
    keyList.addEventListener("keydown", (event) => {
      const row = event.target.closest(".key-row");
      if (row && event.target === row && (event.key === "Enter" || event.key === " ")) {
        event.preventDefault();
        toggleKeyPanel(row.parentElement.dataset.keyId);
        return;
      }
      if (!event.target.classList.contains("key-name") || event.isComposing) return;
      if (event.key === "Enter") {
        event.preventDefault();
        event.target.blur(); // commits on blur
      } else if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        event.target.value = event.target.dataset.original || "";
        event.target.blur();
      }
    });
    keyList.addEventListener("focusout", (event) => {
      if (event.target.classList.contains("key-name")) commitKeyName(event.target);
    });
    keyList.addEventListener("pointerdown", onKeyDragStart);
    keyList.addEventListener("pointermove", onKeyDragMove);
    keyList.addEventListener("pointerup", onKeyDragEnd);
    keyList.addEventListener("pointercancel", onKeyDragEnd);

    keyForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      keyError.hidden = true;
      const value = keyValue.value.trim();
      if (!value) {
        keyValue.focus();
        return;
      }
      keySave.disabled = true;
      keySave.textContent = t("keys.checking");
      try {
        await call("AddKey", "", state.keyTier, value);
        keyValue.value = "";
        await loadKeys();
        showToast(t("toast.keySaved"));
      } catch (error) {
        showKeyError(errorText(error));
      } finally {
        keySave.disabled = false;
        keySave.textContent = t("keys.save");
      }
    });
  }

  async function init() {
    bindEvents();

    // The drop listener is registered at the very top of this file.
    if (window.runtime && typeof window.runtime.EventsOn === "function") {
      // Subscribe before the first list load, so a change that lands between
      // that response and a later subscription cannot be missed.
      // A folder with several PDFs waits for the PDF card.
      window.runtime.EventsOn("items:choosePDF", queuePDFChoice);
      window.runtime.EventsOn("items:changed", refreshItems);
      window.runtime.EventsOn("keys:changed", reloadKeys);
      // A drop the backend could not take, e.g. before any key exists.
      window.runtime.EventsOn("items:addFailed", (message, noKeys) => {
        if (noKeys) explainMissingKey();
        else showToast(errorText(message));
      });
      // The macOS menu bar (internal/app/menu.go) does what ⚙ and ? do. Its
      // shortcuts skip the keydown handler, so the PDF card's "other
      // shortcuts wait" rule is applied here too.
      window.runtime.EventsOn("menu:settings", () => {
        if (pdfSheet.hidden) openKeys();
      });
      window.runtime.EventsOn("menu:help", () => {
        if (pdfSheet.hidden) openHelp();
      });
    }

    // The window opened at the width for this choice (cmd/nalmeok).
    try {
      setPane(Boolean(await call("PaneOpen")), false);
    } catch (_) {
      setPane(false, false);
    }

    try {
      await loadKeys();
    } catch (error) {
      showToast(errorText(error));
    }

    try {
      await refreshItems();
    } catch (error) {
      showToast(errorText(error));
    }

    if (document.activeElement === search) search.blur();
    if (state.keys.length === 0) openKeys("free");

    await loadPendingPDFChoices();

    // Keep "진행 중 · N분" and "기다리는 중 · N분" current without a backend
    // round trip.
    setInterval(() => {
      if (state.items.some(isActive)) renderItems();
    }, 30000);
  }

  init();
})();
