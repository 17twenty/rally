# Rally — an operating system for organizations

Rally is a platform for building and operating **living organizations composed of humans and artificial employees (AEs)**. You define a company, hire AEs (CEO, CTO, engineers, etc.), connect them to real tools (Slack, GitHub, browser), and Rally runs a persistent, observable org that plans and executes work autonomously.

> Rally is not a chatbot. It is an operating system for organizations where intelligence is programmable, persistent, and collaborative.

---

## Architecture Overview

```text
User (Web UI)
   ↓
Rally Core (Go + templui)
   ↓
--------------------------------------------------
| Orchestrator | LLM Router | Knowledgebase       |
| Agent Runtime | Tool Gateway | Org Manager      |
--------------------------------------------------
   ↓
Postgres + River
   ↓
External Systems (Slack, GitHub, Browser, etc.)
```

| Component | Responsibility |
|---|---|
| **Rally Core** | Web UI (Go + templui), API layer, orchestration control plane, config management |
| **Org Manager** | Org structure, reporting lines, roles, hiring logic |
| **Agent Runtime** | Per-AE worker loop driven by River heartbeat jobs; each AE has config, memory, tools, and model routing |
| **LLM Router** | Model selection, provider routing, API normalization (OpenAI + Anthropic compatible) |
| **Tool Gateway** | Central interface for Slack, GitHub, shell, browser — enforces permissions and logging |
| **Knowledgebase** | Shared company facts, employee registry, decisions, docs, playbooks |
| **Memory** | Per-AE private episodic memory, reflections, heuristics |
| **Queue** | River-backed queue for heartbeat scheduling, retries, async tool execution |

---

## Prerequisites

- **Go 1.23+**
- **Docker** (for Postgres via docker-compose)
- **[templ CLI](https://templ.guide/quick-start/installation)** — `go install github.com/a-h/templ/cmd/templ@latest`
- **[sqlc](https://sqlc.dev/)** — `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
- **[task](https://taskfile.dev/installation/)** — Taskfile runner
- **[golang-migrate](https://github.com/golang-migrate/migrate)** — `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`
- **Playwright** — installed via `task playwright:install`

---

## Setup

```bash
# 1. Clone the repo
git clone <repo-url>
cd rally

# 2. Copy environment file and fill in values
cp .env.example .env
$EDITOR .env

# 3. Start Postgres
task docker:up

# 4. Apply DB migrations
task migrate

# 5. Start development server (templ watch + tailwind + go server)
task dev
```

The web UI will be available at `http://localhost:7331` (templ dev proxy). The proxy forwards to the Go server at `http://localhost:8432`.

> The server defaults to port 8432 in development. Set the `PORT` env var to change.

---

## Environment Variables

| Variable | Description |
|---|---|
| `DATABASE_URL` | Postgres connection string (used by `task migrate`) |
| `DB_URI` | Postgres connection string (used by Taskfile internal tasks) |
| `SLACK_BOT_TOKEN` | Slack bot token (`xoxb-...`) for posting messages and reading events |
| `SLACK_SIGNING_SECRET` | Slack signing secret for verifying incoming webhook payloads |
| `ANTHROPIC_API_KEY` | Anthropic API key for Claude models |
| `OPENAI_API_KEY` | OpenAI API key for GPT models |
| `GITHUB_TOKEN` | GitHub personal access token for the GitHub tool |
| `PORT` | HTTP server port (default: `8432` in dev, set to `8080` via docker-compose in prod) |
| `APP_NAME` | Application name used in Docker container naming (default: `rally`) |
| `RALLY_WORKSPACE_ROOT` | Host path for AE workspace storage (default: `/var/rally/workspaces` — see [Workspaces](#workspaces--ae-containers)) |
| `RALLY_API_URL` | URL AE containers use to reach Rally (default: `http://host.docker.internal:8432`) |
| `VAULT_ENCRYPTION_KEY` | AES-256 key for credential vault — generate with `openssl rand -base64 32` |
| `GREENTHREAD_API_KEY` | API key for Greenthread LLM provider (OpenAI-compatible, default provider) |
| `GOOGLE_CLIENT_ID` | Google OAuth2 client ID (for Google Workspace tool) |
| `GOOGLE_CLIENT_SECRET` | Google OAuth2 client secret |
| `GOOGLE_REDIRECT_URI` | Google OAuth2 redirect URI (default: `http://localhost:8432/oauth/google/callback`) |

---

## Workspaces & AE Containers

Each AE runs as a Docker container using the `rally-ae-base:latest` image. Rally (the control plane) provisions containers when a company's team is built, and each AE gets two filesystem mounts connecting it back to the host.

### Workspace layout

`RALLY_WORKSPACE_ROOT` is the host directory where all company workspace files live. Rally creates the following structure automatically:

```text
$RALLY_WORKSPACE_ROOT/
└── {companyID}/
    ├── shared/                  ← Shared workspace (mounted as /workspace in every AE container)
    │   ├── playbook.md
    │   └── research/
    │       └── competitors.md
    └── .ae/
        ├── {employeeID_1}/      ← Private scratch (mounted as /home/ae/scratch in that AE's container)
        │   └── screenshot-1712345678.png
        └── {employeeID_2}/
            └── draft-email.txt
```

| Mount | Host path | Container path | Visibility |
|---|---|---|---|
| **Shared workspace** | `{ROOT}/{companyID}/shared` | `/workspace` | All AEs in the company |
| **Per-AE scratch** | `{ROOT}/{companyID}/.ae/{employeeID}` | `/home/ae/scratch` | Only that AE |

- **Shared workspace** (`/workspace`): collaborative space where AEs read and write artifacts visible to the whole team — documents, research, code, playbooks. This is the AE's working directory.
- **Per-AE scratch** (`/home/ae/scratch`): private to each AE for temporary files — browser screenshots, intermediate computations, draft content before publishing to the shared workspace.

### Setting up for local development

On macOS, the default `/var/rally/workspaces` requires root. Set `RALLY_WORKSPACE_ROOT` to a local path instead:

```bash
# In .env
RALLY_WORKSPACE_ROOT=/path/to/rally/.workspaces
RALLY_API_URL=http://host.docker.internal:8432
```

`RALLY_API_URL` is the address AE containers use to call back to the Rally server. On Docker Desktop (macOS/Windows), `host.docker.internal` resolves to the host machine. In production or Linux, use the appropriate internal service URL.

### Building and running AE containers

```bash
# Build the AE base image (required before hiring)
task ae-build

# Start the database and server
task db-start
task db-migrate
task server        # or: task dev (for hot reload)

# Create a company and build its team via the web UI at http://localhost:8432
# Or via API:
curl -X POST http://localhost:8432/companies \
  --data-urlencode "name=Acme Corp" \
  --data-urlencode "mission=Build the best widgets" \
  --data-urlencode "emp_name=Your Name" \
  --data-urlencode "emp_role=CEO" \
  --data-urlencode "emp_specialties=strategy"
# Returns a redirect with the company ID

curl -X POST http://localhost:8432/companies/{id}/build
# Hires all AEs, provisions containers, returns {"status":"ready","hired":8}
```

### Managing AE containers

```bash
task ae-list          # List all running AE containers with status and role
task ae-logs -- {name} # Tail logs for a specific AE (e.g., rally-acme-corp-ceo-alex)
task ae-stop-all      # Stop all AE containers
```

### How AE containers work

Each container runs the `ae-agent` binary which:

1. **Starts a heartbeat loop** (default: every 300s) — observe, plan, act, evaluate, store
2. **Calls back to Rally** via `RALLY_API_URL` using a unique API token for authentication
3. **Executes local tools** inside the container — shell commands, file read/write in `/workspace`, browser automation via Playwright/Chromium
4. **Proxies remote tools** through Rally — Slack messages, GitHub operations, Google Workspace actions

The container receives its identity and personality via environment variables (`SOUL_MD`, `AE_CONFIG`, `EMPLOYEE_ID`, etc.), set automatically during the hiring flow.

### Container networking

AE containers join the `rally-net` Docker bridge network. The Rally server must be reachable from this network — in local dev, `host.docker.internal:8432` handles this automatically via Docker Desktop.

---

## Taskfile Tasks

| Task | Description |
|---|---|
| `task dev` | Start development server with hot reload (templ watch + tailwind) |
| `task build` | Build the server binary |
| `task test` | Run all tests |
| `task gen` | Run templ code generation |
| `task sqlc` | Generate type-safe DB code from SQL queries |
| `task db-start` | Start PostgreSQL container |
| `task db-stop` | Stop PostgreSQL container |
| `task db-reset` | Destroy and recreate the database (fresh data) |
| `task db-migrate` | Apply DB and River migrations |
| `task templ` | Run templ with integrated server and hot reload |
| `task tailwind` | Watch Tailwind CSS changes |
| `task tailwind:build` | Build Tailwind CSS for production (minified) |
| `task ae-build` | Build the AE agent Docker image (`rally-ae-base:latest`) |
| `task ae-list` | List all running AE containers with status and role |
| `task ae-logs -- {name}` | Tail logs for a specific AE container |
| `task ae-stop-all` | Stop all AE containers |
| `task playwright:install` | Install Playwright browsers (Chromium) |
| `task server` | Run server directly without hot reload (quick manual testing) |

---

## Development Workflow

```bash
task dev          # Start hot-reload dev server (templ + tailwind + go run)
task build        # Compile server binary
task test         # Run go test ./...
task sqlc         # Regenerate DB access layer after changing SQL queries
task gen          # Regenerate templ Go files after changing .templ files
```

After modifying `.templ` files, `task dev` auto-regenerates them. For a one-off generation run `task gen`.

After modifying SQL in `internal/db/queries/`, run `task sqlc` to regenerate `internal/db/dao/`.

---

## Docker Workflow

```bash
task docker:up      # Start Postgres (and any other compose services)
task docker:down    # Stop all compose services
task docker:reset   # Wipe DB volume and restart fresh
task docker:logs    # Tail server logs
```

---

## Slack Setup

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app **From scratch**.
2. Under **OAuth & Permissions**, add the following **Bot Token Scopes**:
   - `channels:history` — read messages from public channels
   - `channels:join` — join channels
   - `channels:read` — list channels
   - `chat:write` — post messages
   - `groups:history` — read messages from private channels
   - `groups:read` — list private channels
   - `im:history` — read direct messages
   - `im:read` — list DMs
   - `im:write` — open DM conversations
   - `users:read` — look up user info
   - `users:read.email` — look up users by email
3. Under **Event Subscriptions**, enable events and point the Request URL to `https://<your-host>/slack/events`.
   - Subscribe to bot events: `message.channels`, `message.groups`, `message.im`
4. Install the app to your workspace and copy the **Bot User OAuth Token** → `SLACK_BOT_TOKEN` in `.env`.
5. Copy the **Signing Secret** from Basic Information → `SLACK_SIGNING_SECRET` in `.env`.

---

## Google Workspace Setup

Rally AEs can send/receive Gmail, create Google Docs, and manage Drive files via the Google Workspace tool.

### Required OAuth Scopes

| Scope | Purpose |
|---|---|
| `https://www.googleapis.com/auth/gmail.send` | Send email as the AE |
| `https://www.googleapis.com/auth/gmail.readonly` | Read and list emails |
| `https://www.googleapis.com/auth/documents` | Create and edit Google Docs |
| `https://www.googleapis.com/auth/drive.file` | Upload and list Drive files |

### Create a Google Cloud Project & OAuth2 Credentials

1. Go to [console.cloud.google.com](https://console.cloud.google.com) and create a new project (e.g. `rally`).
2. Enable the following APIs: **Gmail API**, **Google Docs API**, **Google Drive API**.
3. Navigate to **APIs & Services → Credentials → Create Credentials → OAuth 2.0 Client ID**.
4. Set application type to **Web application**.
5. Add an authorized redirect URI: `http://localhost:8432/oauth/google/callback` (update host for production).
6. Copy the **Client ID** and **Client Secret** → set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in `.env`.
7. Set `GOOGLE_REDIRECT_URI=http://localhost:8432/oauth/google/callback` in `.env`.

### Authorize an AE

Each AE authorizes independently. To trigger the OAuth flow for a specific AE:

```
GET /oauth/google?employee_id={employee_id}
```

This redirects the user to Google's consent screen. After approval, the token is stored in the credential vault and the AE can immediately use the Google Workspace tool.

All AEs have full access to Google Workspace actions (email, docs, drive, calendar) once authorized. No additional config flags needed.

---

## Architecture Diagram (PRD §4.1)

```text
User (Web UI)
   ↓
Rally Core (Go + templui)
   ↓
--------------------------------------------------
| Orchestrator | LLM Router | Knowledgebase       |
| Agent Runtime | Tool Gateway | Org Manager      |
--------------------------------------------------
   ↓
Postgres + River
   ↓
External Systems (Slack, GitHub, Browser, etc.)
```

**Agent heartbeat loop:**

```text
observe → plan → act → evaluate → store
```

Sources feeding observe: Slack messages, scheduled tasks, AE memory, shared KB.
