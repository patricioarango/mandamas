import { esc, icons, avatarHTML, logoHTML, greeting, firstName, categoryInfo } from "../ui.js";

// Contour-line pattern for the card background (like card.png).
export const waves = (() => {
  let rings = "";
  for (let i = 0; i < 16; i++) {
    const rx = 30 + i * 13, ry = 22 + i * 10;
    rings += `<ellipse cx="190" cy="70" rx="${rx}" ry="${ry}" transform="rotate(${-18 + i * 1.6} 190 70)" />`;
  }
  return `<svg viewBox="0 0 300 220" preserveAspectRatio="xMidYMid slice" fill="none" stroke="white" stroke-width="1">${rings}</svg>`;
})();

export function cardHTML(p) {
  return `
    <a class="card" href="#/p/${esc(p.key)}" style="--c:${esc(p.color)}" data-key="${esc(p.key)}">
      <div class="card-waves">${waves}</div>
      <span class="card-badge pill" hidden><span class="ping"></span><span class="badge-text"></span></span>
      <div class="card-head">
        <h2 class="card-title">${esc(p.label)}</h2>
        <span class="card-chip pill" title="Issues finished"><span class="dot">${icons.check}</span><span class="done-count">–</span></span>
      </div>
      <div class="card-body">
        <div class="card-cats">${p.categories.slice(0, 3).map((c) => `<span class="pill">${esc(categoryInfo(c).name)}</span>`).join("")}</div>
        <p class="card-desc">${esc(p.description)}</p>
      </div>
      <div class="card-logo">${logoHTML(p)}</div>
    </a>`;
}

export async function render(root, { api, user, isCurrent }) {
  root.innerHTML = `
    <div class="screen wide">
      <header class="home-top">
        ${avatarHTML(user)}
        <div class="hello"><small>${greeting()}</small><strong>${esc(firstName(user))}</strong></div>
        <button class="circle-btn light" id="logout" aria-label="Sign out">${icons.logout}</button>
      </header>
      <h1 class="home-title">Your projects</h1>
      <p class="home-sub">Pick one and tell Claude what to do.</p>
      <div class="cards" id="cards"></div>
    </div>`;
  root.querySelector("#logout").addEventListener("click", () => api.signOut());

  const projects = await api.listProjects();
  if (!isCurrent()) return;
  const list = root.querySelector("#cards");
  const addCard = `<a class="card add-card" href="#/new-project"><span class="add-plus">${icons.plus}</span><span><strong>New project</strong><small>Connect a repo's Claude routine</small></span></a>`;
  list.innerHTML = projects.map(cardHTML).join("") + addCard;

  // Counts load per card so a slow one doesn't hold the page.
  for (const p of projects) {
    api.projectStats(p.key).then((s) => {
      const card = list.querySelector(`[data-key="${CSS.escape(p.key)}"]`);
      if (!card) return;
      card.querySelector(".done-count").textContent = `${s.done} done`;
      if (s.needsInput > 0) {
        const b = card.querySelector(".card-badge");
        b.querySelector(".badge-text").textContent = s.needsInput === 1 ? "Claude needs you" : `${s.needsInput} need you`;
        b.hidden = false;
      }
    }).catch(console.error);
  }
}
