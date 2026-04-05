package handlers

import (
	"fmt"
	"net/http"
	"os"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/kb"
	"github.com/17twenty/rally/internal/org"
	"github.com/17twenty/rally/internal/queue"
	"github.com/17twenty/rally/internal/vault"
	"github.com/17twenty/rally/templates/pages"
	"github.com/a-h/templ"
)

// SetupHandler handles the Rally self-bootstrap wizard.
type SetupHandler struct {
	DB    *db.DB
	Vault *vault.CredentialVault
}

func (h *SetupHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

// Show handles GET /setup.
// If no company exists, shows the setup wizard. Otherwise shows the Settings page.
func (h *SetupHandler) Show(w http.ResponseWriter, r *http.Request) {
	if h.DB != nil {
		ctx := r.Context()
		count, _ := h.q().CountCompanies(ctx)
		if count > 0 {
			// Show settings page with company info and integration status.
			companies, _ := h.q().ListCompanies(ctx)
			var company domain.Company
			if len(companies) > 0 {
				c := companies[0]
				company = domain.Company{
					ID: c.ID, Name: c.Name, Mission: db.Deref(c.Mission),
					Status: c.Status, CreatedAt: db.PgTime(c.CreatedAt),
				}
			}

			// Check integration status.
			slackConnected := false
			if h.Vault != nil {
				if _, err := h.Vault.Get(ctx, "rally-system", "slack"); err == nil {
					slackConnected = true
				}
			}

			templ.Handler(pages.Settings(pages.SettingsData{
				Company:         company,
				SlackConnected:  slackConnected,
				SlackConfigured: os.Getenv("SLACK_CLIENT_ID") != "",
			})).ServeHTTP(w, r)
			return
		}
	}
	templ.Handler(pages.Setup(pages.SetupWizardData{
		SlackConfigured: os.Getenv("SLACK_CLIENT_ID") != "",
	})).ServeHTTP(w, r)
}

// Create handles POST /setup.
func (h *SetupHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	companyName := r.FormValue("company_name")
	if companyName == "" {
		companyName = "Rally"
	}
	mission := r.FormValue("mission")
	if mission == "" {
		mission = "Build and operate an AI-powered organization OS. We eat our own dogfood — Rally runs Rally."
	}
	founderName := r.FormValue("founder_name")
	founderRole := r.FormValue("founder_role")
	if founderRole == "" {
		founderRole = "Founder"
	}
	founderSlackID := r.FormValue("founder_slack_id")
	policy := r.FormValue("policy")
	githubRepo := r.FormValue("github_repo")
	githubToken := r.FormValue("github_token")

	ctx := r.Context()

	if h.DB == nil {
		http.Error(w, "database not configured", http.StatusInternalServerError)
		return
	}

	// Check if already set up.
	count, _ := h.q().CountCompanies(ctx)
	if count > 0 {
		http.Redirect(w, r, "/?msg=Rally+is+already+set+up", http.StatusSeeOther)
		return
	}

	// 1. Create company row (status='active').
	companyID := newID()
	_, err := h.q().InsertCompany(ctx, dao.InsertCompanyParams{
		ID:      companyID,
		Name:    companyName,
		Mission: db.Ref(mission),
		Status:  "active",
	})
	if err != nil {
		http.Error(w, "failed to create company: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Set company policy if provided.
	if policy != "" {
		_ = h.q().UpdateCompanyPolicy(ctx, dao.UpdateCompanyPolicyParams{ID: companyID, PolicyDoc: db.Ref(policy)})
	}

	// 2. Create human founder employee row.
	founderID := newID()
	_, err = h.q().InsertEmployee(ctx, dao.InsertEmployeeParams{
		ID:          founderID,
		CompanyID:   companyID,
		Role:        founderRole,
		Type:        "human",
		Status:      "active",
		SlackUserID: db.Ref(founderSlackID),
		Name:        db.Ref(founderName),
		Specialties: db.Ref(""),
	})
	if err != nil {
		http.Error(w, "failed to create founder employee: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Design org and enqueue hiring jobs.
	if queue.Client != nil {
		company := domain.Company{ID: companyID, Name: companyName, Mission: mission}
		founder := domain.Employee{
			ID:          founderID,
			CompanyID:   companyID,
			Name:        founderName,
			Role:        founderRole,
			Type:        "human",
			Status:      "active",
			SlackUserID: founderSlackID,
		}

		mgr := org.NewOrgManager()
		if plan, designErr := mgr.DesignOrg(company, []domain.Employee{founder}); designErr == nil {
			for _, role := range plan.Roles {
				_, _ = queue.Client.Insert(ctx, queue.HiringJobArgs{
					CompanyID:  companyID,
					PlanRoleID: role.ID,
					Role:       role.Role,
					Department: role.Department,
					ReportsTo:  role.ReportsTo,
				}, nil)
			}
		}
	}

	// 4. If github_token provided, store speculatively in vault by role placeholder.
	if githubToken != "" && h.Vault != nil {
		// Store using a role-based placeholder employee ID so Developer-AE can pick it up.
		placeholderEmpID := fmt.Sprintf("developer-ae-%s", companyID)
		_ = h.Vault.Store(ctx, companyID, placeholderEmpID, "github", githubToken, "token", []string{"repo"})
	}

	// 5. Add KB entry for the repo if provided.
	if githubRepo != "" {
		kbStore := &kb.KBStore{DB: h.DB.Pool}
		content := fmt.Sprintf("GitHub repo: %s. This is the Rally source code. Developer-AE and Engineer-AE should use it for development work.", githubRepo)
		_ = kbStore.Save(ctx, domain.KnowledgebaseEntry{
			ID:         newID(),
			CompanyID:  companyID,
			Title:      "Rally Codebase",
			Content:    content,
			Tags:       []string{"repo", "development"},
			Status:     "active",
			ApprovedBy: "setup",
		})
	}

	http.Redirect(w, r, "/companies/"+companyID+"?msg=Your+team+is+being+assembled!+Check+Slack+for+introductions.", http.StatusSeeOther)
}
