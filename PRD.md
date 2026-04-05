# rally — Product Requirements Document

## 1. Vision

Rally is a system that enables users to:

* define a company
* hire artificial employees (AEs)
* integrate human employees
* connect to real tools (Slack, GitHub, browser, GSuite, etc.)
* operate a fully functional, persistent, remote-first organization

The output of Rally is not a chatbot.

The output is:

> **a living organization composed of humans and artificial employees working together**

Our primary driver is that `rally` itself becomes our primary company so we should be able to work backwards to understand everything needed.

We should be able to setup rally for rally, create our org chart, set the company mission - point it at the repo for Rally itself and do research, iterate on the product, restart our rally offering to improve it based on our AEs own design and research using our own learnings and be able to sell our own product to companies as well as send invoices. We still want things like Stripe or Airwallex to be setup for us BUT we should ask for that information as needed (like provider, secret keys and pointers to the API docs) or just regular PDF invoices but ask for the company bank accounts (THAT SHOULD BE A CONFIG IN FACT!)

## IMPORTANT **If we can't ideate, build, ship, iterate, market and sell our own product, what the fuck are we doing trying to sell it to others! - until we get to that point, the rally creator and human, Nick AKA 17twenty is here to help and AEs can call upon him**

---

## 2. Core Experience

### User Journey Start

1. User opens Rally (web UI)
2. User defines:

   * company name
   * mission
   * initial humans (optional)
3. User clicks:
   → “Build my team”
4. Rally names each AEs i.e. `Sarah (Design)` and records it accordingly in their soul.md and company records
  * The AEs would join the team, introduce themselves and be ready for work.
  

Rally:

* designs org structure
* hires AEs (CEO, CTO, etc.)
* generates identities (soul.md)
* provisions tools
* connects to Slack workspace
* deploys agents

Then:

* Slack becomes active with AEs
* AEs introduce themselves
* AEs begin planning and executing work
* User can:
  * talk to Rally
  * talk directly to AEs
  * observe everything

---

## 3. System Principles

### 3.1 Organizational > Agent-centric

System is designed around:

* roles
* hierarchy
* responsibilities
* accountability

Not just individual agents.

---

### 3.2 Memory Privacy + Knowledge Sharing

* memory = private (per AE)
* knowledge = shared (curated)
* communication = explicit

---

### 3.3 Observable by Default

Every action:

* logged
* attributable
* inspectable

---

### 3.4 Replaceable Cognition

* AEs ≠ models
* models are configurable + swappable

---

### 3.5 Real Tools, Not Simulations

* Slack (real)
* GitHub (real)
* browser (real)
* filesystem (real)

---

## 4. System Architecture

## 4.1 High-Level

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

---

## 5. Major Components

---

## 5.1 Rally Core (Go + templui)

Responsibilities:

* UI (company creation, monitoring)
* API layer
* orchestration control plane
* configuration management (YAML/JSON ingestion)

---

## 5.2 Organization Manager

Handles:

* org structure
* reporting lines
* roles
* hiring logic

### Inputs:

* company goals
* team composition
* human employees

### Outputs:

* AE definitions
* hierarchy

---

## 5.3 Agent Runtime System

Each AE:

* runs as independent worker
* has:

  * config
  * memory
  * tools
  * model routing

Execution driven by:

* heartbeat jobs (River)

---

## 5.4 LLM Routing Layer

Handles:

* model selection
* provider routing
* API normalization

Supports:

* OpenAI-compatible APIs
* Anthropic-compatible APIs

---

## 5.5 Tool Gateway

Central interface for:

* Slack
* GitHub
* shell
* browser
* desktop (future)

Ensures:

* permission enforcement
* logging
* consistency

---

## 5.6 Knowledgebase

Stores:

* company facts
* employee registry
* decisions
* docs
* playbooks

Supports:

* retrieval
* updates (approval-based)

---

## 5.7 Memory System

Per-AE:

* episodic memory
* reflections
* heuristics

Not shared directly.

---

## 5.8 Queue System (River)

Used for:

* heartbeat scheduling
* retries
* async tool execution
* delayed workflows

---

## 6. Data Architecture

---

## 6.1 Config-Driven System

All core definitions via YAML/JSON:

* employees
* models
* providers
* organization

---

## 6.2 Employee Config

```yaml
employee:
  id: cto-ae
  role: CTO
  reports_to: ceo-ae

identity:
  soul_file: soul.md

cognition:
  default_model_ref: internal-gpt-oss-120b
  routing:
    planning: internal-gpt-oss-120b

runtime:
  heartbeat_seconds: 300

tools:
  slack: true
  github: true
```

---

## 6.3 Model + Provider Separation

* model registry → logical models
* provider config → endpoint/auth/API style

---

## 7. Agent Lifecycle

---

## 7.1 Hiring

Rally:

1. defines role
2. generates soul.md
3. assigns tools
4. assigns model routing
5. provisions Slack identity
6. announces hire

---

## 7.2 Operation (Heartbeat Loop)

```text
observe → plan → act → evaluate → store
```

Sources:

* Slack
* tasks
* memory
* KB

---

## 7.3 Communication

Slack-first:

* channels = teams
* threads = tasks
* messages = coordination

---

## 8. Slack Integration

---

## 8.1 Responsibilities

* create/join workspace
* create users (AEs)
* join channels
* post messages
* read threads

---

## 8.2 Message Types

* Intent
* Update
* Blocker
* Request

Structured but human-readable.

---

## 9. Tooling

---

## 9.1 Required (v1)

* Slack
* GitHub
* shell
* browser (Playwright)

---

## 9.2 Future

* desktop environments (Xvfb)
* email/calendar
* internal APIs

---

## 10. Desktop Workstations (Advanced Capability)

---

## 10.1 Concept

Per-AE container:

* Linux environment
* Xvfb display
* window manager
* automation tools

---

## 10.2 Philosophy

* not required for most work
* accessibility-first > vision-first
* screenshots = fallback

---

## 11. Storage (Postgres)

Tables:

* employees
* employee_configs
* org_structure
* memory_events
* knowledgebase
* tasks
* slack_events
* tool_logs
* model_registry
* provider_registry

---

## 12. Observability

Everything logged:

* prompts
* tool calls
* decisions
* Slack messages

Each with:

* employee_id
* trace_id
* task_id

---

## 13. Governance

Rules:

* production actions → human approval
* merges → CTO or human
* KB updates → approval required

---

## 14. Human Integration

Humans defined in KB:

```json
{
  "name": "Alice",
  "role": "CEO",
  "type": "human",
  "specialties": ["strategy"]
}
```

Agents:

* can defer
* can escalate
* can collaborate

---

## 15. UX (templui)

User can:

* create company
* define team
* monitor agents
* inspect logs
* override decisions
* chat with Rally

---

## 16. Initial System Behavior

Upon creation:

1. Rally generates org
2. hires AEs
3. connects Slack
4. AEs introduce themselves
5. AEs begin planning work

---

## 17. Success Criteria

* full org appears in Slack
* AEs coordinate autonomously
* tasks executed without prompting
* memory persists
* KB evolves
* humans can collaborate naturally
* Can use the playwright tooling iteratively to accomplish goals - write/form-submission etc.
* Ability to point all agents at a CompanyDocs.md file that proscribes policy and dos/donts

---

Yeah — I think that’s the right instinct.

This isn’t just “more backlog items.”
It’s a **new capability layer + validation track** for Rally.

Treating it as a **named chapter** does two important things:

1. Signals to the team this is a **strategic expansion**, not incremental work
2. Gives Ralph loop something **coherent to iterate on**, not scattered tickets

---

## **Rally Workspace & Agent Capability Test**

---

## 1. Purpose

Define and validate the next major evolution of Rally:

> Transition from “agent orchestration” → **full company operating environment**

This phase introduces:

* a **shared company workspace (artifacts + knowledge)**
* **real-world agent workflows** (Design, SDR, Marketing)
* **external system integration via MCP / APIs**
* **measurable business outcomes**

---

## 2. Core Hypothesis

If Rally provides:

* a persistent shared workspace
* structured agent roles
* real-world tool integrations

Then:

> AEs can independently produce meaningful business outputs
> (designs, leads, campaigns) with minimal human intervention.

---

## 3. Rally Workspace (New System Primitive)

### 3.1 Definition

Rally Workspace is a **shared, persistent, versioned artifact system** accessible to:

* all AEs
* all human employees
* all tools

---

### 3.2 Capabilities

* File storage (docs, images, JSON, assets)
* Version history
* Comments / feedback threads
* Ownership & permissions
* Approval workflows
* Structured metadata
* Search & retrieval

---

### 3.3 Access Modes

#### 1. UI (templui)

* browse files
* review outputs
* leave feedback
* approve/reject

#### 2. Filesystem (agent containers)

* mounted volume:
  `/workspace/...`

#### 3. MCP Resources

* URI-based access:

```text
workspace://company/...
workspace://projects/...
workspace://design/...
workspace://campaigns/...
```

---

### 3.4 Design Principle

> Workspace is the **source of truth for all artifacts**, not Slack

Slack = coordination
Workspace = output

---

## 4. Agent Capability Tests (Primary Focus)

---

# 🎯 4.1 AE Designer (Figma MCP)

### Objective

Enable an AE to:

* create designs
* iterate on feedback
* produce usable UI assets

---

### Approach

* integrate with Figma MCP server
* use:

  * structured tools
  * reusable skills
* avoid browser automation where possible

---

### Flow

```text
Brief (Workspace)
→ AE Designer
→ Figma MCP actions
→ Design output
→ Export to Workspace
→ Feedback loop
→ Iteration
```

---

### Requirements

* Figma MCP client integration
* skill-based workflows (design system aware)
* Workspace ↔ Figma sync

---

### Success Criteria

* AE produces usable UI design from brief
* Iterates based on feedback
* Outputs assets to Workspace

---

---

# 📈 4.2 AE SDR (Outbound Engine)

### Objective

Enable an AE to:

* identify leads
* enrich contacts
* generate outreach
* run campaigns
* track responses

---

### Approach (API-first)

* Google search → companies
* enrichment APIs → contacts
* email APIs → outreach
* reply classification → feedback loop

---

### Flow

```text
Target ICP (Workspace)
→ Lead discovery
→ Contact enrichment
→ Outreach generation
→ Email send
→ Reply ingestion
→ Iteration
```

---

### Constraints

* LinkedIn = optional, not core, should be given an account login if needed.
* cookie/session usage = controlled + scoped

---

### Success Criteria

* valid leads generated
* emails sent successfully
* replies received
* messaging improves over time

---

---

# 📣 4.3 AE CMO (Campaign Engine)

### Objective

Enable an AE to:

* generate marketing assets
* run experiments
* analyze performance

---

### Approach

* Workspace as asset hub
* APIs for publishing (where possible)
* analytics ingestion

---
# 📣 4.4 AE Developer (The builder and shipper of tech and infra wirerupper)

### Objective

AE Developer Responsibilities
1. Environment Setup
provision container
install tools
clone repos
configure runtime
2. Credential Acquisition

Flow:

AE requests credentials
→ Rally notifies human via Slack DM
→ human provides token / OAuth grant
→ stored in secure vault
→ AE gains scoped access


```text
Development brief
→ PRD review and backlog creation
→ Store in Workspace
→ Test development
→ Review/approval
→ Iterate
```

3. GitHub Identity

Options:

shared bot account (v1)
per-AE GitHub accounts (v2)
4. Workspace Setup
configure Google Workspace access
setup CLI
verify:
- email
- drive
- docs

### Flow

```text
Development brief
→ PRD review and backlog creation
→ Store in Workspace
→ Test development
→ Review/approval
→ Iterate
```

---

### Channels

* email
* landing pages
* social (API-permitted only)

---

### Success Criteria

* assets created
* campaigns executed
* measurable engagement

---

## 5. Access Provider System

---

### 5.1 Purpose

Standardize how AEs access external systems.

---

### 5.2 Types

#### API Access (preferred)

```yaml
type: api
provider: sendgrid
```

#### OAuth

```yaml
type: oauth
provider: figma
```

#### Delegated Session (controlled)

```yaml
type: browser_session
scope: linkedin.com
```

---

### 5.3 Rules

* no raw credential exposure to agents
* all access mediated via Rally
* full audit logging required

---

## 6. Desktop-Capable Agents

---

### Purpose

Support tools that require UI interaction.

---

### Stack

* container per AE
* Xvfb display
* window manager
* automation tools
* optional remote viewer

---

### Principle

> Desktop is an enhancement, not default interface

---

## 7. Evaluation Framework

---

### 7.1 Metrics

#### System-level

* task completion rate
* agent coordination quality
* error recovery success

#### Business-level

* leads generated
* replies received
* campaigns executed
* assets produced

---

### 7.2 Qualitative

* output usefulness
* human trust
* clarity of communication
* iteration quality

---

## 9. Phasing Strategy

---

### Phase 1

* Workspace (basic)
* AE SDR (API-first)
* minimal Designer (read-only or partial)

---

### Phase 2

* Figma MCP integration
* CMO campaigns
* approval workflows

---

### Phase 3

* desktop agents
* advanced coordination
* optimization loops

---

## Digital Identity & Accounts”
Each AE must have:

- Slack identity
- optional GitHub identity
- Google Workspace identity
- email address
- access provider bindings

Rally is responsible for:
- provisioning
- authentication
- credential storage
- access auditing
Section: “Workspace Integration Layer”
Rally integrates with Google Workspace via:

- Google Workspace CLI (primary interface)
- OAuth-based authentication per AE
- structured command execution via tool gateway

This enables AEs to:
- send/receive email
- manage files
- create documents
- schedule events
Backlog Additions
- [ ] Implement Google Workspace org onboarding flow
- [ ] Add per-AE Google identity provisioning
- [ ] Integrate Google Workspace CLI into agent containers
- [ ] Build OAuth credential flow + vault storage
- [ ] Add Gmail-based communication capability
- [ ] Add Drive sync with Rally Workspace
- [ ] Add email ingestion + classification system

Use case - The SDR needs to be tasked with finding meetings with "CEOs in Sydney in the SAAS industry who'd be keen for lunch" - go do some research, find leads and email them - offering to setup a time etc and collaborate with the task setter on the goal - they would use their local container environment mounted to the workspace, grab the files, process them, and then manipulate them, tracking process and followsup etc as they go. Note which need following up, which don't, offer to book meetings in the calendar or work with the COO / asker for time/dates etc. They should be able to structure their information and read through it to provide stats.


## Technical Notes

- This is a Golang project, refer to https://templui.io/llms.txt for details on how to run and install Templui which is used for the web UI
- We have already performed a `templui init` and `go mod init` and `templui add "*"` to install all available components for our WebUI.
- When receiving a Slack message - rally should review who needs to hear it and optionally wake up OR 'heartbeat' the AE - worth reviewing the latest SLACK_NOTES.md for changes
- For the Go side, SQLc is configured, we have a taskfile that should be used / iterated on and containers containerised docker setup etc.
- Our app should run natively for testing if using `task dev` or similar, db-start/db-stop and db-migrate tasks control our database only and our app should be able to run locally and take in the POSTGRES_USER, POSTGRES_DB, POSTGRES_PASSWORD etc.
- https://github.com/googleworkspace/cli is a core part of this project to allow AEs to interact with their own GMail accounts (or access a shared one sending emails using an alias like nick+cto@curiola.com) - we should have a suitable setup, dependency requirement and setup as well as usage instructions for our agent (including the ability to complain via Slack if they dont have it and need it!)
- In the parent folder, you can find `slackme` - this has some Slack connectivity tools that might help - there's a SLACK_NOTES.md as well that explains more of how Rally's slack integration should work.
- Verify the README.md and Taskfile.yml are in sync, are correct and update any tasks that aren't correct.
- Never implement brittle integrations - prefer APIs/OAuth2/Tokens/MCP (including setup info) as needed and recommend better tools to replace bad tooling. Fall back to playwright as needed - YOU ARE A REAL(ish) PERSON.
- Verify our 'greenthread' providers.yaml config is working and providing an openAI compatible endpoint and ensure models.yaml is setup to use it. We should default to our greenthread endpoint for all employee models during build
