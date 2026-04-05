package hiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/17twenty/rally/internal/container"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/org"
	"github.com/17twenty/rally/internal/slack"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Hirer handles the full AE hiring flow.
type Hirer struct {
	DB               *pgxpool.Pool
	LLMRouter        *llm.Router
	SlackClient      *slack.SlackClient
	ContainerManager *container.Manager
	// OnHired is called after an AE is successfully created. Use it to
	// enqueue the first heartbeat job without creating an import cycle.
	OnHired func(ctx context.Context, employeeID, companyID string)
}

// HireAE runs the full hiring flow for a single planned role:
// generates soul.md, inserts DB rows, provisions Slack, posts introduction.
func (h *Hirer) HireAE(ctx context.Context, companyID string, plan org.PlannedRole, company domain.Company) (*domain.Employee, error) {
	// 1. Generate AE name
	aeName, err := GenerateAEName(ctx, h.LLMRouter, plan.Role, company.Name)
	if err != nil {
		aeName = fallbackName(plan.Role)
	}

	// 2. Generate soul.md
	soulMD, err := GenerateSoulMD(ctx, h.LLMRouter, aeName, plan.Role, plan.Department, plan.ReportsTo, company.Name, company.Mission)
	if err != nil {
		return nil, fmt.Errorf("generate soul: %w", err)
	}

	// 3. Build EmployeeConfig with soul content, model defaults, tools
	employeeID := fmt.Sprintf("%s-%s-%d", companyID, plan.ID, time.Now().UnixNano())
	cfg := &domain.EmployeeConfig{
		ID:         fmt.Sprintf("cfg-%s", employeeID),
		EmployeeID: employeeID,
	}
	cfg.Employee.ID = employeeID
	cfg.Employee.Role = plan.Role
	cfg.Employee.ReportsTo = plan.ReportsTo
	cfg.Identity.SoulFile = soulMD
	cfg.Cognition.DefaultModelRef = h.LLMRouter.DefaultModel()
	cfg.Cognition.Routing = map[string]string{}
	cfg.Runtime.HeartbeatSeconds = 300
	cfg.Tools = map[string]bool{
		"slack":            true,
		"github":           true,
		"shell":            true,
		"browser":          true,
		"workspace":        true,
		"google_workspace": true,
		"figma":            true,
	}

	displayName := aeName + " (" + plan.Role + ")"

	// 4. Insert employee row (status='active')
	_, err = h.DB.Exec(ctx,
		`INSERT INTO employees (id, company_id, name, role, type, status) VALUES ($1, $2, $3, $4, 'ae', 'active')`,
		employeeID, companyID, displayName, plan.Role,
	)
	if err != nil {
		return nil, fmt.Errorf("insert employee: %w", err)
	}

	// Insert employee_config row
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	_, err = h.DB.Exec(ctx,
		`INSERT INTO employee_configs (id, employee_id, config) VALUES ($1, $2, $3)`,
		cfg.ID, employeeID, cfgJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert employee_config: %w", err)
	}

	// 4. Insert org_structure row
	orgID := fmt.Sprintf("org-%s", employeeID)
	_, err = h.DB.Exec(ctx,
		`INSERT INTO org_structure (id, company_id, employee_id, reports_to, department) VALUES ($1, $2, $3, $4, $5)`,
		orgID, companyID, employeeID, nullableString(plan.ReportsTo), plan.Department,
	)
	if err != nil {
		return nil, fmt.Errorf("insert org_structure: %w", err)
	}

	// 5. Provision AE container
	if h.ContainerManager != nil {
		containerName := container.ContainerName(company.Name, plan.Role, aeName)
		cfgJSONStr := string(cfgJSON)

		// Load policy for workspace seeding.
		var policyDoc string
		_ = h.DB.QueryRow(ctx, `SELECT COALESCE(policy_doc,'') FROM companies WHERE id = $1`, companyID).Scan(&policyDoc)

		token, tokenHash, tokenErr := container.GenerateAPIToken()
		if tokenErr != nil {
			slog.Warn("hiring: generate API token failed", "err", tokenErr)
		} else {
			tokenID := fmt.Sprintf("tok-%s", employeeID)
			if storeErr := container.StoreToken(ctx, h.DB, tokenID, employeeID, companyID, tokenHash); storeErr != nil {
				slog.Warn("hiring: store API token failed", "err", storeErr)
			} else {
				containerID, startErr := h.ContainerManager.CreateAndStart(ctx, container.CreateAndStartOpts{
					ContainerName:  containerName,
					EmployeeID:     employeeID,
					CompanyID:      companyID,
					CompanyName:    company.Name,
					CompanyMission: company.Mission,
					Role:           plan.Role,
					AEName:         aeName,
					APIToken:       token,
					SoulMD:         soulMD,
					ConfigJSON:     cfgJSONStr,
					PolicyDoc:      policyDoc,
				})
				if startErr != nil {
					slog.Warn("hiring: container start failed", "err", startErr, "name", containerName)
				} else {
					_, _ = h.DB.Exec(ctx,
						`UPDATE employees SET container_id = $1, container_status = 'running' WHERE id = $2`,
						containerID, employeeID)
				}
			}
		}
	}

	// 6. Notify caller (enqueue heartbeat for health monitoring).
	if h.OnHired != nil {
		h.OnHired(ctx, employeeID, companyID)
	}

	// 7. Provision Slack channels and post introduction
	if h.SlackClient != nil {
		if err := slack.EnsureDefaultChannels(ctx, h.SlackClient); err != nil {
			// Non-fatal: log but continue
			_ = err
		}

		// 6. Generate and post an in-character introduction using the AE's soul/personality.
		introMsg := generateIntroMessage(ctx, h.LLMRouter, aeName, plan.Role, company.Name, soulMD)
		_, _ = h.SlackClient.PostMessage(ctx, "#general",
			fmt.Sprintf("[%s-AE] %s", plan.Role, introMsg))
	}

	// 7. Return created Employee
	return &domain.Employee{
		ID:        employeeID,
		CompanyID: companyID,
		Name:      displayName,
		Role:      plan.Role,
		Type:      "ae",
		Status:    "active",
		Config:    cfg,
	}, nil
}

// generateIntroMessage asks the LLM to write a first-person introduction for a
// newly hired AE, fully grounded in their soul document. Falls back to a simple
// template if the LLM is unavailable.
func generateIntroMessage(ctx context.Context, router *llm.Router, name, role, companyName, soulMD string) string {
	if router == nil {
		return fmt.Sprintf("Hello, I'm %s, the %s at %s. Excited to be here and ready to get to work.", name, role, companyName)
	}
	systemPrompt := fmt.Sprintf(
		"You are %s, the %s at %s. Your soul and personality:\n\n%s\n\nStay completely in character.",
		name, role, companyName, soulMD,
	)
	userPrompt := "You've just been onboarded and are introducing yourself to the team in Slack's #general channel. " +
		"Write your introduction. 2-3 sentences, first person, in character. No hashtags or markdown formatting."

	result, err := router.Complete(ctx, router.DefaultModel(), systemPrompt, userPrompt, 200)
	if err != nil {
		return fmt.Sprintf("Hello, I'm %s, the %s at %s. Excited to join the team!", name, role, companyName)
	}
	return strings.TrimSpace(result)
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
