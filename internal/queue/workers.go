package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/17twenty/rally/internal/container"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/hiring"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/org"
	"github.com/17twenty/rally/internal/slack"
	"github.com/17twenty/rally/internal/tools"
	"github.com/17twenty/rally/internal/workspace"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// HeartbeatWorker monitors AE container health. The actual agent loop runs
// inside the container; this worker checks the container is alive and restarts
// it if needed.
type HeartbeatWorker struct {
	river.WorkerDefaults[HeartbeatJobArgs]
	DB               *pgxpool.Pool
	LLMRouter        *llm.Router
	ContainerManager *container.Manager
}

func (w *HeartbeatWorker) Work(ctx context.Context, job *river.Job[HeartbeatJobArgs]) error {
	slog.Info("heartbeat_health_check", "employee_id", job.Args.EmployeeID, "company_id", job.Args.CompanyID)

	_, cfg, err := loadEmployee(ctx, w.DB, job.Args.EmployeeID)
	if err != nil {
		return fmt.Errorf("load employee %s: %w", job.Args.EmployeeID, err)
	}

	// Check container health if container manager is available
	if w.ContainerManager != nil {
		var containerName string
		scanErr := w.DB.QueryRow(ctx,
			`SELECT COALESCE(container_id,'') FROM employees WHERE id = $1`, job.Args.EmployeeID,
		).Scan(&containerName)

		if scanErr == nil && containerName != "" {
			info, inspectErr := w.ContainerManager.Inspect(ctx, containerName)
			if inspectErr != nil || info.State != "running" {
				slog.Warn("heartbeat: container not running, restarting",
					"employee_id", job.Args.EmployeeID,
					"state", func() string {
						if info != nil {
							return info.State
						}
						return "unknown"
					}(),
				)
				if restartErr := w.ContainerManager.Restart(ctx, containerName); restartErr != nil {
					slog.Error("heartbeat: restart failed", "employee_id", job.Args.EmployeeID, "err", restartErr)
					_, _ = w.DB.Exec(ctx,
						`UPDATE employees SET container_status = 'error' WHERE id = $1`, job.Args.EmployeeID)
				} else {
					_, _ = w.DB.Exec(ctx,
						`UPDATE employees SET container_status = 'running' WHERE id = $1`, job.Args.EmployeeID)
				}
			}
		}
	}

	// Re-enqueue next health check
	delay := time.Duration(cfg.Runtime.HeartbeatSeconds) * time.Second
	if delay <= 0 {
		delay = 5 * time.Minute
	}
	if Client != nil {
		_, _ = Client.Insert(ctx, HeartbeatJobArgs{
			EmployeeID: job.Args.EmployeeID,
			CompanyID:  job.Args.CompanyID,
		}, &river.InsertOpts{
			ScheduledAt: time.Now().Add(delay),
		})
	}
	return nil
}

// loadEmployee fetches an employee and its config from the database.
func loadEmployee(ctx context.Context, db *pgxpool.Pool, employeeID string) (domain.Employee, domain.EmployeeConfig, error) {
	var emp domain.Employee
	err := db.QueryRow(ctx, `
		SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''),
		       type, status, COALESCE(slack_user_id,''), created_at
		FROM employees WHERE id = $1
	`, employeeID).Scan(
		&emp.ID, &emp.CompanyID, &emp.Name, &emp.Role, &emp.Specialties,
		&emp.Type, &emp.Status, &emp.SlackUserID, &emp.CreatedAt,
	)
	if err != nil {
		return domain.Employee{}, domain.EmployeeConfig{}, fmt.Errorf("query employee: %w", err)
	}

	var configJSON []byte
	err = db.QueryRow(ctx, `
		SELECT config FROM employee_configs WHERE employee_id = $1 LIMIT 1
	`, employeeID).Scan(&configJSON)
	if err != nil {
		return domain.Employee{}, domain.EmployeeConfig{}, fmt.Errorf("query employee_config: %w", err)
	}

	var cfg domain.EmployeeConfig
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return domain.Employee{}, domain.EmployeeConfig{}, fmt.Errorf("unmarshal config: %w", err)
	}

	return emp, cfg, nil
}

// SlackEventWorker processes incoming Slack events.
type SlackEventWorker struct {
	river.WorkerDefaults[SlackEventJobArgs]
	DB          *pgxpool.Pool
	RiverClient *river.Client[pgx.Tx]
}

func (w *SlackEventWorker) Work(ctx context.Context, job *river.Job[SlackEventJobArgs]) error {
	slog.Info("slack_event", "slack_event_id", job.Args.SlackEventID, "company_id", job.Args.CompanyID)

	rc := w.RiverClient
	if rc == nil {
		rc = Client
	}
	if rc == nil {
		return fmt.Errorf("no river client available")
	}

	// 1. Fetch the slack_event row from DB.
	var (
		eventType string
		channel   *string
		userID    *string
		threadTS  *string
		payload   []byte
	)
	err := w.DB.QueryRow(ctx, `
		SELECT event_type, channel, user_id, thread_ts, payload
		FROM slack_events WHERE id = $1
	`, job.Args.SlackEventID).Scan(&eventType, &channel, &userID, &threadTS, &payload)
	if err != nil {
		return fmt.Errorf("fetch slack_event %s: %w", job.Args.SlackEventID, err)
	}

	// 2. Parse payload to extract text.
	var payloadMap map[string]any
	if err := json.Unmarshal(payload, &payloadMap); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	text, _ := payloadMap["text"].(string)

	// 3. Fetch all AE employees for the company.
	rows, err := w.DB.Query(ctx, `
		SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''),
		       type, status, COALESCE(slack_user_id,''), created_at
		FROM employees WHERE company_id = $1 AND type = 'ae' AND status = 'active'
	`, job.Args.CompanyID)
	if err != nil {
		return fmt.Errorf("list employees: %w", err)
	}
	defer rows.Close()
	var employees []domain.Employee
	for rows.Next() {
		var emp domain.Employee
		if err := rows.Scan(&emp.ID, &emp.CompanyID, &emp.Name, &emp.Role,
			&emp.Specialties, &emp.Type, &emp.Status, &emp.SlackUserID, &emp.CreatedAt); err != nil {
			return fmt.Errorf("scan employee: %w", err)
		}
		employees = append(employees, emp)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows error: %w", err)
	}

	// 4. Determine target AEs using routing logic.
	var targets []domain.Employee

	if eventType == "app_mention" {
		// Always wake up all active AEs for app_mention events.
		targets = employees
	} else {
		// Try @mention routing first.
		mentions := slack.ParseMentions(text)
		if len(mentions) > 0 {
			targets = slack.MatchAEsByRole(employees, mentions)
		}

		// Try name-based routing — "Hey Drew" matches Drew.
		if len(targets) == 0 {
			targets = slack.MatchAEsByName(employees, text)
		}

		// Channel-based routing if no name or mention matches.
		if len(targets) == 0 && channel != nil {
			channelName := strings.TrimPrefix(*channel, "#")
			roles := slack.ChannelToRoles(channelName)
			targets = slack.MatchAEsByRole(employees, roles)
		}

		// Fallback: wake up CEO-AE.
		if len(targets) == 0 {
			targets = slack.MatchAEsByRole(employees, []string{"CEO"})
		}
	}

	// 5. Enqueue HeartbeatJobArgs for each target AE.
	targetIDs := make([]string, 0, len(targets))
	for _, emp := range targets {
		// Insert immediate heartbeat with NO unique constraint.
		// The regular heartbeat scheduler uses dedup, but Slack-triggered
		// wakes must bypass it to ensure fast response times.
		_, err := rc.Insert(ctx, HeartbeatJobArgs{
			EmployeeID: emp.ID,
			CompanyID:  job.Args.CompanyID,
		}, &river.InsertOpts{
			ScheduledAt: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("enqueue heartbeat for %s: %w", emp.ID, err)
		}
		targetIDs = append(targetIDs, emp.ID)
	}

	// 6. Mark slack_event as processed.
	if _, err := w.DB.Exec(ctx, `UPDATE slack_events SET processed_at = NOW() WHERE id = $1`, job.Args.SlackEventID); err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}

	// 7. Log routing decision.
	slog.Info("slack_event_routed",
		"event_id", job.Args.SlackEventID,
		"event_type", eventType,
		"target_aes", targetIDs,
	)

	return nil
}

// ToolExecutionWorker processes AE tool calls.
type ToolExecutionWorker struct {
	river.WorkerDefaults[ToolExecutionJobArgs]
}

func (w *ToolExecutionWorker) Work(ctx context.Context, job *river.Job[ToolExecutionJobArgs]) error {
	slog.Info("tool_execution", "employee_id", job.Args.EmployeeID, "tool", job.Args.Tool, "action", job.Args.Action, "trace_id", job.Args.TraceID)
	return nil
}

// HiringWorker processes new AE hiring requests.
type HiringWorker struct {
	river.WorkerDefaults[HiringJobArgs]
	DB               *pgxpool.Pool
	LLMRouter        *llm.Router
	ContainerManager *container.Manager
}

func (w *HiringWorker) Work(ctx context.Context, job *river.Job[HiringJobArgs]) error {
	slog.Info("hiring", "role", job.Args.Role, "company_id", job.Args.CompanyID, "reports_to", job.Args.ReportsTo)

	// Load company from DB
	var company domain.Company
	err := w.DB.QueryRow(ctx,
		`SELECT id, name, COALESCE(mission,''), status, created_at FROM companies WHERE id = $1`,
		job.Args.CompanyID,
	).Scan(&company.ID, &company.Name, &company.Mission, &company.Status, &company.CreatedAt)
	if err != nil {
		return fmt.Errorf("load company %s: %w", job.Args.CompanyID, err)
	}

	// Construct Slack client from env if available
	var slackClient *slack.SlackClient
	if token := os.Getenv("SLACK_BOT_TOKEN"); token != "" {
		slackClient = slack.NewClient(token)
	}

	hirer := &hiring.Hirer{
		DB:               w.DB,
		LLMRouter:        w.LLMRouter,
		SlackClient:      slackClient,
		ContainerManager: w.ContainerManager,
		OnHired: func(ctx context.Context, employeeID, companyID string) {
			if Client != nil {
				_, _ = Client.Insert(ctx, HeartbeatJobArgs{
					EmployeeID: employeeID,
					CompanyID:  companyID,
				}, nil)
			}
		},
	}

	plan := org.PlannedRole{
		ID:         job.Args.PlanRoleID,
		Role:       job.Args.Role,
		Department: job.Args.Department,
		ReportsTo:  job.Args.ReportsTo,
	}

	_, err = hirer.HireAE(ctx, job.Args.CompanyID, plan, company)
	return err
}

// CampaignDraftWorker drafts a marketing campaign using the LLM and saves it as a workspace file.
type CampaignDraftWorker struct {
	river.WorkerDefaults[CampaignDraftJobArgs]
	DB             *pgxpool.Pool
	LLMRouter      *llm.Router
	WorkspaceStore *workspace.WorkspaceStore
	SlackClient    *slack.SlackClient
}

func (w *CampaignDraftWorker) Work(ctx context.Context, job *river.Job[CampaignDraftJobArgs]) error {
	slog.Info("campaign_draft", "employee_id", job.Args.EmployeeID, "company_id", job.Args.CompanyID)

	content, err := w.LLMRouter.Complete(ctx, w.LLMRouter.DefaultModel(),
		"You are a CMO AI. Generate concise marketing campaign copy.",
		fmt.Sprintf("Write a campaign based on this brief: %s\n\nFormat:\nSubject: ...\nBody: ...\nCTA: ...", job.Args.Brief),
		1000)
	if err != nil {
		slog.Warn("campaign_draft: llm error, using placeholder", "error", err)
		content = fmt.Sprintf("# Campaign\n\nBrief: %s\n\nSubject: Campaign Draft\nBody: Content pending.\nCTA: Learn More", job.Args.Brief)
	}

	// Extract subject line as title.
	title := "Campaign Draft"
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "Subject:") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "Subject:"))
			break
		}
	}

	timestamp := time.Now().UnixMilli()
	fileID := fmt.Sprintf("cmo-campaign-%d", timestamp)
	path := fmt.Sprintf("marketing/campaigns/%d.md", timestamp)

	if w.WorkspaceStore != nil {
		file := workspace.WorkspaceFile{
			ID:        fileID,
			CompanyID: job.Args.CompanyID,
			Path:      path,
			Title:     title,
			Content:   content,
			MimeType:  "text/markdown",
			CreatedBy: job.Args.EmployeeID,
		}
		if saveErr := w.WorkspaceStore.SaveFile(ctx, file); saveErr != nil {
			slog.Warn("campaign_draft: save file error", "error", saveErr)
		}
	}

	if w.SlackClient != nil {
		msg := fmt.Sprintf("[CMO-AE] Drafted campaign: %s. Review at /workspace/%s", title, fileID)
		if _, postErr := w.SlackClient.PostMessage(ctx, "#general", msg); postErr != nil {
			slog.Warn("campaign_draft: slack post error", "error", postErr)
		}
	}

	return nil
}

// ContentPublishWorker reads a workspace file and publishes it to a Slack channel via ToolGateway.
type ContentPublishWorker struct {
	river.WorkerDefaults[ContentPublishJobArgs]
	DB             *pgxpool.Pool
	WorkspaceStore *workspace.WorkspaceStore
	SlackClient    *slack.SlackClient
}

func (w *ContentPublishWorker) Work(ctx context.Context, job *river.Job[ContentPublishJobArgs]) error {
	slog.Info("content_publish", "employee_id", job.Args.EmployeeID, "file_id", job.Args.WorkspaceFileID, "channel", job.Args.Channel)

	if w.WorkspaceStore == nil {
		return fmt.Errorf("content_publish: workspace store not available")
	}

	file, err := w.WorkspaceStore.GetFile(ctx, job.Args.WorkspaceFileID)
	if err != nil {
		return fmt.Errorf("content_publish: get file %s: %w", job.Args.WorkspaceFileID, err)
	}

	gw := &tools.ToolGateway{
		DB:          w.DB,
		SlackClient: w.SlackClient,
		EmployeeID:  job.Args.EmployeeID,
		CompanyID:   job.Args.CompanyID,
	}

	result, execErr := gw.Execute(ctx, "slack", "post_message", map[string]any{
		"channel": job.Args.Channel,
		"text":    file.Content,
		"persona": "CMO-AE",
	})
	if execErr != nil {
		slog.Warn("content_publish: post failed", "channel", job.Args.Channel, "error", execErr)
	} else {
		slog.Info("content_publish: posted", "channel", job.Args.Channel, "result", result)
	}

	return nil
}

// MemberJoinWorker handles new human members joining the Slack workspace.
// It upserts the human as an employee in the DB and dispatches an LLM-generated,
// personality-driven greeting from the most senior active AE.
type MemberJoinWorker struct {
	river.WorkerDefaults[MemberJoinJobArgs]
	DB        *pgxpool.Pool
	LLMRouter *llm.Router
}

func (w *MemberJoinWorker) Work(ctx context.Context, job *river.Job[MemberJoinJobArgs]) error {
	slog.Info("member_join", "slack_user_id", job.Args.SlackUserID, "name", job.Args.RealName)

	// 1. Resolve company ID — single-tenant fallback if empty.
	companyID := job.Args.CompanyID
	if companyID == "" {
		if err := w.DB.QueryRow(ctx,
			`SELECT id FROM companies WHERE status = 'active' ORDER BY created_at LIMIT 1`,
		).Scan(&companyID); err != nil {
			return fmt.Errorf("resolve company: %w", err)
		}
	}

	// 2. Upsert the human employee using a deterministic ID derived from their Slack user ID.
	name := job.Args.RealName
	if name == "" {
		name = job.Args.SlackUserID
	}
	humanID := "human-" + job.Args.SlackUserID
	if _, err := w.DB.Exec(ctx, `
		INSERT INTO employees (id, company_id, name, role, type, status, slack_user_id)
		VALUES ($1, $2, $3, 'Human', 'human', 'active', $4)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, status = 'active'
	`, humanID, companyID, name, job.Args.SlackUserID); err != nil {
		slog.Warn("member_join: upsert human employee failed", "err", err)
		// Non-fatal — still attempt the greeting.
	}

	// 3. Find the CEO-AE first; fall back to any active AE.
	var aeID, aeName string
	var configJSON []byte
	err := w.DB.QueryRow(ctx, `
		SELECT e.id, COALESCE(e.name, e.role, 'AE'), ec.config
		FROM employees e
		JOIN employee_configs ec ON ec.employee_id = e.id
		WHERE e.company_id = $1 AND e.type = 'ae' AND e.status = 'active'
		  AND LOWER(e.role) LIKE '%ceo%'
		ORDER BY e.created_at LIMIT 1
	`, companyID).Scan(&aeID, &aeName, &configJSON)
	if err != nil {
		err = w.DB.QueryRow(ctx, `
			SELECT e.id, COALESCE(e.name, e.role, 'AE'), ec.config
			FROM employees e
			JOIN employee_configs ec ON ec.employee_id = e.id
			WHERE e.company_id = $1 AND e.type = 'ae' AND e.status = 'active'
			ORDER BY e.created_at LIMIT 1
		`, companyID).Scan(&aeID, &aeName, &configJSON)
		if err != nil {
			slog.Warn("member_join: no active AE found, skipping greeting", "company_id", companyID)
			return nil
		}
	}

	// 4. Load the AE's soul/personality from their config.
	var cfg domain.EmployeeConfig
	soulContent := ""
	if unmarshalErr := json.Unmarshal(configJSON, &cfg); unmarshalErr == nil {
		soulContent = cfg.Identity.SoulFile
	}

	// 5. Generate a personality-driven greeting via LLM.
	systemPrompt := "You are an AI employee at a company. Respond only as yourself, in character."
	if soulContent != "" {
		systemPrompt = fmt.Sprintf(
			"You are an AI employee. Your soul and personality:\n\n%s\n\nStay completely in character.",
			soulContent,
		)
	}
	userPrompt := fmt.Sprintf(
		"%s just joined the company on Slack. Write a short, warm, in-character welcome message. "+
			"2-3 sentences, first person. No hashtags or markdown.",
		name,
	)

	modelRef := cfg.Cognition.DefaultModelRef
	if modelRef == "" {
		modelRef = w.LLMRouter.DefaultModel()
	}
	greeting, llmErr := w.LLMRouter.Complete(ctx, modelRef, systemPrompt, userPrompt, 150)
	if llmErr != nil {
		slog.Warn("member_join: llm greeting failed, using fallback", "err", llmErr)
		greeting = fmt.Sprintf("Welcome to the team, %s! Really glad to have you with us.", name)
	}

	// 6. Post the greeting to #general as the AE persona.
	slackToken := os.Getenv("SLACK_BOT_TOKEN")
	if slackToken == "" {
		slog.Warn("member_join: SLACK_BOT_TOKEN not set, skipping greeting")
		return nil
	}
	slackClient := slack.NewClient(slackToken)
	msg := fmt.Sprintf("[%s] %s", aeName, greeting)
	if _, postErr := slackClient.PostMessage(ctx, "#general", msg); postErr != nil {
		slog.Warn("member_join: post greeting failed", "err", postErr)
	}

	return nil
}

// WorkerDeps holds shared dependencies for all workers.
type WorkerDeps struct {
	DB               *pgxpool.Pool
	LLMRouter        *llm.Router
	ContainerManager *container.Manager
	WorkspaceStore   *workspace.WorkspaceStore
	SlackClient      *slack.SlackClient
}

// NewWorkers registers all job workers and returns the bundle.
func NewWorkers(deps WorkerDeps) *river.Workers {
	router := deps.LLMRouter
	if router == nil {
		router = &llm.Router{}
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &HeartbeatWorker{DB: deps.DB, LLMRouter: router, ContainerManager: deps.ContainerManager})
	river.AddWorker(workers, &SlackEventWorker{DB: deps.DB})
	river.AddWorker(workers, &ToolExecutionWorker{})
	river.AddWorker(workers, &HiringWorker{DB: deps.DB, LLMRouter: router, ContainerManager: deps.ContainerManager})
	river.AddWorker(workers, &CampaignDraftWorker{DB: deps.DB, LLMRouter: router, WorkspaceStore: deps.WorkspaceStore, SlackClient: deps.SlackClient})
	river.AddWorker(workers, &ContentPublishWorker{DB: deps.DB, WorkspaceStore: deps.WorkspaceStore, SlackClient: deps.SlackClient})
	river.AddWorker(workers, &MemberJoinWorker{DB: deps.DB, LLMRouter: router})
	return workers
}
