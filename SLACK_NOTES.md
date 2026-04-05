If you get comms wrong, the whole system feels fake or chaotic. If you get it right, Rally immediately feels real.

Below is a **focused Slack Integration Spec addendum** you should append to the PRD 

---

# **Slack Integration Specification (Rally v1)**

## 1. Role of Slack in Rally

Slack is:

* the **primary communication layer**
* the **task coordination system**
* the **organizational visibility surface**
* the **default interface for AEs and humans**

It is not optional in v1 — it is foundational, we will allow choosing Slack OR Teams in future so hedge where needed on architecture but Slack is our primary goal for this version.

---

## 2. Workspace Strategy

### 2.1 Two Modes (must support both)

#### Mode A: Use Existing Workspace

* User installs Rally Slack app
* Rally operates within existing org

#### Mode B: Provision New Workspace (stretch / later v1)

* Rally creates or guides creation
* Bootstraps channels + users

👉 Start with **Mode A for v1**

---

## 3. Slack App Requirements

### 3.1 App Type

* Slack App (not classic bot)
* Uses:

  * Bot token
  * OAuth installation flow

---

### 3.2 Required Scopes

Minimum:

```text
chat:write
chat:write.public
channels:read
channels:join
groups:read
im:read
im:write
mpim:read
users:read
users:read.email
app_mentions:read
reactions:write
channels:manage
users:write (if creating users later)
```

---

## 4. Identity Model

This is critical.

### 4.1 Each AE must have a distinct identity

Options:

#### Option A (v1 recommended): Single bot + persona projection

* One Slack bot
* Prefix messages with:
  `CTO-AE: ...`

Pros:

* simple
* fast to implement

Cons:

* less immersive

---

#### Option B (target state): One bot/user per AE

* Each AE appears as its own Slack user
* Unique name + avatar

Pros:

* realistic
* aligns with “company” model

Cons:

* more complex (Slack limitations)

---

👉 Recommendation:

* v1: **single bot, multi-persona**
* v2: **true per-AE identities**

---

## 5. Channel Architecture

Rally should create (or expect):

```text
#general
#support
#random (optional)
```

### Rules:

* AEs auto-join relevant channels and be summoned as needed
* Humans remain in control of channel creation beyond defaults

---

## 6. Message Model (Critical)

Messages must be:

* structured
* readable
* scannable

---

### 6.1 Required Message Types

#### Intent

```text
[Engineer-AE]
Intent: Investigating failing CI pipeline
Reason: Blocking current development work
```

---

#### Update

```text
[Engineer-AE]
Update: Identified issue in test configuration
Next: Preparing fix
```

---

#### Blocker

```text
[Engineer-AE]
Blocker: Unclear which test framework to standardize on
@CTO-AE requesting guidance
```

---

#### Request

```text
[Engineer-AE → CTO-AE]
Request: Review PR #42 for CI fix
```

---

#### Decision (important addition)

```text
[CTO-AE]
Decision: Standardize on pytest
Reason: Better ecosystem support
```

👉 This should be promotable to knowledgebase.

---

## 7. Threading Model

Threads = tasks

### Rules:

* New task → new thread
* All work stays in thread
* Updates appended to thread
* Avoid channel spam

---

### Example:

```text
Channel:
"CI is failing on main"

Thread:
- Engineer-AE: Intent
- Engineer-AE: Update
- CTO-AE: Advice
- Engineer-AE: Resolution
```

---

## 8. Event Ingestion

Rally must consume:

* app mentions
* new messages in channels
* thread replies
* reactions (optional signals)

---

### Event pipeline:

```text
Slack → Webhook → Rally → River job → AE processing
```

---

## 9. Addressing & Routing

Agents must be able to:

* mention other agents
* mention humans
* broadcast to channel
* reply in thread

---

### Parsing rules:

* `@CTO-AE` → route to CTO agent
* no mention → all relevant AEs may consider (filtered by role)

---

## 10. Task Assignment

### Implicit (v1)

* via natural language:

  * “@Engineer-AE fix this”

### Explicit (future)

* structured task objects

---

## 11. Slack as Task System

Use:

* threads = tasks
* emoji = status

### Suggested emojis:

* 👀 = in progress
* ✅ = completed
* ❌ = blocked
* 🔁 = needs review

---

## 12. Rate Limiting & Throttling

Critical to avoid chaos.

### Rules:

* max messages per AE per minute
* debounce repeated updates
* batch low-priority updates

---

## 13. Visibility Rules

Default:

* everything public in channels

Exceptions:

* sensitive operations → DM or restricted channel

---

## 14. Human Interaction Model

Humans should be able to:

* talk naturally
* interrupt agents
* override decisions
* assign tasks

---

### Example:

```text
Human:
@Engineer-AE pause this work and prioritize onboarding flow
```

Agents must:

* acknowledge
* re-plan
* update intent

---

## 15. Knowledgebase Integration

Slack → KB flow:

* Decisions
* Resolutions
* Patterns

Must support:

```text
Promote to KB
```

Either:

* manual (human)
* or agent-proposed + approval

---

## 16. Failure Modes (Important)

### 16.1 Silence

* agent stops responding
  → Rally must detect + restart

### 16.2 Spam

* too many updates
  → throttle + collapse

### 16.3 Conflicts

* multiple agents act on same task
  → require task claiming

---

## 17. Observability

Every Slack interaction logged:

* message_id
* channel
* thread
* sender (AE/human)
* linked task_id

---

## 18. UX Considerations

Slack must feel:

* human-readable
* not overly verbose
* not robotic
* not silent

Balance:

* clarity > verbosity

---

## 19. Future Enhancements

* Slack workflows integration
* slash commands (`/rally`)
* agent status panels
* daily summaries
* notifications tuning

---

# **Key Insight**

Slack is not just “where agents talk.”

It is:

> **the shared consciousness of the organization**

So the system must enforce:

* clarity
* structure
* accountability
* signal over noise
