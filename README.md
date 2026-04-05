# Rally — an operating system for organizations

Rally is a platform for building and operating **living organizations composed of humans and artificial employees (AEs)**. You define a company with a mission, hire a CEO, and Rally builds a persistent, observable team that plans and executes work autonomously.

> Rally is not a chatbot. It is an operating system for organizations where intelligence is programmable, persistent, and collaborative.

---

## How It Works

1. **Create a company** with a name, mission, and policies
2. **Rally hires a CEO** — the founding AI executive
3. **The CEO proposes hires** based on the company mission (CTO, engineers, designers, etc.)
4. **You approve hires** from the web UI — each gets their own Docker container
5. **AEs work autonomously** — they check Slack, manage backlogs, delegate tasks, and communicate with each other
6. **You interact via Slack** — just like talking to a real team

No hardcoded roles. The org structure is dynamic, driven by the CEO's understanding of what the company needs.

---

## Architecture

```text
Human (Slack + Web UI)
   ↓
Rally Server (Go + templui + River queue)
   ├── LLM Router (OpenAI SDK + Anthropic SDK)
   ├── Tool Gateway (Slack, GitHub, Google, Figma, Browser)
   ├── Credential Vault (plaintext in DB)
   └── Container Manager (Docker)
   ↓
AE Containers (rally-ae-base:latest)
   ├── Multi-turn agentic loop (observe → think → act → iterate)
   ├── Local tools (Bash, Read, Write, Edit, Grep, Glob, Browser)
   ├── Remote tools (via Rally gateway — Slack, GitHub, etc.)
   ├── Work tracking (BacklogList/Add/Update)
   ├── Collaboration (Delegate, Escalate, SendMessage, ProposeHire)
   └── Session state persistence (/home/ae/scratch/session_state.md)
```

Each AE runs a **context-driven heartbeat loop** (default: every 300s):
- Receives rich context: identity, team roster, company policy, backlog, session notes
- The LLM decides what to do — no hardcoded priority logic
- Calls tools, sees results, iterates (up to 25 turns per cycle)
- Writes session state for continuity across cycles

---

## Prerequisites

- **Go 1.23+**
- **Docker** (Docker Desktop for macOS/Windows)
- **[templ CLI](https://templ.guide/quick-start/installation)** — `go install github.com/a-h/templ/cmd/templ@latest`
- **[sqlc](https://sqlc.dev/)** — `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
- **[task](https://taskfile.dev/installation/)** — Taskfile runner
- **[golang-migrate](https://github.com/golang-migrate/migrate)** — `go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`

---

## Quick Start

```bash
# 1. Clone and enter the project
cd rally

# 2. Copy environment file and fill in values
cp .env.example .env
$EDITOR .env

# 3. Start Postgres
task db-start

# 4. Apply DB migrations
task db-migrate

# 5. Build the AE Docker image
task ae-build

# 6. Start the server
task server

# 7. Visit http://localhost:8432
# The setup wizard will guide you through creating a company and hiring your CEO.
```

---

## Environment Variables

### Required

| Variable | Description |
|---|---|
| `DATABASE_URL` | Postgres connection string |
| `GREENTHREAD_API_KEY` | API key for Greenthread LLM provider (default provider) |

### AE Container Runtime

| Variable | Description |
|---|---|
| `RALLY_WORKSPACE_ROOT` | Host path for workspace storage (default: `/var/rally/workspaces` — use a local path on macOS) |
| `RALLY_API_URL` | URL AE containers use to reach Rally (default: `http://host.docker.internal:8432`) |

### Slack (for AE communication)

| Variable | Description |
|---|---|
| `SLACK_CLIENT_ID` | Slack app client ID (from api.slack.com) |
| `SLACK_CLIENT_SECRET` | Slack app client secret |
| `SLACK_BOT_TOKEN` | Bot token (alternative to OAuth — set directly or connect via Settings) |
| `SLACK_SIGNING_SECRET` | For verifying inbound Slack webhooks |

### Optional Integrations

| Variable | Description |
|---|---|
| `ANTHROPIC_API_KEY` | Anthropic API key (for Claude models) |
| `OPENAI_API_KEY` | OpenAI API key (for GPT models) |
| `GITHUB_TOKEN` | GitHub personal access token |
| `GOOGLE_CLIENT_ID` | Google OAuth2 client ID |
| `GOOGLE_CLIENT_SECRET` | Google OAuth2 client secret |
| `FIGMA_PERSONAL_ACCESS_TOKEN` | Figma API token |

---

## Taskfile Commands

| Task | Description |
|---|---|
| `task server` | Start the server (no hot reload) |
| `task dev` | Start with hot reload (templ + tailwind) |
| `task build` | Build the server binary |
| `task test` | Run all tests |
| `task gen` | Regenerate templ Go files |
| `task sqlc` | Regenerate DB access layer from SQL queries |
| `task db-start` | Start PostgreSQL container |
| `task db-stop` | Stop PostgreSQL container |
| `task db-migrate` | Apply DB and River migrations |
| `task db-reset` | Destroy and recreate database |
| `task ae-build` | Build the AE agent Docker image |
| `task ae-list` | List running AE containers |
| `task ae-logs -- {name}` | Tail logs for an AE container |
| `task ae-stop-all` | Stop all AE containers |
| `task nuke` | Delete ALL data and containers (destructive) |

---

## AE Tools

Every AE has access to these tools. The LLM decides which to use based on context.

### Local Tools (execute inside the container)

| Tool | Description |
|---|---|
| `Bash` | Execute shell commands |
| `Read` | Read files with line numbers, offset/limit |
| `Write` | Create or overwrite files |
| `Edit` | String-replacement edits with staleness guard |
| `Grep` | Regex search across workspace files (uses ripgrep) |
| `Glob` | Find files by pattern |
| `ListFiles` | List directory contents |
| `BrowserNavigate` | Navigate URLs via Playwright/Chromium |

### Work Tracking

| Tool | Description |
|---|---|
| `BacklogList` | List work items by status |
| `BacklogAdd` | Create a work item |
| `BacklogUpdate` | Update status or add notes |
| `UpdateTask` | Mark assigned tasks as done/in_progress |

### Collaboration

| Tool | Description |
|---|---|
| `ProposeHire` | Propose a new team member (human approval required) |
| `ListTeam` | See current team roster |
| `Delegate` | Assign work to another AE by role |
| `Escalate` | Flag an issue for human attention (posts to Slack) |
| `SendMessage` | Direct message another AE |
| `SlackSend` | Post to a Slack channel |

### Remote Tools (via Rally gateway)

GitHub, Google Workspace (Gmail, Docs, Drive, Calendar), Figma, and more — discovered dynamically at startup from the server's tool registry.

---

## Workspaces

Each AE gets two filesystem mounts:

| Mount | Container Path | Visibility |
|---|---|---|
| Shared workspace | `/workspace` | All AEs in the company |
| Private scratch | `/home/ae/scratch` | Only that AE |

The shared workspace is seeded with `README.md` and `POLICIES.md` on first hire. AEs write session state to their private scratch directory for continuity across heartbeat cycles.

---

## Web UI

| Page | Purpose |
|---|---|
| `/` | Dashboard — agent cards, team activity, recent logs |
| `/setup` | First-run wizard / Settings page (integrations, danger zone) |
| `/companies/{id}` | Company detail — org chart, policy editor, proposed hires with approve/reject |
| `/agents` | Agent list with last-active timestamps |
| `/agents/{id}` | Agent detail — current work, soul, memory, tool logs |
| `/chat` | Chat with any AE or Rally orchestrator |
| `/tasks` | Task management |
| `/credentials` | Manage OAuth tokens and API keys |
| `/logs` | Tool execution log viewer |
| `/slack/install` | Connect Slack workspace via OAuth |

---

## Slack Integration

Rally AEs communicate with your team via Slack. To set up:

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app
2. Add the required bot scopes (see `.env.example` for the full list)
3. Set `SLACK_CLIENT_ID` and `SLACK_CLIENT_SECRET` in `.env`
4. Start Rally and visit **Settings** → click **Connect** next to Slack
5. Authorize the app in your workspace — Rally stores the bot token automatically

AEs post messages as `[AEName] message` in channels. They receive Slack events via the `/slack/events` webhook endpoint.

---

## Development

```bash
# After changing .templ files
task gen

# After changing SQL queries in internal/db/queries/
task sqlc

# Run tests
task test

# Full reset (nuke all data + containers)
task nuke
```

Database access uses sqlc-generated queries (`internal/db/dao/`). Add new queries to `internal/db/queries/*.sql` and run `task sqlc` to generate.

---

## LLM Providers

Rally uses the **OpenAI** and **Anthropic** Go SDKs with native tool-use support. The default provider is Greenthread (OpenAI-compatible, uses streaming for tool-call support on vLLM).

Models and providers are configured in `config/models.yaml` and `config/providers.yaml`. Each AE's model can be changed dynamically via the employee config in the database — takes effect on the next heartbeat cycle.
