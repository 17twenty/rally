package handlers

import (
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/vault"
	"github.com/17twenty/rally/templates/pages"
)

// CredentialHandler manages credential vault UI and API endpoints.
type CredentialHandler struct {
	DB    *db.DB
	Vault *vault.CredentialVault
}

// List handles GET /credentials — shows all access_providers for the company.
func (h *CredentialHandler) List(w http.ResponseWriter, r *http.Request) {
	data := pages.CredentialsPageData{}

	if h.DB != nil {
		ctx := r.Context()

		// Load employees for the dropdown and for name resolution
		empRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''), type, status, COALESCE(slack_user_id,''), created_at
			 FROM employees ORDER BY type, role`)
		empNames := map[string]string{}
		if err == nil {
			defer empRows.Close()
			for empRows.Next() {
				var e domain.Employee
				if scanErr := empRows.Scan(
					&e.ID, &e.CompanyID, &e.Name, &e.Role, &e.Specialties, &e.Type, &e.Status, &e.SlackUserID, &e.CreatedAt,
				); scanErr == nil {
					displayName := e.Name
					if displayName == "" {
						displayName = e.Role
					}
					empNames[e.ID] = displayName
					data.Employees = append(data.Employees, pages.EmployeeOption{ID: e.ID, Name: displayName})
				}
			}
		}

		// Load all access_providers
		if h.Vault != nil {
			rows, queryErr := h.DB.Pool.Query(ctx,
				`SELECT id, COALESCE(company_id,''), employee_id, provider_name, COALESCE(access_type,''), COALESCE(scopes,'{}'), status, created_at
				 FROM access_providers ORDER BY created_at DESC`)
			if queryErr == nil {
				defer rows.Close()
				for rows.Next() {
					var ap vault.AccessProvider
					if scanErr := rows.Scan(
						&ap.ID, &ap.CompanyID, &ap.EmployeeID, &ap.ProviderName, &ap.AccessType, &ap.Scopes, &ap.Status, &ap.CreatedAt,
					); scanErr == nil {
						name := empNames[ap.EmployeeID]
						if name == "" {
							name = ap.EmployeeID
						}
						data.Providers = append(data.Providers, pages.CredentialRow{
							AccessProvider: ap,
							EmployeeName:   name,
						})
					}
				}
			}
		}

		// Load recent audit log
		if h.Vault != nil {
			entries, _ := vault.GetAuditLog(ctx, h.DB.Pool, "", 100)
			data.AuditLog = entries
		}
	}

	templ.Handler(pages.Credentials(data)).ServeHTTP(w, r)
}

// Store handles POST /credentials — stores a new credential.
func (h *CredentialHandler) Store(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	employeeID := r.FormValue("employee_id")
	providerName := r.FormValue("provider_name")
	accessType := r.FormValue("access_type")
	token := r.FormValue("token")
	scopesRaw := r.FormValue("scopes")

	if employeeID == "" || providerName == "" || token == "" {
		http.Error(w, "employee_id, provider_name, and token are required", http.StatusBadRequest)
		return
	}

	var scopes []string
	for _, s := range strings.Split(scopesRaw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			scopes = append(scopes, s)
		}
	}

	// Resolve company_id from the employee record
	companyID := ""
	if h.DB != nil {
		_ = h.DB.Pool.QueryRow(r.Context(),
			`SELECT COALESCE(company_id,'') FROM employees WHERE id = $1`, employeeID,
		).Scan(&companyID)
	}

	if h.Vault == nil {
		http.Error(w, "vault not configured", http.StatusServiceUnavailable)
		return
	}

	if err := h.Vault.Store(r.Context(), companyID, employeeID, providerName, token, accessType, scopes); err != nil {
		http.Error(w, "failed to store credential: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/credentials", http.StatusSeeOther)
}

// Revoke handles POST /credentials/{id}/revoke.
func (h *CredentialHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	if h.Vault == nil {
		http.Error(w, "vault not configured", http.StatusServiceUnavailable)
		return
	}

	if err := h.Vault.RevokeByID(r.Context(), id); err != nil {
		http.Error(w, "failed to revoke: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/credentials", http.StatusSeeOther)
}

// AuditLog handles GET /credentials/audit — returns raw audit log as JSON-ish redirect to main page.
func (h *CredentialHandler) AuditLog(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/credentials", http.StatusSeeOther)
}
