(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);

  const appEl = $("app");
  const dropZone = $("drop-zone");
  const modeToggle = $("mode-toggle");
  const freeSegment = $("mode-free");
  const batchSegment = $("mode-batch");
  const emptyHint = $("empty-hint");
  const search = $("search");
  const itemList = $("item-list");
  const gear = $("gear");

  const sheet = $("sheet");
  const sheetClose = $("sheet-close");
  const sheetIntro = $("sheet-intro");
  const keyList = $("key-list");
  const keyForm = $("key-form");
  const keyLabel = $("key-label");
  const keyTier = $("key-tier");
  const keyValue = $("key-value");
  const keyError = $("key-error");
  const keySave = $("key-save");

  const toast = $("toast");

  const state = {
    items: [],
    keys: [],
    mode: "free",
    query: "",
  };

  const ICONS = {
    music: `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M9 18V5l10-2v13"/>
      <circle cx="6.5" cy="18" r="2.5"/>
      <circle cx="16.5" cy="16" r="2.5"/>
    </svg>`,
    folder: `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3 6.5A1.5 1.5 0 0 1 4.5 5h4l2 2.5h7A1.5 1.5 0 0 1 19 9v8a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 3 17V6.5Z"/>
    </svg>`,
    clock: `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="9"/>
      <path d="M12 7v5l3 2"/>
    </svg>`,
    check: `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="m5 12 5 5L20 7"/>
    </svg>`,
  };

  function backend() {
    return (window.go && window.go.app && window.go.app.App) || null;
  }

  function call(method, ...args) {
    const api = backend();
    if (!api || typeof api[method] !== "function") {
      return Promise.reject(new Error("앱이 아직 준비되지 않았습니다."));
    }
    return api[method](...args);
  }

  function errorText(error) {
    let text = "";
    if (typeof error === "string") {
      text = error;
    } else if (error && typeof error.message === "string") {
      text = error.message;
    } else if (error != null) {
      text = String(error);
    }
    text = text.replace(/^Error:\s*/, "").trim();
    return text || "알 수 없는 오류가 발생했습니다.";
  }

  function esc(value) {
    return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;",
    })[ch]);
  }

  let toastTimer = null;

  function showToast(message) {
    toast.textContent = message;
    toast.classList.add("is-visible");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove("is-visible"), 1900);
  }

  // ── Mode ──────────────────────────────────────────────────

  function renderMode() {
    const tiers = { free: false, paid: false };
    for (const key of state.keys) {
      if (key.Tier === "free" || key.Tier === "paid") tiers[key.Tier] = true;
    }
    const hasKeys = state.keys.length > 0;

    freeSegment.disabled = hasKeys && !tiers.free;
    batchSegment.disabled = hasKeys && !tiers.paid;

    for (const segment of [freeSegment, batchSegment]) {
      const active = segment.dataset.mode === state.mode;
      segment.classList.toggle("is-active", active);
      segment.setAttribute("aria-selected", String(active));
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

  async function syncModeAvailability() {
    if (state.keys.length === 0) return;
    const hasFree = state.keys.some((key) => key.Tier === "free");
    const hasPaid = state.keys.some((key) => key.Tier === "paid");
    if (!hasFree && hasPaid && state.mode !== "batch") {
      await setMode("batch");
    } else if (hasFree && !hasPaid && state.mode !== "free") {
      await setMode("free");
    }
  }

  // ── Items ─────────────────────────────────────────────────

  function elapsedMinutes(item) {
    const stamp = item.UpdatedAt || item.CreatedAt;
    const then = Date.parse(stamp);
    if (!Number.isFinite(then)) return 0;
    return Math.max(0, Math.floor((Date.now() - then) / 60000));
  }

  function statusHTML(item) {
    const id = esc(item.ID);

    switch (item.State) {
      case "running": {
        const percent = Math.max(0, Math.min(100, Math.round((Number(item.Progress) || 0) * 100)));
        return `
          <div class="status status-running">
            <div class="status-line">
              <span class="status-text">전사 중</span>
              <span class="status-percent">${percent}%</span>
            </div>
            <div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax="100"
                 aria-valuenow="${percent}">
              <span class="progress-fill" style="width:${percent}%"></span>
            </div>
          </div>`;
      }

      case "batch_waiting": {
        const minutes = elapsedMinutes(item);
        return `
          <div class="status status-batch">
            <span class="status-line">
              <span class="status-text">${ICONS.clock}Batch 대기 · ${minutes}분 경과</span>
            </span>
          </div>`;
      }

      case "free_exhausted":
        return `
          <div class="status status-exhausted">
            <span class="status-text">무료 한도 소진</span>
          </div>
          <button class="batch-button" type="button" data-action="batch" data-id="${id}">
            남은 분량 유료 Batch로
          </button>`;

      case "done":
        return `
          <div class="status status-done">
            <span class="status-text">${ICONS.check}완료</span>
          </div>
          <div class="status-actions">
            <button class="link-button" type="button" data-action="open" data-id="${id}">열기</button>
            <button class="link-button" type="button" data-action="copy" data-id="${id}">복사</button>
            <button class="link-button" type="button" data-action="reveal" data-id="${id}">폴더</button>
          </div>`;

      case "failed": {
        const detail = item.Detail ? ` · ${esc(item.Detail)}` : "";
        return `
          <div class="status status-failed">
            <span class="status-text">실패${detail}</span>
          </div>
          <div class="status-actions">
            <button class="link-button" type="button" data-action="retry" data-id="${id}">다시 시도</button>
          </div>`;
      }

      case "canceled":
        return `
          <div class="status status-canceled">
            <span class="status-text">취소됨</span>
          </div>`;

      default:
        return `
          <div class="status status-canceled">
            <span class="status-text">${esc(item.Detail || item.State || "대기 중")}</span>
          </div>`;
    }
  }

  function sideHTML(item) {
    const id = esc(item.ID);
    if (item.State === "running" || item.State === "batch_waiting") {
      return `<button class="cancel-button" type="button" data-action="cancel" data-id="${id}">취소</button>`;
    }
    return `<button class="icon-button" type="button" data-action="remove" data-id="${id}"
      aria-label="목록에서 지우기" title="목록에서 지우기">✕</button>`;
  }

  function cardHTML(item) {
    const icon = item.Kind === "folder" ? ICONS.folder : ICONS.music;
    return `
      <article class="item-card" data-id="${esc(item.ID)}">
        <div class="item-icon" aria-hidden="true">${icon}</div>
        <div class="item-body">
          <div class="item-name" title="${esc(item.Source || item.Name)}">${esc(item.Name)}</div>
          ${statusHTML(item)}
        </div>
        <div class="item-side">${sideHTML(item)}</div>
      </article>`;
  }

  function renderItems() {
    if (state.items.length === 0) {
      itemList.innerHTML = state.query ? `<p class="list-empty">검색 결과가 없습니다</p>` : "";
      return;
    }
    const scrollTop = itemList.scrollTop;
    itemList.innerHTML = state.items.map(cardHTML).join("");
    itemList.scrollTop = scrollTop;
  }

  function renderEmpty() {
    const empty = state.items.length === 0 && !state.query;
    appEl.classList.toggle("is-empty", empty);
    emptyHint.hidden = !empty;
  }

  let listRequest = 0;

  async function loadItems() {
    const token = ++listRequest;
    const items = await call("ListItems", state.query);
    if (token !== listRequest) return; // a newer search won
    state.items = Array.isArray(items) ? items : [];
    renderItems();
    renderEmpty();
  }

  async function refreshItems() {
    try {
      await loadItems();
    } catch (error) {
      showToast(errorText(error));
    }
  }

  // ── Keys ──────────────────────────────────────────────────

  function keyHTML(key) {
    const tierLabel = key.Tier === "free" ? "무료" : "유료";
    const isEnv = key.Source === "env";
    return `
      <div class="key-row">
        <div class="key-info">
          <span class="key-label" title="${esc(key.Label)}">${esc(key.Label)}</span>
          <span class="tier-badge" data-tier="${esc(key.Tier)}">${tierLabel}</span>
          ${isEnv ? `<span class="env-badge">환경변수 · 읽기 전용</span>` : ""}
        </div>
        <span class="key-meta">${esc(key.Masked)}</span>
        ${isEnv ? "" : `
          <button class="icon-button" type="button" data-key-delete="${esc(key.ID)}"
            aria-label="키 삭제" title="키 삭제">✕</button>`}
      </div>`;
  }

  function renderKeys() {
    keyList.innerHTML = state.keys.length
      ? state.keys.map(keyHTML).join("")
      : `<p class="key-empty">등록된 키가 없습니다.</p>`;
    sheetIntro.hidden = state.keys.length > 0;
  }

  async function loadKeys() {
    const keys = await call("Keys");
    state.keys = Array.isArray(keys) ? keys : [];
    renderKeys();
    await syncModeAvailability();
    renderMode();
  }

  function showKeyError(message) {
    keyError.textContent = message;
    keyError.hidden = false;
  }

  // ── Sheet ─────────────────────────────────────────────────

  function openSheet() {
    sheet.hidden = false;
    sheet.setAttribute("aria-hidden", "false");
    renderKeys();
    keyError.hidden = true;
    keyLabel.focus();
  }

  function closeSheet() {
    sheet.hidden = true;
    sheet.setAttribute("aria-hidden", "true");
  }

  // ── Events ────────────────────────────────────────────────

  function bindEvents() {
    dropZone.addEventListener("click", () => {
      call("PickFiles").catch((error) => showToast(errorText(error)));
    });

    for (const type of ["dragenter", "dragover"]) {
      dropZone.addEventListener(type, (event) => {
        event.preventDefault();
        dropZone.classList.add("is-over");
      });
    }
    for (const type of ["dragleave", "drop"]) {
      dropZone.addEventListener(type, (event) => {
        event.preventDefault();
        if (type === "dragleave" && dropZone.contains(event.relatedTarget)) return;
        dropZone.classList.remove("is-over");
      });
    }
    window.addEventListener("dragover", (event) => event.preventDefault());
    window.addEventListener("drop", (event) => event.preventDefault());

    modeToggle.addEventListener("click", (event) => {
      const segment = event.target.closest(".segment");
      if (!segment || segment.disabled) return;
      setMode(segment.dataset.mode);
    });

    let searchTimer = null;
    search.addEventListener("input", () => {
      clearTimeout(searchTimer);
      searchTimer = setTimeout(() => {
        state.query = search.value.trim();
        refreshItems();
      }, 150);
    });

    itemList.addEventListener("click", async (event) => {
      const button = event.target.closest("[data-action]");
      if (!button) return;
      const id = button.dataset.id;
      const action = button.dataset.action;

      button.disabled = true;
      try {
        switch (action) {
          case "open":
            await call("OpenOutput", id);
            break;
          case "reveal":
            await call("RevealOutput", id);
            break;
          case "copy":
            await call("CopyOutput", id);
            showToast("복사했습니다");
            break;
          case "retry":
            await call("Retry", id);
            await refreshItems();
            break;
          case "batch":
            await call("SendToBatch", id);
            await refreshItems();
            break;
          case "cancel":
            await call("Cancel", id);
            await refreshItems();
            break;
          case "remove":
            await call("Remove", id);
            await refreshItems();
            break;
          default:
            break;
        }
      } catch (error) {
        showToast(errorText(error));
      } finally {
        button.disabled = false;
      }
    });

    gear.addEventListener("click", openSheet);
    sheetClose.addEventListener("click", closeSheet);
    sheet.addEventListener("click", (event) => {
      if (event.target === sheet) closeSheet();
    });
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && !sheet.hidden) closeSheet();
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

      const label = keyLabel.value.trim();
      const tier = keyTier.value;
      const value = keyValue.value.trim();
      if (!value) {
        showKeyError("API 키를 입력해 주세요.");
        keyValue.focus();
        return;
      }

      keySave.disabled = true;
      keySave.textContent = "확인 중…";
      try {
        await call("AddKey", label, tier, value);
        keyValue.value = "";
        keyLabel.value = "";
        await loadKeys();
        showToast("키를 저장했습니다");
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
    } catch (error) {
      // Stay on the default mode; the first drop will report the real error.
    }

    try {
      await loadKeys();
    } catch (error) {
      showToast(errorText(error));
    }

    try {
      await loadItems();
    } catch (error) {
      showToast(errorText(error));
    }

    if (state.keys.length === 0) openSheet();

    if (window.runtime && typeof window.runtime.EventsOn === "function") {
      window.runtime.EventsOn("items:changed", refreshItems);
    }

    // Keep "Batch 대기 · N분 경과" current without a backend round trip.
    setInterval(() => {
      if (state.items.some((item) => item.State === "batch_waiting")) renderItems();
    }, 30000);
  }

  init();
})();
