// Real backend: Firebase Auth (Google only) in the browser, everything else through the Go API at /api.
// Same exports as mock.js.
import { initializeApp } from "https://www.gstatic.com/firebasejs/11.0.2/firebase-app.js";
import {
  getAuth, GoogleAuthProvider, onAuthStateChanged, signInWithPopup, signInWithRedirect,
  signOut as fbSignOut, connectAuthEmulator,
} from "https://www.gstatic.com/firebasejs/11.0.2/firebase-auth.js";
import { firebaseConfig, useAuthEmulator } from "./firebase-config.js";

export const isMock = false;

if (firebaseConfig.apiKey === "REPLACE_ME") {
  throw new Error("Firebase is not configured yet: fill in app/js/firebase-config.js (or open the app with ?mock).");
}

const app = initializeApp(firebaseConfig);
const auth = getAuth(app);
if (useAuthEmulator) connectAuthEmulator(auth, "http://localhost:9099", { disableWarnings: true });

// ---------- auth ----------
export const onAuth = (cb) => onAuthStateChanged(auth, cb);

export async function signIn() {
  const provider = new GoogleAuthProvider();
  provider.setCustomParameters({ prompt: "select_account" });
  // Popups are unreliable in an installed iOS PWA; use a redirect there.
  const standalone = matchMedia("(display-mode: standalone)").matches || navigator.standalone;
  if (standalone && /iP(hone|ad|od)/.test(navigator.userAgent)) return signInWithRedirect(auth, provider);
  try {
    await signInWithPopup(auth, provider);
  } catch (e) {
    if (e.code === "auth/popup-blocked") return signInWithRedirect(auth, provider);
    if (e.code !== "auth/popup-closed-by-user" && e.code !== "auth/cancelled-popup-request") throw e;
  }
}

export const signOut = () => { projectCache = null; return fbSignOut(auth); };

// ---------- API ----------
export class ApiError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

async function api(path, { method = "GET", body } = {}) {
  const user = auth.currentUser;
  if (!user) throw new ApiError(401, "Not signed in");
  const res = await fetch("/api" + path, {
    method,
    headers: {
      Authorization: `Bearer ${await user.getIdToken()}`, // refreshed by the SDK when close to expiry
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.error || `Request failed (${res.status})`);
  return data;
}

/** true if allowed, false if the API says 403; throws when the server can't be reached. */
export async function isAllowed() {
  try {
    await api("/me");
    return true;
  } catch (e) {
    if (e.status === 403) return false;
    throw e;
  }
}

const toTask = (t) => ({ ...t, createdAt: t.createdAt ? new Date(t.createdAt) : null });

let projectCache = null;

export async function listProjects() {
  const { projects } = await api("/projects");
  projects.sort((a, b) => a.label.localeCompare(b.label));
  projectCache = new Map(projects.map((p) => [p.key, p]));
  return projects;
}

export async function getProject(key) {
  try {
    return await api(`/projects/${encodeURIComponent(key)}`);
  } catch (e) {
    if (e.status === 404) return null;
    throw e;
  }
}

/** Counts come with the project list; fetch it if we got here by a deep link. */
export async function projectStats(key) {
  if (!projectCache?.has(key)) await listProjects();
  const p = projectCache.get(key);
  return { done: p?.done ?? 0, needsInput: p?.needsInput ?? 0 };
}

export async function listTasks(key) {
  const { tasks } = await api(`/projects/${encodeURIComponent(key)}/tasks`);
  return tasks.map(toTask);
}

export async function createTask({ project, category, title, description }) {
  const t = await api("/tasks", { method: "POST", body: { project, category, title, description } });
  return t.id;
}

export async function answerQuestion(id, answer) {
  await api(`/tasks/${encodeURIComponent(id)}/answer`, { method: "POST", body: { answer } });
  return true;
}

// ---------- projects ----------
// The API only accepts these fields; the routine URL + token are write-only (never returned).
const projectBody = (p, secrets) => ({
  key: p.key, label: p.label, description: p.description, color: p.color, logo: p.logo || "",
  categories: p.categories,
  routineUrl: secrets?.routineUrl || "", routineToken: secrets?.routineToken || "",
});

export async function createProject(project, secrets) {
  projectCache = null;
  return api("/projects", { method: "POST", body: projectBody(project, secrets) });
}

/** `secrets` is null to keep the stored URL + token. */
export async function updateProject(key, project, secrets) {
  projectCache = null;
  return api(`/projects/${encodeURIComponent(key)}`, { method: "PATCH", body: projectBody({ ...project, key }, secrets) });
}

export async function archiveProject(key) {
  projectCache = null;
  return api(`/projects/${encodeURIComponent(key)}/archive`, { method: "POST", body: {} });
}
