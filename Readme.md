# Mandamás

Write tasks (bugs, features, UI…) in one app and have Claude Code work on them in the cloud, one routine per GitHub repo, while your PC is off. Claude opens a PR on `task/<id>`, asks you through the app when it's blocked, and you review and merge.

```
App ─▶ Go server (/api) ─▶ Firestore task (queued)
                              │ dispatcher
                              ▼
            Claude Code routine for that repo (API trigger)
                              │ works on branch task/<id>
                              ▼
   PR opened ─▶ MCP update_task (pr_open) ─▶ push to your phone
   blocked   ─▶ MCP request_input ─▶ you answer in the app ─▶ routine fired again
```

Setup has two parts: **the server once**, then **each repo** you want Claude to work on.

---

## Part 1 — Server (once)

### 1. Firebase project

1. Create a project at [console.firebase.google.com](https://console.firebase.google.com) (the free Spark plan is enough).
2. **Firestore**: create the database, then deploy the deny-all rules (only the server touches Firestore):
   ```bash
   firebase deploy --only firestore:rules,firestore:indexes --project <your-project-id>
   ```
3. **Authentication** → Sign-in method → enable **Google**. Under Settings → **Authorized domains**, add the domain the app is served from.
4. **Remote Config** → add a parameter `allowed_emails` with a JSON array of the emails that may sign in, e.g. `["you@example.com"]`, and publish.
5. **Project settings → General** → add a Web app and copy its config into `app/js/firebase-config.js`.
6. **Cloud Messaging** (for pushes): web push needs no extra setup on the server side; for iOS, upload an APNs key under Project settings → Cloud Messaging.

### 2. Service account

Project settings → Service accounts → **Generate new private key**. Store the JSON **outside the repo** and lock it down:

```bash
chmod 600 /path/outside/repo/service-account.json
```

It needs Firestore access and Remote Config read (the default Firebase Admin SDK account has both).

### 3. `.env`

Create `.env` next to `docker-compose.yml` (never commit it):

```bash
FIREBASE_PROJECT_ID=your-project-id
SERVICE_ACCOUNT_PATH=/path/outside/repo/service-account.json
API_PORT=9090            # the only port (app, /api and /mcp); same inside the container and on the host
API_TARGET=prod          # dev = live reload, prod = small static binary
TRUSTED_PROXIES=         # IP/CIDR of your front proxy, for correct client IPs in logs
MCP_API_KEY=             # at least 32 chars: openssl rand -hex 32  (empty = /mcp disabled)
DISPATCHER=on            # off = this instance never fires routines
```

```bash
chmod 600 .env
```

### 4. Run it

```bash
docker compose up -d --build
docker logs -f mandamas-api-1 | grep -v '"msg":"request"'
```

You should see `listening`, then `dispatcher started`, and **no** `MCP endpoint disabled` warning.

Put it behind HTTPS (nginx, Caddy, Traefik…) pointing your domain at `API_PORT`, with gzip on. The MCP endpoint must be reachable from the internet over HTTPS, because Claude's cloud sessions call it.

Check: `https://<your-domain>/healthz` returns `{"ok":true}`, and signing in to the app with an allowed email works.

> Changing the port later is a `.env` edit plus `docker compose up -d`. Also update your front proxy, and the Firebase API key's referrer restrictions if you set any.

### 5. Connect the MCP server to Claude (once)

Routines report back to Mandamás through its MCP server. Add it once as a custom connector in claude.ai:

1. claude.ai → **Settings → Connectors → Add custom connector**.
2. Name: `Mandamás`. URL:
   ```
   https://<your-domain>/mcp/<MCP_API_KEY>
   ```
   The key goes in the URL because claude.ai connectors only take a URL. **Treat this URL as a password.** Clients that can send headers can use `https://<your-domain>/mcp` with `Authorization: Bearer <MCP_API_KEY>` instead.
3. Save. Claude should list 5 tools: `list_projects`, `create_task`, `get_task_status`, `update_task`, `request_input`.

To rotate the key: change `MCP_API_KEY` in `.env`, `docker compose up -d`, and update the connector URL.

---

## Part 2 — Each repo

Do these steps for every GitHub repo you add. Test one small task end to end before adding the next repo.

### 1. Protect `main` on GitHub

Repo → Settings → Branches (or Rules → Rulesets) → add a rule for `main` (or your default branch):

- Require a pull request before merging (at least 1 approval).
- Block force pushes and deletions.

Claude only opens PRs; you review and merge.

### 2. Add a `CLAUDE.md` to the repo

Claude reads it at the start of every session. Commit it to the default branch. Keep it practical:

```markdown
# CLAUDE.md

## Setup
- Install: `npm ci`  (or `pip install -r requirements.txt`, `go mod download`…)
- Env for tests: copy `.env.example` to `.env.test`; no real secrets needed.

## Commands
- Build: `npm run build`
- Tests: `npm test` (all must pass before opening a PR)
- Lint/format: `npm run lint && npm run format`

## Conventions
- Language/framework versions, folder layout, naming, error handling.
- Every bug fix comes with a test that failed before the fix.

## Never touch
- `migrations/` already applied, `infra/`, CI secrets, `LICENSE`.
- Don't add dependencies without saying why in the PR.
```

### 3. Create the routine

Go to [claude.ai/code/routines](https://claude.ai/code/routines) → **New routine**.

1. **Name**: the repo name, e.g. `billing-api`.
2. **Repository**: select this repo only (one routine per repo).
3. **Branch pushes**: Claude must be able to push branches named `task/<id>`. If the routine or repo settings restrict pushes to `claude/`-prefixed branches, turn on unrestricted branch pushes for this repo. Branch protection on `main` (step 1) still blocks direct pushes there.
4. **Environment**: pick or create a cloud environment with the network access the repo's install and tests need (package registries, etc.). Add a setup script if dependencies need one.
5. **Connectors**: enable **Mandamás** (from Part 1, step 5). Turn off connectors this repo doesn't need.
6. **Prompt**: paste this, then add anything repo-specific at the end:

   > You will receive a task from Mandamás with an ID, a category and a branch name. Bug: reproduce with a failing test first, then fix. Feature: implement with tests and update docs. Other categories: do what the task describes, following CLAUDE.md.
   >
   > Always work on branch `task/<id>`. If the task says **Resume**, check out the existing branch, read the answered questions, and continue from where it stopped; do not start over.
   >
   > When done, open a PR from `task/<id>` that references the task ID in the title and body, then call the Mandamás tool `update_task` with status `pr_open` and the PR URL.
   >
   > Only call `request_input` when you are truly blocked or a choice is costly to reverse. Ask one clear question, with options when possible. For small decisions, choose the reasonable option and note the assumption in the PR description. Before calling `request_input`, commit and push your progress to `task/<id>`. After calling it, end the session immediately; you will be started again with the answer.
   >
   > If you can't complete the task, push what you have, then call `update_task` with status `failed` and a short reason (no secrets).

7. **Trigger**: add an **API** trigger and **generate a token**. Copy:
   - the trigger URL: `https://api.anthropic.com/v1/claude_code/routines/<id>/fire`
   - the token: `sk-ant-oat01-…` (shown once)

   Don't add a schedule trigger; Mandamás fires the routine when there's a task.

8. Save the routine.

> The token can start work on this repo. Don't paste it anywhere except the app. If it leaks, regenerate it in the routine (that revokes the old one) and update the project in the app.

### 4. Add the project in the app

App home → **New project**:

| Field | What to put |
|---|---|
| Key | Lowercase, numbers, dashes, e.g. `billing-api`. Can't change later. |
| Title, description, color, logo | Shown on the home card. |
| Categories | 1–12, e.g. `bug`, `feature`, `ui`. They feed the `/` menu in the editor. |
| Trigger URL + token | From step 3.7. Saved write-only: the app never shows them again. |

No server restart needed; the server picks up new projects and secrets live.

### 5. Test end to end

1. In the project, create a small task, e.g. category `bug`, title `Fix typo in README`.
2. Within seconds its status goes `queued → dispatched`, and the task gets a session link (open it to watch Claude work).
3. Claude opens a PR on `task/<id>` and the task moves to `pr_open` with the PR link, plus a push notification.
4. Review and merge the PR on GitHub.

To test questions: write a task that's deliberately ambiguous. The task should go to `needs_input`; answer it in **Issues**, and a new session continues from the same branch.

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Task goes straight to `failed` with "Project not configured" | Project missing, archived, or saved without a trigger URL + token. Edit the project and paste both. |
| `failed` with "routine API answered 401/403" | Token wrong or regenerated. Generate a new one and update the project. |
| `failed` with "routine API answered 404" | Trigger URL wrong or the routine was deleted. |
| Task stays `queued` | Rate limit (25 fires/hour per routine, 90 per account kept as margin); it fires when there's room. Or the server is down: check `docker logs`. |
| Task stays `dispatched` forever | Claude finished without calling `update_task` (check the session link), or the Mandamás connector isn't enabled on the routine. |
| Claude can't push `task/<id>` | Allow unrestricted branch pushes for the repo in the routine (Part 2, step 3.3). |
| Claude says it has no Mandamás tools | Connector not enabled on the routine, wrong key in its URL, or `/mcp` disabled (`MCP_API_KEY` empty or under 32 chars). |
| Stuck in `dispatching` | The server restarted mid-fire. It re-queues after 10 minutes automatically. |
| No push notifications | The app doesn't register devices yet (`devices` collection is empty); pushes are logged as "no devices registered". |

Each fire, including every resume after an answer, counts toward the routine limits. Prefer one well-formed question over several small ones.

## Development

```bash
docker compose up -d                       # dev (API_TARGET=dev): http://localhost:$API_PORT, live reload
docker compose run --rm api go vet ./...
docker compose run --rm api go test ./...
```

Open `app/index.html?mock` to preview the app without Firebase. See `CLAUDE.md` for architecture, data model and rules.
