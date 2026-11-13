// In-memory stand-in for firebase.js, used with ?mock so the screens can be previewed without Firebase.
//   ?mock          signed in as an allowed user
//   ?mock=login    starts signed out (login screen)
//   ?mock=blocked  signed in with an email that is not allowed

const mode = new URLSearchParams(location.search).get("mock") || "";

const allowedUser = { displayName: "Sam Rivera", email: "owner@example.com", photoURL: "" };
const blockedUser = { displayName: "Someone Else", email: "someone@example.com", photoURL: "" };

let user = mode === "login" ? null : mode === "blocked" ? blockedUser : allowedUser;
const listeners = new Set();
const emit = () => listeners.forEach((cb) => cb(user));

const projects = [
  {
    key: "billing-api",
    label: "Billing API",
    description: "Invoices, payments and the Stripe webhooks that keep them in sync.",
    color: "#3b6ff5",
    logo: "assets/projects/billing-api.svg",
    categories: ["bug", "feature", "security"],
  },
  {
    key: "mobile-app",
    label: "Mobile App",
    description: "The React Native app our customers carry around.",
    color: "#e8590c",
    logo: "assets/projects/mobile-app.svg",
    categories: ["bug", "feature", "ui"],
  },
  {
    key: "landing",
    label: "Landing Site",
    description: "Marketing pages and the blog.",
    color: "#0f9f6e",
    logo: "",
    categories: ["bug", "ui", "docs"],
  },
];

const ago = (min) => new Date(Date.now() - min * 60_000);
let seq = 100;
const tasks = [
  { id: "t1", project: "billing-api", category: "bug", title: "Refund webhook marks invoice as paid twice", description: "When Stripe retries charge.refunded the invoice gets two ledger entries.", status: "needs_input", questions: [{ question: "Should a refund also reopen the invoice, or only add a credit note? The current tests assume both, which conflict.", options: ["Reopen the invoice", "Only add a credit note"], askedAt: ago(20) }], createdAt: ago(95) },
  { id: "t2", project: "billing-api", category: "feature", title: "Export invoices as CSV", description: "Monthly export from the admin panel, same columns as the PDF.", status: "pr_open", prUrl: "https://github.com/example/billing-api/pull/42", sessionUrl: "https://claude.ai/code/session_x", createdAt: ago(60 * 5) },
  { id: "t3", project: "billing-api", category: "bug", title: "Currency rounding on partial refunds", description: "", status: "dispatched", sessionUrl: "https://claude.ai/code/session_y", createdAt: ago(12) },
  ...Array.from({ length: 12 }, (_, i) => ({ id: "d" + i, project: "billing-api", category: i % 2 ? "bug" : "feature", title: `Shipped task #${i + 1}`, description: "", status: "done", prUrl: "https://github.com/example/billing-api/pull/" + (10 + i), createdAt: ago(60 * 24 * (i + 2)) })),
  { id: "t4", project: "mobile-app", category: "ui", title: "Dark mode for the settings screen", description: "Use the tokens from the design system.", status: "queued", createdAt: ago(3) },
  { id: "t5", project: "mobile-app", category: "bug", title: "Crash when opening a push notification while logged out", description: "", status: "failed", error: "Could not reproduce on Android 14 emulator.", createdAt: ago(60 * 30) },
  ...Array.from({ length: 5 }, (_, i) => ({ id: "m" + i, project: "mobile-app", category: "feature", title: `Shipped mobile task #${i + 1}`, description: "", status: "done", createdAt: ago(60 * 24 * (i + 3)) })),
];

const delay = (v, ms = 180) => new Promise((r) => setTimeout(() => r(v), ms));

export const isMock = true;

export function onAuth(cb) {
  listeners.add(cb);
  setTimeout(() => cb(user), 250);
  return () => listeners.delete(cb);
}
export async function signIn() { user = allowedUser; emit(); }
export async function signOut() { user = null; emit(); }
export async function isAllowed(u) { return delay(u?.email === allowedUser.email); }

export const listProjects = () => delay(projects.filter((p) => !p.archived).map((p) => ({ ...p })));
export const getProject = async (key) => (await listProjects()).find((p) => p.key === key) || null;

export function projectStats(key) {
  const mine = tasks.filter((t) => t.project === key);
  return delay({
    done: mine.filter((t) => t.status === "done").length,
    needsInput: mine.filter((t) => t.status === "needs_input").length,
  });
}

export const listTasks = (key) =>
  delay(tasks.filter((t) => t.project === key).sort((a, b) => b.createdAt - a.createdAt).map((t) => ({ ...t })));

export async function createTask({ project, category, title, description }) {
  const id = "n" + seq++;
  tasks.unshift({ id, project, category, title, description, status: "queued", createdAt: new Date() });
  return delay(id, 400);
}

export async function answerQuestion(id, answer) {
  const t = tasks.find((x) => x.id === id);
  if (t?.status === "needs_input" && t.questions?.length) {
    Object.assign(t.questions.at(-1), { answer, answeredAt: new Date() });
    t.status = "queued";
  }
  return delay(true, 300);
}

const secrets = {}; // write-only in the real app; kept here only so the mock behaves the same

export async function createProject(project, sec) {
  if (projects.some((p) => p.key === project.key)) throw new Error(`A project with key "${project.key}" already exists.`);
  projects.push({ ...project, archived: false });
  secrets[project.key] = sec;
  return delay(true, 300);
}

export async function updateProject(key, project, sec) {
  Object.assign(projects.find((p) => p.key === key), project, { key });
  if (sec) secrets[key] = sec;
  return delay(true, 300);
}

export async function archiveProject(key) {
  projects.find((p) => p.key === key).archived = true;
  return delay(true, 200);
}
