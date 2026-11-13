import { esc, icons, logoHTML, avatarHTML, greeting, firstName, categoryInfo } from "../ui.js";

const MAX_TEXT = 60_000; // routine text limit is 65,536 incl. the header the dispatcher adds

// Pixel position of the caret inside a textarea, via an off-screen mirror element.
function caretTop(ta, pos) {
  const cs = getComputedStyle(ta);
  const m = document.createElement("div");
  for (const p of ["fontFamily", "fontSize", "fontWeight", "lineHeight", "letterSpacing", "padding", "border", "boxSizing", "whiteSpace", "wordWrap"]) m.style[p] = cs[p];
  Object.assign(m.style, { position: "absolute", visibility: "hidden", top: "0", left: "-9999px", width: ta.clientWidth + "px", whiteSpace: "pre-wrap", wordWrap: "break-word" });
  m.textContent = ta.value.slice(0, pos);
  const mark = document.createElement("span");
  mark.textContent = "​";
  m.appendChild(mark);
  document.body.appendChild(m);
  const top = mark.offsetTop + parseFloat(cs.lineHeight);
  m.remove();
  return top;
}

const autosize = (ta) => { ta.style.height = "auto"; ta.style.height = ta.scrollHeight + "px"; };

export async function render(root, { api, user, go, toast, isCurrent }, key) {
  const p = await api.getProject(key);
  if (!isCurrent()) return;
  if (!p) return go("/");

  const cats = p.categories.map(categoryInfo);
  const draftKey = `mandamas:draft:${key}`;
  let draft = {};
  try { draft = JSON.parse(localStorage.getItem(draftKey) || "{}"); } catch {}
  let category = cats.some((c) => c.key === draft.category) ? draft.category : null;

  root.innerHTML = `
    <div class="editor">
      <header class="ed-head" style="--c:${esc(p.color)}">
        ${logoHTML(p, "art")}
        <div class="ed-bar">
          <div class="who">
            ${avatarHTML(user)}
            <div><small>${greeting()} ${esc(firstName(user))}</small><strong>New issue · ${esc(p.label)}</strong></div>
          </div>
          <button class="circle-btn" id="back" aria-label="Back">${icons.back}</button>
          <button class="circle-btn send" id="send" aria-label="Send to Claude" disabled>${icons.arrowUp}</button>
        </div>
        <button class="ed-hint" id="hint">${icons.sparkle}<span>Type <kbd>/</kbd> to pick a category</span></button>
      </header>
      <section class="sheet">
        <div class="sheet-inner" id="inner">
          <div id="chip"></div>
          <textarea class="ed-field ed-title" id="title" rows="1" placeholder="/ category, then the title" aria-label="Title" maxlength="300"></textarea>
          <textarea class="ed-field ed-body" id="body" rows="6" placeholder="Describe it. Steps to reproduce, what you expect, links…" aria-label="Description"></textarea>
          <div class="ed-meta" id="meta"></div>
          <div class="slash-menu" id="menu" role="listbox" aria-label="Categories" hidden></div>
        </div>
      </section>
    </div>`;

  const $ = (s) => root.querySelector(s);
  const title = $("#title"), body = $("#body"), menu = $("#menu"), send = $("#send"), chip = $("#chip");
  title.value = draft.title || "";
  body.value = draft.body || "";

  const save = () => {
    try { localStorage.setItem(draftKey, JSON.stringify({ category, title: title.value, body: body.value })); } catch {}
  };

  const refresh = () => {
    if (category) {
      const c = categoryInfo(category);
      chip.innerHTML = `<span class="cat-chip">${c.icon}${esc(c.name)}<button id="clear" aria-label="Remove category">${icons.close}</button></span>`;
      chip.querySelector("#clear").addEventListener("click", () => { category = null; refresh(); save(); title.focus(); });
    } else chip.innerHTML = "";
    const len = title.value.length + body.value.length;
    $("#meta").textContent = len > MAX_TEXT * 0.8 ? `${len.toLocaleString()} / ${MAX_TEXT.toLocaleString()} characters` : "";
    send.disabled = !(category && title.value.replace(/^\/\S*/, "").trim() && len <= MAX_TEXT);
    autosize(title); autosize(body);
  };

  // ---------- "/" menu ----------
  let slash = null; // { field, start, end, query }
  let active = 0;

  const matches = () => cats.filter((c) => !slash.query || c.key.startsWith(slash.query) || c.name.toLowerCase().startsWith(slash.query));

  function drawMenu() {
    const list = matches();
    active = Math.min(active, Math.max(list.length - 1, 0));
    menu.innerHTML = `<div class="group">Categories</div>` + (list.length
      ? list.map((c, i) => `
        <button class="slash-item" role="option" data-key="${esc(c.key)}" aria-selected="${i === active}">
          <span class="ico">${c.icon}</span>
          <span><strong>${esc(c.name)}</strong><small>${esc(c.hint)}</small></span>
        </button>`).join("")
      : `<div class="slash-empty">No category “${esc(slash.query)}”</div>`);
    menu.style.top = slash.field.offsetTop + caretTop(slash.field, slash.end) + 6 + "px";
    menu.hidden = false;
    menu.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  }

  const closeMenu = () => { slash = null; menu.hidden = true; };

  function detect(field) {
    const caret = field.selectionStart;
    if (caret !== field.selectionEnd) return closeMenu();
    const before = field.value.slice(0, caret);
    const start = before.lastIndexOf("\n") + 1;
    const m = before.slice(start).match(/^\/([\w-]*)$/);
    if (!m) return closeMenu();
    const fresh = !slash;
    slash = { field, start, end: caret, query: m[1].toLowerCase() };
    if (fresh) active = 0;
    drawMenu();
  }

  function pick(catKey) {
    const { field, start, end } = slash;
    field.value = field.value.slice(0, start) + field.value.slice(end);
    field.setSelectionRange(start, start);
    category = catKey;
    closeMenu();
    refresh();
    save();
    field.focus();
  }

  for (const field of [title, body]) {
    field.addEventListener("input", () => { detect(field); refresh(); save(); });
    field.addEventListener("click", () => detect(field));
    field.addEventListener("keydown", (e) => {
      if (slash && !menu.hidden) {
        const list = matches();
        if (e.key === "ArrowDown") { e.preventDefault(); active = (active + 1) % Math.max(list.length, 1); drawMenu(); return; }
        if (e.key === "ArrowUp") { e.preventDefault(); active = (active - 1 + list.length) % Math.max(list.length, 1); drawMenu(); return; }
        if ((e.key === "Enter" || e.key === "Tab") && list[active]) { e.preventDefault(); pick(list[active].key); return; }
        if (e.key === "Escape") { e.preventDefault(); closeMenu(); return; }
      }
      if (field === title && e.key === "Enter") { e.preventDefault(); body.focus(); body.setSelectionRange(0, 0); }
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && !send.disabled) { e.preventDefault(); submit(); }
    });
  }
  menu.addEventListener("mousedown", (e) => e.preventDefault()); // keep focus in the field
  menu.addEventListener("click", (e) => {
    const b = e.target.closest(".slash-item");
    if (b) pick(b.dataset.key);
  });
  const outside = (e) => { if (slash && !menu.contains(e.target) && e.target !== slash.field) closeMenu(); };
  document.addEventListener("pointerdown", outside);

  $("#hint").addEventListener("click", () => {
    if (!title.value.startsWith("/")) title.value = "/" + title.value;
    title.focus();
    title.setSelectionRange(1, 1);
    detect(title);
  });

  // ---------- send ----------
  async function submit() {
    if (send.disabled) return;
    send.disabled = true;
    try {
      await api.createTask({ project: key, category, title: title.value.trim(), description: body.value.trim() });
      try { localStorage.removeItem(draftKey); } catch {}
      toast("Sent to Claude.");
      go(`/p/${key}/issues`);
    } catch (err) {
      console.error(err);
      toast("Couldn't send it. Your draft is kept.");
      refresh();
    }
  }
  send.addEventListener("click", submit);
  $("#back").addEventListener("click", () => go(`/p/${key}`));

  refresh();
  requestAnimationFrame(() => {
    const f = title.value ? body : title;
    f.focus();
    // Open the menu straight away on a fresh doc, so the first thing you do is pick a category.
    if (!category && !title.value) $("#hint").click();
  });

  return () => document.removeEventListener("pointerdown", outside);
}
