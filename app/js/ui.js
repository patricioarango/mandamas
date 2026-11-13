// Small shared helpers: HTML escaping, icons, category metadata, toast.

export const esc = (s) =>
  String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

const svg = (body, extra = "") =>
  `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" ${extra}>${body}</svg>`;

export const icons = {
  back: svg('<path d="M15 18l-6-6 6-6"/>'),
  arrowRight: svg('<path d="M5 12h14M13 6l6 6-6 6"/>'),
  arrowUp: svg('<path d="M12 19V5M6 11l6-6 6 6"/>'),
  edit: svg('<path d="M4 20h4L19 9l-4-4L4 16v4zM13.5 6.5l4 4"/>'),
  plus: svg('<path d="M12 5v14M5 12h14"/>'),
  check: svg('<path d="M5 12.5l4.5 4.5L19 7.5"/>', 'stroke-width="2.6"'),
  close: svg('<path d="M6 6l12 12M18 6L6 18"/>'),
  logout: svg('<path d="M15 4h3a2 2 0 012 2v12a2 2 0 01-2 2h-3M10 17l5-5-5-5M15 12H3"/>'),
  external: svg('<path d="M14 4h6v6M20 4l-9 9M18 14v4a2 2 0 01-2 2H6a2 2 0 01-2-2V8a2 2 0 012-2h4"/>'),
  sparkle: svg('<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3zM19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8L19 15z"/>'),
  claude: svg('<path d="M12 2v6M12 16v6M2 12h6M16 12h6M4.9 4.9l4.2 4.2M14.9 14.9l4.2 4.2M4.9 19.1l4.2-4.2M14.9 9.1l4.2-4.2"/>', 'stroke-width="2.4"'),
  google: `<svg viewBox="0 0 48 48" aria-hidden="true"><path fill="#FFC107" d="M43.6 20.5H42V20H24v8h11.3C33.7 32.7 29.2 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.4-.4-3.5z"/><path fill="#FF3D00" d="M6.3 14.7l6.6 4.8C14.7 15.1 19 12 24 12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z"/><path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-8l-6.5 5C9.5 39.6 16.2 44 24 44z"/><path fill="#1976D2" d="M43.6 20.5H42V20H24v8h11.3c-.8 2.2-2.2 4.2-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.4-.4-3.5z"/></svg>`,
};

// Known categories get a nicer icon + hint in the "/" menu. Unknown ones fall back to a generic tag.
const CATS = {
  bug: ["Bug", "Something is broken. Claude reproduces it with a failing test first", '<path d="M8 8a4 4 0 018 0M6 13h12M12 20a6 6 0 006-6v-3a3 3 0 00-3-3H9a3 3 0 00-3 3v3a6 6 0 006 6zM12 20v-9M3 9l3 2M21 9l-3 2M3 19l3-2M21 19l-3-2"/>', "#e5484d"],
  feature: ["Feature", "Something new, with tests and docs", '<path d="M12 3l2.6 5.5 6 .8-4.4 4.2 1.1 6L12 16.6 6.7 19.5l1.1-6L3.4 9.3l6-.8L12 3z"/>', "#e8a300"],
  ui: ["UI", "Looks, layout or interaction", '<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M3 9h18M9 20V9"/>', "#8e4ec6"],
  docs: ["Docs", "README, guides or comments", '<path d="M7 3h7l5 5v13H7a2 2 0 01-2-2V5a2 2 0 012-2z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>', "#0091ff"],
  refactor: ["Refactor", "Change the code, not the behavior", '<path d="M4 7h11l-3-3M20 17H9l3 3"/>', "#12a594"],
  chore: ["Chore", "Dependencies, config, housekeeping", '<path d="M14.7 6.3a4 4 0 00-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 005.4-5.4l-2.5 2.5-2.4-.6-.6-2.4 2.5-2.5z"/>', "#6f6e77"],
  test: ["Test", "Add or fix tests", '<path d="M9 3h6M10 3v6l-5 9a2 2 0 001.8 3h10.4A2 2 0 0019 18l-5-9V3M7.5 14h9"/>', "#30a46c"],
  perf: ["Performance", "Make it faster or lighter", '<path d="M13 2L4 14h7l-1 8 9-12h-7l1-8z"/>', "#f76808"],
  security: ["Security", "Harden something", '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6l8-3z"/><path d="M9 12l2 2 4-4"/>', "#3e63dd"],
};

export const KNOWN_CATEGORIES = Object.keys(CATS);

export function categoryInfo(key) {
  const k = String(key).toLowerCase();
  const [name, hint, path, color] = CATS[k] || [
    k.charAt(0).toUpperCase() + k.slice(1),
    "Send it as " + k,
    '<path d="M20.6 13.4l-7.2 7.2a2 2 0 01-2.8 0L3 13V3h10l7.6 7.6a2 2 0 010 2.8z"/><circle cx="7.5" cy="7.5" r="1.5"/>',
    "#6f6e77",
  ];
  return { key: k, name, hint, color, icon: svg(path, `style="color:${color}"`) };
}

export const STATUS_LABEL = {
  queued: "Queued",
  dispatching: "Starting",
  dispatched: "Working",
  pr_open: "PR open",
  done: "Done",
  failed: "Failed",
  needs_input: "Needs you",
};

export function timeAgo(date) {
  if (!date) return "";
  const s = Math.round((Date.now() - date.getTime()) / 1000);
  if (s < 60) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.round(h / 24);
  if (d < 30) return `${d}d ago`;
  return date.toLocaleDateString();
}

export function greeting() {
  const h = new Date().getHours();
  return h < 12 ? "Good morning," : h < 19 ? "Good afternoon," : "Good evening,";
}

export const firstName = (user) => (user?.displayName || user?.email || "").split(/[ @]/)[0];

export function avatarHTML(user, cls = "avatar") {
  if (user?.photoURL) return `<img class="${cls}" src="${esc(user.photoURL)}" alt="" referrerpolicy="no-referrer">`;
  return `<span class="${cls}" style="background:var(--brand)">${esc(firstName(user).charAt(0).toUpperCase())}</span>`;
}

/** Project logo, or a monogram tile when the project has none. */
export function logoHTML(project, cls = "") {
  if (project.logo) return `<img class="${cls}" src="${esc(project.logo)}" alt="">`;
  return `<span class="monogram ${cls}">${esc((project.label || project.key).charAt(0).toUpperCase())}</span>`;
}

let toastTimer;
export function toast(msg) {
  const el = document.getElementById("toast");
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (el.hidden = true), 2800);
}
