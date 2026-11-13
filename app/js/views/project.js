import { esc, icons, logoHTML } from "../ui.js";

export async function render(root, { api, go, isCurrent }, key) {
  const p = await api.getProject(key);
  if (!isCurrent()) return;
  if (!p) return go("/");

  root.innerHTML = `
    <div class="hero-wrap">
      <section class="hero" style="--c:${esc(p.color)}">
        <div class="hero-top">
          <button class="circle-btn" id="back" aria-label="Back to projects">${icons.back}</button>
          <button class="circle-btn" id="edit" aria-label="Edit project">${icons.edit}</button>
        </div>
        <div class="hero-art">${logoHTML(p)}</div>
        <div class="hero-text">
          <h1 class="hero-title">${esc(p.label)}</h1>
          ${p.description ? `<p class="hero-desc">${esc(p.description)}</p>` : ""}
          <div class="hero-stats"><span class="pill" id="stats" hidden></span></div>
        </div>
        <div class="hero-actions">
          <button class="big-pill" id="list">List issues</button>
          <button class="circle-btn" id="new" aria-label="New issue">${icons.arrowRight}</button>
        </div>
      </section>
    </div>`;

  root.querySelector("#back").addEventListener("click", () => go("/"));
  root.querySelector("#edit").addEventListener("click", () => go(`/p/${key}/edit`));
  root.querySelector("#list").addEventListener("click", () => go(`/p/${key}/issues`));
  root.querySelector("#new").addEventListener("click", () => go(`/p/${key}/new`));

  api.projectStats(key).then((s) => {
    if (!isCurrent()) return;
    const el = root.querySelector("#stats");
    el.textContent = `${s.done} done` + (s.needsInput ? ` · ${s.needsInput} waiting for you` : "");
    el.hidden = false;
  }).catch(console.error);
}
