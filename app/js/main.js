import { esc, toast } from "./ui.js";
import * as login from "./views/login.js";
import * as notAllowed from "./views/not-allowed.js";
import * as home from "./views/home.js";
import * as project from "./views/project.js";
import * as issues from "./views/issues.js";
import * as editor from "./views/editor.js";
import * as projectForm from "./views/project-form.js";

const root = document.getElementById("app");
const useMock = new URLSearchParams(location.search).has("mock");

const routes = [
  [/^\/?$/, home],
  [/^\/p\/([\w-]+)\/?$/, project],
  [/^\/p\/([\w-]+)\/issues\/?$/, issues],
  [/^\/p\/([\w-]+)\/new\/?$/, editor],
  [/^\/new-project\/?$/, projectForm],
  [/^\/p\/([\w-]+)\/edit\/?$/, projectForm],
];

let api;
let user = null;
let cleanup = null;
let renderId = 0;

const go = (path) => { location.hash = "#" + path; };

async function show(view, params = []) {
  const id = ++renderId;
  if (typeof cleanup === "function") cleanup();
  cleanup = null;
  window.scrollTo(0, 0);
  try {
    const c = await view.render(root, { api, user, go, toast, isCurrent: () => id === renderId }, ...params);
    if (id === renderId) cleanup = c;
  } catch (e) {
    console.error(e);
    if (id === renderId) root.innerHTML = `<div class="screen empty"><p>Something went wrong.</p><p style="font-size:13px">${esc(e.message)}</p><button class="text-btn" onclick="location.hash='#/'">Go home</button></div>`;
  }
}

function route() {
  if (!user) return;
  const path = location.hash.replace(/^#/, "") || "/";
  for (const [re, view] of routes) {
    const m = path.match(re);
    if (m) return show(view, m.slice(1));
  }
  go("/");
}

async function boot() {
  try {
    api = useMock ? await import("./mock.js") : await import("./firebase.js");
  } catch (e) {
    root.innerHTML = `<div class="screen auth"><img class="logo" src="img/logo.png" alt=""><h1>Mandamás</h1><p>${esc(e.message)}</p></div>`;
    return;
  }

  let blocked = null; // email that was refused; keeps the not-allowed page up after we sign it out

  api.onAuth(async (u) => {
    if (!u) {
      user = null;
      if (blocked) return;
      return show(login);
    }
    blocked = null;
    root.innerHTML = `<div class="splash"><img src="img/logo.png" alt="" width="96" height="86"></div>`;
    let ok = false;
    try {
      ok = await api.isAllowed(u);
    } catch (e) {
      // Server down or allowlist not loaded: not the user's fault, so don't say "not allowed".
      console.error(e);
      root.innerHTML = `<div class="screen auth"><img class="logo" src="img/logo.png" alt=""><h1>Can't reach Mandamás</h1><p>${esc(e.message || "The server didn't answer.")}</p><button class="google-btn" id="retry">Try again</button></div>`;
      root.querySelector("#retry").addEventListener("click", () => location.reload());
      return;
    }
    if (!ok) {
      user = null;
      blocked = u.email || "this account";
      await show(notAllowed, [blocked, () => { blocked = null; api.signIn(); }]);
      // Do nothing more: drop the session so no data is ever requested.
      api.signOut();
      return;
    }
    user = u;
    route();
  });

  addEventListener("hashchange", route);
}

boot();

if ("serviceWorker" in navigator && !useMock && location.protocol === "https:") {
  navigator.serviceWorker.register("sw.js");
}
