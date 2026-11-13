# CLAUDE.md — Mandamás (Go API + MCP server + dispatcher, and the PWA)

## What this project is

A Go server (Gin) that lets me write tasks (bugs, features, etc.) in one place and have Claude work on them autonomously across several GitHub repos, in the cloud, while my PC is off.

It does four jobs in one process (one container, port 8090):

0. **App API + static app**: serves the PWA from `app/` and the JSON API at `/api` that the app uses for everything except Google sign-in. Built.
1. **MCP server** (not built yet): exposes tools so Claude (in chat or inside a routine) can create, list, and update tasks.
2. **Dispatcher** (not built yet): listens to Firestore for queued tasks and fires the matching Claude Code routine for that task's project.
3. **Routine caller**: the only reader of each project's routine URL + token (`projectSecrets`). The app writes them when creating a project but can never read them back.

## Architecture

```
PWA (Firebase Auth only) ──Bearer ID token──▶ Go API /api ──Admin SDK──▶ Firestore `tasks` (status: queued)
                              │
                     onSnapshot listener (this server)
                              │
        project → routine lookup (Firestore projects + projectSecrets)
                              │
        POST /v1/claude_code/routines/{id}/fire  (one routine per repo)
                              │
     Claude Code on the web: works in the repo, opens a PR on a new branch
                              │
     Routine calls MCP tool `update_task` ──▶ Firestore task: pr_open + prUrl

If Claude is blocked mid-task (ask, stop, resume):
     Routine pushes branch, calls `request_input` ──▶ task: needs_input + question
                              │
                   FCM push to the app (Admin SDK)
                              │
     User answers in app ──▶ task: answer saved, status back to queued
                              │
     Dispatcher fires the routine again with task + Q&A + "continue from branch task/<id>"
```

- Firebase is on the **Spark plan: no Cloud Functions**. All triggering happens in this always-on server via Firestore listeners.
- Claude does not "listen" to events. This server listens and starts Claude by firing routines.
- Repo-specific knowledge (build, tests, conventions) lives in each repo's own `CLAUDE.md` and in its routine's prompt, **not** here.

## Firestore data model

### `tasks/{taskId}`
| Field | Type | Notes |
|---|---|---|
| `project` | string | Key of a `projects` doc, e.g. `billing-api` |
| `category` | string | Must be in that project's allowed categories (bug, feature, ui…) |
| `title` | string | |
| `description` | string | Becomes the routine `text` (max 65,536 chars total) |
| `status` | string | `queued` → `dispatching` → `dispatched` → (`needs_input` → `queued` …) → `pr_open` → `done` / `failed` |
| `questions` | array | `{ question, options[], askedAt, answer?, answeredAt? }`, appended by `request_input`, answered by the app |
| `branch` | string | `task/<id>`; set on first dispatch so resumed sessions continue from it |
| `resumeCount` | number | Incremented on each re-fire after an answer |
| `remindedAt` | timestamp | Last reminder push for an unanswered question |
| `sessionUrl` | string | From the routine fire response |
| `prUrl` | string | Set by the routine via `update_task` |
| `error` | string | Set when `failed` (never include secrets) |
| `retryAfter` | timestamp | Set on 429; dispatcher re-queues after this time |
| `createdBy` | string | Email of the app user who created it |
| `createdAt`, `updatedAt` | timestamp | |

### `projects/{projectKey}` (created and edited from the app)
Key: `^[a-z0-9][a-z0-9-]{1,39}$`, chosen at creation, never changes.

| Field | Type | Notes |
|---|---|---|
| `label` | string | ≤ 60 chars |
| `description` | string | ≤ 300 chars |
| `color` | string | `#rrggbb` |
| `logo` | string | `""` or a `data:image/(webp\|png\|jpeg);base64,…` URL ≤ 200 KB (resized to 256px in the browser; no Firebase Storage on Spark) |
| `categories` | string[] | 1–12, unique; feeds the `/` menu and task validation |
| `archived` | bool | Archived projects are hidden and reject new tasks. No deletes |
| `createdBy`, `createdAt`, `updatedAt` | | |

### `projectSecrets/{projectKey}` (write-only for the app)
`routineUrl` (`https://api.anthropic.com/v1/claude_code/routines/<id>/fire`), `routineToken` (`sk-ant-oat01-…`), `updatedAt`. Rules: app may create/update when the project exists, **never read**. Only this server reads it (Admin SDK).

### `devices/{deviceId}`
The app saves its FCM registration token here (`token`, `platform`, `updatedAt`). The server reads it to send pushes and deletes tokens FCM reports as unregistered or invalid.

### Access model
- **Firestore rules deny every client read and write.** Only this server touches Firestore, via the Admin SDK (which bypasses rules). `tests/rules.test.js` checks the deny-all (`npm run test:rules`).
- **Allowlist:** Firebase Remote Config parameter `allowed_emails` (JSON array). The API reads the template with the service account (Remote Config REST API), caches it 5 min, keeps the last good copy if a refresh fails, and denies everyone if it never loaded (503). The app never reads Remote Config; it calls `GET /api/me`.
- What the app may do is decided by the API handlers (see "App API"): create/edit/archive projects, write (never read) project secrets, create `queued` tasks, answer a `needs_input` question. It can never set `status` freely, `sessionUrl`, `prUrl`, `branch` or `createdBy`.

## Configuration (.env)

Server-level settings live in `.env` (read by docker compose). Never commit it; `.env.example` has placeholders. Projects and their routine URL + token live in Firestore and are created from the app; the allowlist lives in Remote Config.

```bash
FIREBASE_PROJECT_ID=your-project-id
SERVICE_ACCOUNT_PATH=/path/outside/repo/service-account.json   # mounted read-only at /run/secrets/service-account.json
API_PORT=8090                          # the only published port (app + /api); the front server routes the domain here
API_TARGET=dev                         # dev = air live reload, prod = static binary on distroless
TRUSTED_PROXIES=                       # front proxy IPs/CIDRs for correct client IPs in logs
MCP_API_KEY=change-me                  # auth for the MCP endpoint (later)
```

`internal/config` validates on startup and names the missing variable (never a value). Emulator mode (`FIREBASE_AUTH_EMULATOR_HOST` set) skips credentials and takes the allowlist from `DEV_ALLOWED_EMAILS`; it can't work in production because emulator tokens are unsigned. Changes to `.env` require a restart. The service account needs Firestore access and Remote Config read (`firebase.remoteconfig` scope); add the domain to Firebase Auth → Authorized domains.

**Project config is live, no restart:** keep an in-memory map from `onSnapshot` on `projects` and `projectSecrets` (both small). A project that is missing, archived, or has no secrets makes its tasks `failed` with "Project not configured"; log the key and missing field, never values.

## Dispatcher rules

- Listen with `tasks.where("status", "==", "queued").onSnapshot(...)` and handle `added` changes. On restart, the first snapshot delivers all still-queued tasks, which is intended and catches tasks created while the server was down.
- **Claim before firing** with a transaction: only proceed if `status === "queued"`, then set `dispatching`. The routine endpoint has **no idempotency key**, so every fire creates a new session and duplicate fires mean duplicate PRs.
- Fire request:
  - `POST {routineUrl}`
  - Headers: `Authorization: Bearer {token}`, `anthropic-version: 2026-10-21`, `Content-Type: application/json`
  - Body: `{ "text": "Task {id}\nCategory: {category}\nBranch: task/{id}\n{title}\n\n{description}" }`
  - On a **resume** (task has answered questions), append the full Q&A history and the line `Resume: continue from existing branch task/{id}; do not start over.` Increment `resumeCount`. Keep the total under 65,536 chars, truncating the oldest Q&A first if needed.
  - Response: `{ type: "routine_fire", claude_code_session_id, claude_code_session_url }`. It returns once the session is created and does not wait for the work.
- On success, set `status: "dispatched"` and `sessionUrl`.
- On **429**, set the task back to `queued` with `retryAfter` from the `Retry-After` header. Don't mark it failed.
- On other errors or unknown project, set `status: "failed"` with a short `error`.
- **Stuck-task sweeper**: every few minutes, reset tasks in `dispatching` for more than 10 minutes back to `queued`.
- Respect limits: **30 fires/hour per routine, 100 fires/hour per account** (shared with "Run now" in the web UI). Queue rather than burst.
- The routine fire API is **experimental**. Keep all calls to it in one module so shape changes are a one-file fix.

## MCP tools

| Tool | Purpose | Rules |
|---|---|---|
| `list_projects` | Project keys, labels, categories (non-archived, from the in-memory map) | Never return URLs or tokens |
| `create_task` | Add a task from chat | Validate project and category against config; status starts `queued` |
| `get_task_status` | Show task(s) and where they stand | Filter by project or status |
| `update_task` | Routines report back (`pr_open`, `prUrl`, `done`, `failed`) | Must **not** allow changing `project` or `category` |
| `request_input` | Routine asks the user a question when blocked | Appends to `questions`, sets `needs_input`, sends FCM push, tells Claude to push its branch and end the session |

The MCP server requires auth (`MCP_API_KEY`). Routines and any other client must send it.

## Clarifications and notifications

**Pattern: ask, stop, resume.** Routine sessions run unattended, so Claude must never block waiting for an answer (no long-polling or "wait for answer" tools, which burn usage and can time out).

1. Claude commits and pushes progress to `task/<id>`, then calls `request_input(taskId, question, options?)`.
2. The server appends the question, sets `status: "needs_input"`, and calls `notifyUser`.
3. The user answers in the app, which saves the answer and sets `status: "queued"`.
4. The existing dispatcher listener picks it up and fires the routine with task + Q&A + branch (see Dispatcher rules). No separate resume code path.

**Push notifications (FCM)**
- FCM works on the Spark plan. The server sends with Admin SDK `messaging().sendEachForMulticast` using tokens from `devices`. No Cloud Functions needed.
- `notifyUser(taskId, title, body)` is shared by every event: question asked, PR ready (`pr_open`), task failed.
- Payload: `notification: { title, body }` with the body truncated to about 120 chars, and `data: { taskId, type }` so tapping opens the task screen.
- Clean up tokens that come back as `messaging/registration-token-not-registered` or `invalid-registration-token`.
- iOS requires APNs configured in the Firebase console, and web push requires a service worker in the app.
- **Reminder sweeper**: if a task stays in `needs_input` for more than 24h and `remindedAt` is older than 24h, send a reminder and update `remindedAt`. Never auto-answer.

**Fallback**: the user can open `sessionUrl` in claude.ai to continue that session directly. This is useful for long back-and-forth, but it bypasses the app and task status.

## Security checklist

- `.env` and the service account JSON are in `.gitignore`, `chmod 600`, and outside any Docker image (`env_file` + read-only mount at runtime).
- Never log tokens, the service account, or full request headers.
- Routine tokens (`sk-ant-oat01-…`) can each start work on one repo. Treat them as top-level secrets. They live in `projectSecrets`, which no client can read; the app never displays a token after saving. Regenerating one in claude.ai/code/routines revokes the old one; do that when archiving a project.
- Every target repo has branch protection on `main`, so Claude only opens PRs and a human reviews and merges.
- Task text and answers are untrusted input to Claude. Only allowlisted app users (verified by the API) and authenticated MCP clients can create tasks or write answers.
- Each resume is a new routine fire and counts toward the 30/hour per-routine limit. Prefer one well-formed question over several small ones.
- Push payloads should carry only a short question preview and `taskId`, never secrets or code.

## Per-repo setup (done outside this project)

For each repo to add:
1. Add a `CLAUDE.md` to that repo: how to install, build, run tests and lint, conventions, and what never to touch.
2. Create a routine at claude.ai/code/routines with that repo plus this MCP server as a connector, and a prompt like:
   > You will receive a task with an ID and category. Bug: reproduce with a failing test first, then fix. Feature: implement with tests and update docs. Always work on branch `task/<id>`; if the task says Resume, check out the existing branch and continue, taking the answered questions into account. When done, open a PR that references the task ID, then call `update_task` with status `pr_open` and the PR URL. Only call `request_input` when you are truly blocked or a choice is costly to reverse; ask one clear question, with options when possible. For small decisions, choose the reasonable option and note the assumption in the PR description. Before calling `request_input`, commit and push your progress, and after calling it, end the session. If you can't complete the task, call `update_task` with status `failed` and a short reason.
3. Add an API trigger and generate the token.
4. In the app: home → **New project**, fill title, color, logo, categories, and paste the trigger URL + token. No restart. Test with one small task end to end before adding the next repo.

## Running

- Docker compose with `restart: unless-stopped`, so the listener survives reboots and crashes.
- Spark plan: listener updates and transactions count toward the free daily Firestore read and write quotas. Only listen to `status == "queued"`, never whole collections.

## App API (`api/`)

Go 1.26, Gin, Firebase Admin SDK for Go. Every `/api/*` request goes through, in order: `Cache-Control: no-store`, 512 KB body limit, **auth middleware** (`Authorization: Bearer <Firebase ID token>` → `VerifyIDToken` → `email_verified` → email in the Remote Config allowlist; 401/403/503), per-user rate limit (token bucket), JSON-only writes. Bodies are decoded strictly (unknown fields → 400). Validation rules live in `internal/validate`. Logs: method, path, status, ms, user email; never headers, tokens or bodies.

| Method + path | Does |
|---|---|
| `GET /healthz` | no auth |
| `GET /api/me` | `{uid, email, name}`; 403 if not allowed |
| `GET /api/projects` | non-archived projects with `done` / `needsInput` counts and `hasSecrets` |
| `GET /api/projects/:key` | one project |
| `POST /api/projects` | project + secrets in one transaction; 409 if the key exists |
| `PATCH /api/projects/:key` | public fields; secrets replaced only if both URL and token are sent |
| `POST /api/projects/:key/archive` | `archived: true` |
| `GET /api/projects/:key/tasks` | latest 100, newest first |
| `POST /api/tasks` | project must exist, not archived, category enabled; server sets `queued`, `createdBy`, timestamps |
| `POST /api/tasks/:id/answer` | transaction: only when `needs_input`; answers the last question and re-queues |

Secrets are never returned. Everything that isn't `/api` or `/healthz` is served from `APP_DIR` (`./app` mounted read-only): unknown paths fall back to `index.html`, `index.html`/`sw.js`/manifest are `no-cache`. The front server should gzip.

Packages: `cmd/server` (wiring, graceful shutdown), `internal/config`, `internal/allowlist`, `internal/auth`, `internal/validate`, `internal/store` (the only Firestore code, behind an interface), `internal/httpapi` (router, middleware, handlers, static). Later: `internal/mcp`, `internal/dispatcher`, `internal/routines`, `internal/notifications`.

## Client app (`app/`)

Static PWA: plain HTML + ES modules, no build step, served by the Go container. The only Firebase SDK in the browser is Auth.

- Google sign-in only. After login the app calls `GET /api/me`: 200 → in, 403 → "Not allowed" page and sign-out, other errors → "Can't reach Mandamás". `app/js/firebase.js` adds the ID token to every `/api` call (`getIdToken()` refreshes it).
- Screens: login, not-allowed, home (project cards + "New project"), project pre-home (with edit), project form (new/edit/archive, `#/new-project`, `#/p/:key/edit`), issues (answer Claude's questions), editor (`/` at the start of a line picks the category).
- `app/js/firebase.js` and `app/js/mock.js` export the same API. Open `index.html?mock` (or `?mock=login`, `?mock=blocked`) to preview without Firebase.
- Not built yet: FCM token registration into `devices` and push handling in `sw.js`.

## Running and testing

Go isn't needed on the host; everything runs in Docker.

```bash
docker compose up -d                       # dev: http://localhost:8090, air rebuilds on .go changes
API_TARGET=prod docker compose up -d --build
docker compose run --rm api go vet ./...
docker compose run --rm api go test ./...
npm run test:rules                         # Firestore deny-all rules in the emulator
```

Store tests run against the Firestore emulator when `FIRESTORE_EMULATOR_HOST` is set (skipped otherwise), e.g. `firebase emulators:exec --only firestore --project demo-mandamas "docker run --rm --network host -v $PWD/api:/src -w /src -e FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 golang:1.26.10-alpine go test ./internal/store/"`.

## Conventions for working on this repo

- Go (module `github.com/mandamas/api`), Gin, Firebase Admin SDK for Go, official Go MCP SDK when the MCP step starts. `log/slog` JSON logs.
- Keep packages small and single-purpose (list above). All Firestore access goes through `internal/store`; all calls to the routine fire API go through one package.
- No secrets in code, tests, fixtures, or error messages.
- Tests should mock the routine fire API. Never fire real routines from tests.

## Alternative noted (not chosen)

GitHub-only: one GitHub Projects board across repos, issues with category labels, and `anthropics/claude-code-action` in each repo with a `label_trigger`. No dispatcher is needed and it's billed via the API instead of the subscription, but the inbox lives in GitHub instead of my app.
