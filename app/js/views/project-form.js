import { esc, icons, categoryInfo, KNOWN_CATEGORIES } from "../ui.js";
import { cardHTML } from "./home.js";

const SWATCHES = ["#3b6ff5", "#e8590c", "#0f9f6e", "#d6336c", "#7048e8", "#f59f00", "#1098ad", "#495057"];
// Same patterns as firestore.rules.
const KEY_RE = /^[a-z0-9][a-z0-9-]{1,39}$/;
const URL_RE = /^https:\/\/api\.anthropic\.com\/v1\/claude_code\/routines\/[^/]+\/fire$/;
const TOKEN_RE = /^sk-ant-oat01-[A-Za-z0-9_-]+$/;
const MAX_LOGO = 200_000;

const slugify = (s) =>
  s.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40);

/** Resize an image file to fit 256×256 and return a small data URL (Firestore doc limit is 1 MB). */
async function logoFromFile(file) {
  const bmp = await createImageBitmap(file);
  const scale = Math.min(1, 256 / Math.max(bmp.width, bmp.height));
  const c = document.createElement("canvas");
  c.width = Math.round(bmp.width * scale);
  c.height = Math.round(bmp.height * scale);
  c.getContext("2d").drawImage(bmp, 0, 0, c.width, c.height);
  for (const q of [0.85, 0.7, 0.5]) {
    const url = c.toDataURL("image/webp", q);
    if (url.length <= MAX_LOGO) return url;
  }
  throw new Error("That image is too detailed. Try a simpler one.");
}

export async function render(root, { api, go, toast, isCurrent }, editKey) {
  const editing = Boolean(editKey);
  const existing = editing ? await api.getProject(editKey) : null;
  if (!isCurrent()) return;
  if (editing && !existing) return go("/");

  const p = existing
    ? { ...existing, categories: [...existing.categories] }
    : { key: "", label: "", description: "", color: SWATCHES[0], logo: "", categories: ["bug", "feature"] };
  let keyTouched = editing;
  const allCats = [...new Set([...KNOWN_CATEGORIES, ...p.categories])];

  root.innerHTML = `
    <div class="editor pform">
      <header class="ed-head" id="head">
        <div class="ed-bar">
          <button class="circle-btn" id="back" aria-label="Back">${icons.back}</button>
          <div class="who"><div><small>${editing ? "Edit project" : "New project"}</small><strong id="head-title"></strong></div></div>
          <button class="circle-btn send" id="save" aria-label="Save project">${icons.check}</button>
        </div>
        <div class="form-preview" id="preview" aria-hidden="true"></div>
      </header>
      <section class="sheet">
        <form class="sheet-inner form" id="form" novalidate>
          <label class="field"><span>Title</span>
            <input id="label" maxlength="60" placeholder="Billing API" value="${esc(p.label)}" required></label>
          <label class="field"><span>Key <small>used in task IDs and URLs${editing ? ", can't change" : ""}</small></span>
            <input id="key" maxlength="40" placeholder="billing-api" value="${esc(p.key)}" ${editing ? "disabled" : ""} autocapitalize="off" spellcheck="false"></label>
          <label class="field"><span>Description</span>
            <textarea id="desc" rows="2" maxlength="300" placeholder="What this repo is about">${esc(p.description)}</textarea></label>

          <div class="field"><span>Color</span>
            <div class="swatches" id="swatches">
              ${SWATCHES.map((c) => `<button type="button" class="swatch" data-c="${c}" style="--s:${c}" aria-label="Color ${c}"></button>`).join("")}
              <label class="swatch custom" aria-label="Custom color"><input type="color" id="color" value="${esc(p.color)}"></label>
            </div>
          </div>

          <div class="field"><span>Logo <small>PNG, JPG, SVG or WebP. Resized to 256px</small></span>
            <div class="logo-pick">
              <div class="logo-box" id="logo-box"></div>
              <label class="chip-btn">Choose image<input type="file" id="logo" accept="image/*" hidden></label>
              <button type="button" class="chip-btn ghost" id="logo-rm">Remove</button>
            </div>
          </div>

          <div class="field"><span>Categories <small>what the <kbd>/</kbd> menu offers</small></span>
            <div class="cat-toggles" id="cats"></div>
            <div class="add-cat"><input id="new-cat" maxlength="20" placeholder="Add your own, e.g. infra" autocapitalize="off"><button type="button" class="chip-btn" id="add-cat">Add</button></div>
          </div>

          <fieldset class="secret">
            <legend>Claude routine</legend>
            <p class="hint">Create a routine for this repo at <a href="https://claude.ai/code/routines" target="_blank" rel="noopener">claude.ai/code/routines</a>, add an API trigger and paste its URL and token. They're saved where the app can write but never read them back.</p>
            <label class="field"><span>Trigger URL</span>
              <input id="url" inputmode="url" autocapitalize="off" spellcheck="false" placeholder="${editing ? "Saved. Leave blank to keep" : "https://api.anthropic.com/v1/claude_code/routines/…/fire"}"></label>
            <label class="field"><span>Token</span>
              <span class="pw"><input id="token" type="password" autocomplete="off" spellcheck="false" placeholder="${editing ? "Saved. Leave blank to keep" : "sk-ant-oat01-…"}"><button type="button" id="peek" class="chip-btn ghost">Show</button></span></label>
          </fieldset>

          <p class="form-error" id="err" role="alert" hidden></p>
          ${editing ? `<button type="button" class="text-btn danger" id="archive">Archive project</button>` : ""}
        </form>
      </section>
    </div>`;

  const $ = (s) => root.querySelector(s);

  function drawPreview() {
    const shown = { ...p, key: p.key || "preview", label: p.label || "Project title", description: p.description || "A short description shows here." };
    $("#preview").innerHTML = cardHTML(shown);
    $("#preview .done-count").textContent = "0 done";
    $("#head").style.setProperty("--c", p.color);
    $("#head-title").textContent = p.label || (editing ? p.key : "Untitled");
    root.querySelectorAll(".swatch[data-c]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.c === p.color)));
    $(".swatch.custom").style.setProperty("--s", p.color);
    $("#logo-box").innerHTML = p.logo ? `<img src="${esc(p.logo)}" alt="">` : `<span class="monogram" style="background:${esc(p.color)}">${esc((p.label || "?").charAt(0).toUpperCase())}</span>`;
    $("#logo-rm").hidden = !p.logo;
  }

  function drawCats() {
    $("#cats").innerHTML = allCats.map((k) => {
      const c = categoryInfo(k);
      return `<button type="button" class="cat-toggle" data-k="${esc(k)}" aria-pressed="${p.categories.includes(k)}">${c.icon}${esc(c.name)}</button>`;
    }).join("");
  }

  $("#label").addEventListener("input", (e) => {
    p.label = e.target.value;
    if (!keyTouched) { p.key = slugify(p.label); $("#key").value = p.key; }
    drawPreview();
  });
  $("#key").addEventListener("input", (e) => { keyTouched = true; p.key = e.target.value.trim(); });
  $("#desc").addEventListener("input", (e) => { p.description = e.target.value; drawPreview(); });
  $("#swatches").addEventListener("click", (e) => {
    const b = e.target.closest(".swatch[data-c]");
    if (b) { p.color = b.dataset.c; $("#color").value = p.color; drawPreview(); }
  });
  $("#color").addEventListener("input", (e) => { p.color = e.target.value; drawPreview(); });
  $("#logo").addEventListener("change", async (e) => {
    const f = e.target.files[0];
    if (!f) return;
    try { p.logo = await logoFromFile(f); drawPreview(); } catch (err) { toast(err.message || "Couldn't read that image."); }
    e.target.value = "";
  });
  $("#logo-rm").addEventListener("click", () => { p.logo = ""; drawPreview(); });
  $("#cats").addEventListener("click", (e) => {
    const b = e.target.closest(".cat-toggle");
    if (!b) return;
    const k = b.dataset.k;
    p.categories = p.categories.includes(k) ? p.categories.filter((x) => x !== k) : [...p.categories, k];
    drawCats();
  });
  const addCat = () => {
    const k = slugify($("#new-cat").value).slice(0, 20);
    if (!k) return;
    if (!allCats.includes(k)) allCats.push(k);
    if (!p.categories.includes(k)) p.categories.push(k);
    $("#new-cat").value = "";
    drawCats();
  };
  $("#add-cat").addEventListener("click", addCat);
  $("#new-cat").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); addCat(); } });
  $("#peek").addEventListener("click", (e) => {
    const t = $("#token");
    t.type = t.type === "password" ? "text" : "password";
    e.currentTarget.textContent = t.type === "password" ? "Show" : "Hide";
  });
  $("#back").addEventListener("click", () => go(editing ? `/p/${editKey}` : "/"));

  function validate() {
    const url = $("#url").value.trim(), token = $("#token").value.trim();
    if (!p.label.trim()) return "Give the project a title.";
    if (!KEY_RE.test(p.key)) return "Key: 2–40 lowercase letters, numbers or dashes.";
    if (!/^#[0-9a-fA-F]{6}$/.test(p.color)) return "Pick a color.";
    if (!p.categories.length) return "Pick at least one category.";
    if (p.categories.length > 12) return "12 categories at most.";
    const secretsGiven = url || token;
    if (!editing || secretsGiven) {
      if (!URL_RE.test(url)) return "The trigger URL should look like https://api.anthropic.com/v1/claude_code/routines/…/fire";
      if (!TOKEN_RE.test(token)) return "The token should start with sk-ant-oat01-";
    }
    return null;
  }

  async function save() {
    const err = validate();
    $("#err").hidden = !err;
    $("#err").textContent = err || "";
    if (err) return;
    const url = $("#url").value.trim(), token = $("#token").value.trim();
    const secrets = url || token ? { routineUrl: url, routineToken: token } : null;
    const data = { ...p, label: p.label.trim(), description: p.description.trim() };
    $("#save").disabled = true;
    try {
      if (editing) await api.updateProject(editKey, data, secrets);
      else await api.createProject(data, secrets);
      $("#token").value = "";
      toast(editing ? "Project saved." : "Project created.");
      go(`/p/${data.key}`);
    } catch (e) {
      console.error(e);
      $("#err").textContent = e.message || "Couldn't save the project.";
      $("#err").hidden = false;
      $("#save").disabled = false;
    }
  }
  $("#save").addEventListener("click", save);
  $("#form").addEventListener("submit", (e) => { e.preventDefault(); save(); });

  $("#archive")?.addEventListener("click", async () => {
    if (!confirm(`Archive ${p.label}? It disappears from home and no new tasks can be sent to it.`)) return;
    try { await api.archiveProject(editKey); toast("Project archived."); go("/"); }
    catch (e) { console.error(e); toast("Couldn't archive it."); }
  });

  drawPreview();
  drawCats();
  if (!editing) requestAnimationFrame(() => $("#label").focus());
}
