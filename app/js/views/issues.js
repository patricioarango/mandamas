import { esc, icons, categoryInfo, STATUS_LABEL, timeAgo } from "../ui.js";

const FILTERS = [
  ["all", "All", () => true],
  ["needs", "Needs you", (t) => t.status === "needs_input"],
  ["open", "In progress", (t) => ["queued", "dispatching", "dispatched", "pr_open"].includes(t.status)],
  ["done", "Done", (t) => t.status === "done"],
  ["failed", "Failed", (t) => t.status === "failed"],
];

const safeUrl = (u) => (/^https:\/\//.test(u || "") ? u : "");

function issueHTML(t) {
  const ask = t.status === "needs_input";
  const q = t.questions?.at(-1);
  const pr = safeUrl(t.prUrl), session = safeUrl(t.sessionUrl);
  return `
    <article class="issue${ask ? " ask" : ""}" data-id="${esc(t.id)}">
      <div class="issue-top">
        <span class="pill cat">${esc(categoryInfo(t.category).name)}</span>
        <span class="pill status s-${esc(t.status)}">${esc(STATUS_LABEL[t.status] || t.status)}</span>
        <span class="when">${esc(timeAgo(t.createdAt))}</span>
      </div>
      <h3>${esc(t.title)}</h3>
      ${t.description ? `<p class="desc">${esc(t.description)}</p>` : ""}
      ${t.status === "failed" && t.error ? `<div class="error">${esc(t.error)}</div>` : ""}
      ${ask ? `
        <div class="question"><span class="claude">${icons.claude}</span><div class="bubble">${esc(q?.question || "Claude has a question about this task.")}</div></div>
        ${q?.options?.length ? `<div class="options">${q.options.map((o) => `<button type="button" class="option" data-answer="${esc(o)}">${esc(o)}</button>`).join("")}</div>` : ""}
        <form class="reply">
          <textarea rows="1" placeholder="Answer Claude…" aria-label="Your answer" required></textarea>
          <button class="circle-btn" aria-label="Send answer">${icons.arrowUp}</button>
        </form>` : ""}
      ${pr || session ? `<div class="issue-links">
        ${pr ? `<a href="${esc(pr)}" target="_blank" rel="noopener">Pull request ${icons.external}</a>` : ""}
        ${session ? `<a href="${esc(session)}" target="_blank" rel="noopener">Session ${icons.external}</a>` : ""}
      </div>` : ""}
    </article>`;
}

export async function render(root, { api, go, toast, isCurrent }, key) {
  const [p, tasks] = await Promise.all([api.getProject(key), api.listTasks(key)]);
  if (!isCurrent()) return;
  if (!p) return go("/");

  tasks.sort((a, b) => (b.status === "needs_input") - (a.status === "needs_input"));
  let filter = tasks.some((t) => t.status === "needs_input") ? "needs" : "all";

  root.innerHTML = `
    <div class="screen">
      <header class="page-head">
        <button class="circle-btn light" id="back" aria-label="Back">${icons.back}</button>
        <h1>${esc(p.label)}<span class="sub">${tasks.length} issue${tasks.length === 1 ? "" : "s"}</span></h1>
        <button class="circle-btn solid" id="new" aria-label="New issue" style="color:${esc(p.color)}">${icons.plus}</button>
      </header>
      <nav class="filters" aria-label="Filter issues">
        ${FILTERS.map(([k, label, fn]) => `<button class="filter" data-f="${k}" aria-pressed="false">${label} · ${tasks.filter(fn).length}</button>`).join("")}
      </nav>
      <div class="issues" id="list"></div>
    </div>`;

  const list = root.querySelector("#list");
  const draw = () => {
    const fn = FILTERS.find(([k]) => k === filter)[2];
    const shown = tasks.filter(fn);
    root.querySelectorAll(".filter").forEach((b) => {
      const [, label, f] = FILTERS.find(([k]) => k === b.dataset.f);
      b.textContent = `${label} · ${tasks.filter(f).length}`;
      b.setAttribute("aria-pressed", String(b.dataset.f === filter));
    });
    list.innerHTML = shown.length ? shown.map(issueHTML).join("") : `<div class="empty">Nothing here.</div>`;
  };
  draw();

  root.querySelector("#back").addEventListener("click", () => go(`/p/${key}`));
  root.querySelector("#new").addEventListener("click", () => go(`/p/${key}/new`));
  root.querySelector(".filters").addEventListener("click", (e) => {
    const b = e.target.closest(".filter");
    if (b) { filter = b.dataset.f; draw(); }
  });

  list.addEventListener("input", (e) => {
    if (e.target.matches("textarea")) { e.target.style.height = "auto"; e.target.style.height = e.target.scrollHeight + "px"; }
  });
  async function answer(card, text) {
    if (!text) return;
    card.querySelectorAll("button, textarea").forEach((el) => (el.disabled = true));
    try {
      await api.answerQuestion(card.dataset.id, text);
      tasks.find((t) => t.id === card.dataset.id).status = "queued";
      toast("Sent. Claude will pick it up again.");
      if (!tasks.some((t) => t.status === "needs_input")) filter = "all";
      draw();
    } catch (err) {
      console.error(err);
      toast(err.message || "Couldn't send the answer.");
      card.querySelectorAll("button, textarea").forEach((el) => (el.disabled = false));
    }
  }
  list.addEventListener("submit", (e) => {
    e.preventDefault();
    answer(e.target.closest(".issue"), e.target.querySelector("textarea").value.trim());
  });
  list.addEventListener("click", (e) => {
    const o = e.target.closest(".option");
    if (o) answer(o.closest(".issue"), o.dataset.answer);
  });
}
