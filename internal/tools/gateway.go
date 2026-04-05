package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/17twenty/rally/internal/slack"
	"github.com/17twenty/rally/internal/vault"
	"github.com/17twenty/rally/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ToolGateway is the central interface for all AE tool access with permission
// enforcement and logging.
type ToolGateway struct {
	DB                   *pgxpool.Pool
	SlackClient          *slack.SlackClient
	WorkspaceStore       *workspace.WorkspaceStore
	Vault                *vault.CredentialVault
	GoogleWorkspaceTool  *GoogleWorkspaceTool
	FigmaTool            *FigmaTool
	InvoiceTool          *InvoiceTool
	EmployeeID           string
	CompanyID            string
	TraceID              string
	TaskID               string
	ApprovalGranted      bool            // required for github write actions and workspace approve_file
	ToolsConfig          map[string]bool // employee tool-enable config (from employee YAML tools: map)
}

// requiresApproval lists tool+action combos that need human approval.
// Browser actions (navigate, extract_text, screenshot, fetch_html, click,
// fill_form, type_text, select_option, submit_form, interact_sequence) are
// permitted for all AEs without approval.
var requiresApproval = map[string]map[string]bool{
	"github":    {"create_comment": true},
	"workspace": {"approve_file": true},
}

// invoiceAllowedRoles lists the roles that may use each invoice action.
// CEO-AE and SDR-AE may generate/send; all AEs may list.
var invoiceAllowedRoles = map[string][]string{
	"generate_pdf":  {"CEO", "SDR"},
	"send_invoice":  {"CEO", "SDR"},
	"list_invoices": nil, // nil = all allowed
}

// Execute routes to the appropriate tool handler, enforces permissions, and
// logs the invocation to tool_logs.
func (g *ToolGateway) Execute(ctx context.Context, tool, action string, input map[string]any) (map[string]any, error) {
	// Permission enforcement
	if actions, ok := requiresApproval[tool]; ok {
		if actions[action] && !g.ApprovalGranted {
			return nil, fmt.Errorf("tools: %s.%s requires human approval", tool, action)
		}
	}

	var (
		output  map[string]any
		execErr error
	)

	switch tool {
	case "slack":
		st := &SlackTool{Client: g.SlackClient}
		output, execErr = st.Execute(ctx, action, input)
	case "shell":
		st := &ShellTool{}
		output, execErr = st.Execute(ctx, action, input)
	case "github":
		token := g.vaultToken(ctx, "github")
		gt := &GitHubTool{ApprovalGranted: g.ApprovalGranted, Token: token}
		output, execErr = gt.Execute(ctx, action, input)
	case "google_workspace":
		gwt := g.GoogleWorkspaceTool
		if gwt == nil {
			gwt = &GoogleWorkspaceTool{
				Vault:       g.Vault,
				SlackClient: g.SlackClient,
				EmployeeID:  g.EmployeeID,
			}
		}
		output, execErr = gwt.Execute(ctx, action, input)
	case "browser":
		bt := &BrowserTool{}
		output, execErr = bt.Execute(ctx, action, input)
	case "workspace":
		wt := &WorkspaceTool{WorkspaceStore: g.WorkspaceStore}
		output, execErr = wt.Execute(ctx, action, input)
	case "figma":
		ft := g.FigmaTool
		if ft == nil {
			ft = &FigmaTool{
				Vault:      g.Vault,
				EmployeeID: g.EmployeeID,
			}
		}
		output, execErr = ft.Execute(ctx, action, input)
	case "invoice":
		it := g.InvoiceTool
		if it == nil {
			it = &InvoiceTool{DB: g.DB}
		}
		output, execErr = it.Execute(ctx, action, input)
	default:
		return nil, fmt.Errorf("tools: unknown tool %q", tool)
	}

	// Auto-request credentials when the tool signals they are missing.
	if execErr != nil && g.CompanyID != "" {
		msg := execErr.Error()
		if strings.Contains(msg, "credentials not configured") || strings.Contains(msg, "not found in vault") {
			execErr = g.RequestCredential(ctx, tool, action)
		}
	}

	success := execErr == nil
	if output == nil {
		output = map[string]any{}
	}
	if execErr != nil {
		output["error"] = execErr.Error()
	}

	// Log to tool_logs (non-fatal on error)
	if g.DB != nil {
		_ = g.logExecution(ctx, tool, action, input, output, success)
	}

	return output, execErr
}

// RequestCredential inserts a credential_requests row, notifies human employees via
// Slack DM, and returns a sentinel error indicating the request was sent.
func (g *ToolGateway) RequestCredential(ctx context.Context, tool, reason string) error {
	reqID := newID()

	if g.DB != nil {
		_, _ = g.DB.Exec(ctx, `
			INSERT INTO credential_requests (id, employee_id, company_id, provider_name, reason, status)
			VALUES ($1, $2, $3, $4, $5, 'pending')
		`, reqID, g.EmployeeID, g.CompanyID, tool, reason)

		// Notify human employees with a Slack DM.
		if g.SlackClient != nil {
			rows, err := g.DB.Query(ctx,
				`SELECT id, COALESCE(slack_user_id, '') FROM employees WHERE company_id = $1 AND type = 'human'`,
				g.CompanyID,
			)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var empID, slackUID string
					if err := rows.Scan(&empID, &slackUID); err != nil || slackUID == "" {
						continue
					}
					msg := fmt.Sprintf(
						"[Developer-AE] I need %s credentials to continue my work. Reason: %s. Please add credentials at /credentials. Request ID: %s",
						tool, reason, reqID,
					)
					_, _ = g.SlackClient.PostMessage(ctx, slackUID, msg)
				}
			}
		}
	}

	return fmt.Errorf("credential request sent for %s, awaiting human approval", tool)
}

// vaultToken retrieves a token from the vault for the given provider.
// Falls back to the matching environment variable if not found in vault.
func (g *ToolGateway) vaultToken(ctx context.Context, provider string) string {
	if g.Vault != nil && g.EmployeeID != "" {
		token, err := g.Vault.Get(ctx, g.EmployeeID, provider)
		if err == nil {
			// Audit the retrieval (non-fatal on error)
			if g.DB != nil {
				_ = vault.LogAccess(ctx, g.DB, g.EmployeeID, provider, "get")
			}
			return token
		}
		if !errors.Is(err, vault.ErrNotFound) {
			// Log unexpected errors but continue to env fallback
			_ = err
		}
	}
	// Env var fallback for backward compatibility
	switch provider {
	case "github":
		return os.Getenv("GITHUB_TOKEN")
	}
	return ""
}

func (g *ToolGateway) logExecution(ctx context.Context, tool, action string, input, output map[string]any, success bool) error {
	inputBytes, _ := json.Marshal(input)
	outputBytes, _ := json.Marshal(output)

	_, err := g.DB.Exec(ctx,
		`INSERT INTO tool_logs (id, employee_id, tool, action, input, output, success, trace_id, task_id)
		 VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9)`,
		newID(),
		g.EmployeeID,
		tool,
		action,
		string(inputBytes),
		string(outputBytes),
		success,
		g.TraceID,
		g.TaskID,
	)
	return err
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
