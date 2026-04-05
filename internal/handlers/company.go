package handlers

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/17twenty/rally/internal/container"
	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/org"
	"github.com/17twenty/rally/internal/queue"
	"github.com/17twenty/rally/internal/tools"
	"github.com/17twenty/rally/templates/pages"
	"github.com/a-h/templ"
)

// CompanyHandler holds dependencies for company HTTP handlers.
type CompanyHandler struct {
	DB               *db.DB
	LLMRouter        *llm.Router
	ContainerManager *container.Manager
	InvoiceTool      *tools.InvoiceTool
}

// newID generates a random UUID v4 string.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// List handles GET /companies.
func (h *CompanyHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Pool.Query(r.Context(),
		`SELECT id, name, COALESCE(mission,''), status, created_at FROM companies ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, "failed to load companies", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var companies []domain.Company
	for rows.Next() {
		var c domain.Company
		if err := rows.Scan(&c.ID, &c.Name, &c.Mission, &c.Status, &c.CreatedAt); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		companies = append(companies, c)
	}

	templ.Handler(pages.CompaniesList(companies)).ServeHTTP(w, r)
}

// New handles GET /companies/new.
func (h *CompanyHandler) New(w http.ResponseWriter, r *http.Request) {
	templ.Handler(pages.CompanyNew()).ServeHTTP(w, r)
}

// Create handles POST /companies.
func (h *CompanyHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "company name is required", http.StatusBadRequest)
		return
	}
	mission := r.FormValue("mission")

	ctx := r.Context()
	companyID := newID()

	_, err := h.DB.Pool.Exec(ctx,
		`INSERT INTO companies (id, name, mission, status) VALUES ($1, $2, $3, 'pending')`,
		companyID, name, mission,
	)
	if err != nil {
		http.Error(w, "failed to create company", http.StatusInternalServerError)
		return
	}

	empNames := r.Form["emp_name"]
	empRoles := r.Form["emp_role"]
	empSpecialties := r.Form["emp_specialties"]

	for i, empName := range empNames {
		if empName == "" {
			continue
		}
		empRole := ""
		if i < len(empRoles) {
			empRole = empRoles[i]
		}
		empSpec := ""
		if i < len(empSpecialties) {
			empSpec = empSpecialties[i]
		}

		empID := newID()
		_, err := h.DB.Pool.Exec(ctx,
			`INSERT INTO employees (id, company_id, name, role, specialties, type, status) VALUES ($1, $2, $3, $4, $5, 'human', 'active')`,
			empID, companyID, empName, empRole, empSpec,
		)
		if err != nil {
			http.Error(w, "failed to add employee", http.StatusInternalServerError)
			return
		}
	}

	// Design org and enqueue hiring jobs for each planned AE role.
	if queue.Client != nil {
		humanRows, err := h.DB.Pool.Query(ctx,
			`SELECT id, COALESCE(name,''), role FROM employees WHERE company_id = $1 AND type = 'human'`,
			companyID,
		)
		if err == nil {
			var humans []domain.Employee
			for humanRows.Next() {
				var e domain.Employee
				if scanErr := humanRows.Scan(&e.ID, &e.Name, &e.Role); scanErr == nil {
					e.Type = "human"
					humans = append(humans, e)
				}
			}
			humanRows.Close()

			mgr := org.NewOrgManager()
			company := domain.Company{ID: companyID, Name: name, Mission: mission}
			if plan, designErr := mgr.DesignOrg(company, humans); designErr == nil {
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
	}

	http.Redirect(w, r, "/companies/"+companyID+"?msg=Building+your+team...", http.StatusSeeOther)
}

// Show handles GET /companies/{id}.
func (h *CompanyHandler) Show(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	var company domain.Company
	err := h.DB.Pool.QueryRow(ctx,
		`SELECT id, name, COALESCE(mission,''), status, created_at FROM companies WHERE id = $1`, id,
	).Scan(&company.ID, &company.Name, &company.Mission, &company.Status, &company.CreatedAt)
	if err != nil {
		http.Error(w, "company not found", http.StatusNotFound)
		return
	}

	rows, err := h.DB.Pool.Query(ctx,
		`SELECT id, company_id, COALESCE(name,''), role, COALESCE(specialties,''), type, status, COALESCE(slack_user_id,''), created_at
		 FROM employees WHERE company_id = $1 ORDER BY created_at ASC`, id)
	if err != nil {
		http.Error(w, "failed to load employees", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var employees []domain.Employee
	for rows.Next() {
		var e domain.Employee
		if err := rows.Scan(&e.ID, &e.CompanyID, &e.Name, &e.Role, &e.Specialties, &e.Type, &e.Status, &e.SlackUserID, &e.CreatedAt); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		employees = append(employees, e)
	}

	flashMsg := r.URL.Query().Get("msg")

	var policyDoc string
	_ = h.DB.Pool.QueryRow(ctx, `SELECT COALESCE(policy_doc,'') FROM companies WHERE id = $1`, id).Scan(&policyDoc)

	var financials *domain.CompanyFinancials
	var fin domain.CompanyFinancials
	err = h.DB.Pool.QueryRow(ctx, `
		SELECT id, company_id,
		       COALESCE(bank_name,''), COALESCE(account_name,''),
		       COALESCE(bsb,''), COALESCE(account_number,''),
		       COALESCE(swift_code,''), COALESCE(payment_provider,''),
		       COALESCE(invoice_prefix,'INV'), COALESCE(invoice_counter,1),
		       COALESCE(currency,'AUD'), created_at
		FROM company_financials WHERE company_id = $1`, id,
	).Scan(
		&fin.ID, &fin.CompanyID,
		&fin.BankName, &fin.AccountName,
		&fin.BSB, &fin.AccountNumber,
		&fin.SwiftCode, &fin.PaymentProvider,
		&fin.InvoicePrefix, &fin.InvoiceCounter,
		&fin.InvoiceCurrency, &fin.CreatedAt,
	)
	if err == nil {
		financials = &fin
	}

	templ.Handler(pages.CompanyShow(company, employees, flashMsg, policyDoc, financials)).ServeHTTP(w, r)
}

// GetPolicy handles GET /companies/{id}/policy.
func (h *CompanyHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var policyDoc string
	err := h.DB.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(policy_doc,'') FROM companies WHERE id = $1`, id,
	).Scan(&policyDoc)
	if err != nil {
		http.Error(w, "company not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"policy_doc": policyDoc})
}

// GetFinancials handles GET /companies/{id}/financials.
func (h *CompanyHandler) GetFinancials(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	var fin domain.CompanyFinancials
	err := h.DB.Pool.QueryRow(ctx, `
		SELECT id, company_id,
		       COALESCE(bank_name,''), COALESCE(account_name,''),
		       COALESCE(bsb,''), COALESCE(account_number,''),
		       COALESCE(swift_code,''), COALESCE(payment_provider,''),
		       COALESCE(invoice_prefix,'INV'), COALESCE(invoice_counter,1),
		       COALESCE(currency,'AUD'), created_at
		FROM company_financials WHERE company_id = $1`, id,
	).Scan(
		&fin.ID, &fin.CompanyID,
		&fin.BankName, &fin.AccountName,
		&fin.BSB, &fin.AccountNumber,
		&fin.SwiftCode, &fin.PaymentProvider,
		&fin.InvoicePrefix, &fin.InvoiceCounter,
		&fin.InvoiceCurrency, &fin.CreatedAt,
	)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"financials": nil})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"financials": fin})
}

// SetFinancials handles POST /companies/{id}/financials.
func (h *CompanyHandler) SetFinancials(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	bankName := r.FormValue("bank_name")
	accountName := r.FormValue("account_name")
	bsb := r.FormValue("bsb")
	accountNumber := r.FormValue("account_number")
	swiftCode := r.FormValue("swift_code")
	paymentProvider := r.FormValue("payment_provider")
	invoicePrefix := r.FormValue("invoice_prefix")
	if invoicePrefix == "" {
		invoicePrefix = "INV"
	}
	currency := r.FormValue("currency")
	if currency == "" {
		currency = "AUD"
	}

	finID := newID()
	_, err := h.DB.Pool.Exec(ctx, `
		INSERT INTO company_financials
		  (id, company_id, bank_name, account_name, bsb, account_number,
		   swift_code, payment_provider, invoice_prefix, currency)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (company_id) DO UPDATE SET
		  bank_name=$3, account_name=$4, bsb=$5, account_number=$6,
		  swift_code=$7, payment_provider=$8, invoice_prefix=$9, currency=$10,
		  updated_at=NOW()`,
		finID, id, bankName, accountName, bsb, accountNumber,
		swiftCode, paymentProvider, invoicePrefix, currency,
	)
	if err != nil {
		http.Error(w, "failed to save financials", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/companies/"+id+"?msg=Financial+settings+updated", http.StatusSeeOther)
}

// CreateInvoice handles POST /companies/{id}/invoice.
func (h *CompanyHandler) CreateInvoice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	issuedTo := r.FormValue("issued_to")
	issuedToEmail := r.FormValue("issued_to_email")
	description := r.FormValue("description")
	notes := r.FormValue("notes")

	qty := float64(1)
	if v, err := strconv.ParseFloat(r.FormValue("quantity"), 64); err == nil {
		qty = v
	}
	unitPrice := float64(0)
	if v, err := strconv.ParseFloat(r.FormValue("unit_price"), 64); err == nil {
		unitPrice = v
	}
	dueDays := float64(30)
	if v, err := strconv.ParseFloat(r.FormValue("due_days"), 64); err == nil {
		dueDays = v
	}

	if h.InvoiceTool == nil {
		http.Error(w, "invoice tool not configured", http.StatusInternalServerError)
		return
	}

	result, err := h.InvoiceTool.Execute(ctx, "send_invoice", map[string]any{
		"company_id":      id,
		"issued_to":       issuedTo,
		"issued_to_email": issuedToEmail,
		"due_days":        dueDays,
		"notes":           notes,
		"line_items": []any{
			map[string]any{
				"description": description,
				"quantity":    qty,
				"unit_price":  unitPrice,
			},
		},
	})
	if err != nil {
		http.Error(w, "failed to generate invoice: "+err.Error(), http.StatusInternalServerError)
		return
	}

	invoiceNum, _ := result["invoice_number"].(string)
	sent, _ := result["sent"].(bool)

	accept := r.Header.Get("Accept")
	if accept == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}

	msg := fmt.Sprintf("Invoice+%s+generated", invoiceNum)
	if sent {
		msg = fmt.Sprintf("Invoice+%s+sent+to+%s", invoiceNum, issuedToEmail)
	}
	http.Redirect(w, r, "/companies/"+id+"?msg="+msg, http.StatusSeeOther)
}

// SetPolicy handles POST /companies/{id}/policy.
func (h *CompanyHandler) SetPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	var content string
	ct := r.Header.Get("Content-Type")
	if ct == "application/json" {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		content = body.Content
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form data", http.StatusBadRequest)
			return
		}
		content = r.FormValue("policy")
	}

	_, err := h.DB.Pool.Exec(ctx,
		`UPDATE companies SET policy_doc = $1 WHERE id = $2`, content, id,
	)
	if err != nil {
		http.Error(w, "failed to update policy", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/companies/"+id, http.StatusSeeOther)
}

// Nuke handles POST /companies/{id}/nuke — destroys a company and all its data.
func (h *CompanyHandler) Nuke(w http.ResponseWriter, r *http.Request) {
	companyID := r.PathValue("id")
	ctx := r.Context()

	slog.Warn("nuke_company", "company_id", companyID)

	// Delete all data in dependency order.
	queries := []string{
		`DELETE FROM work_item_history WHERE work_item_id IN (SELECT id FROM work_items WHERE company_id = $1)`,
		`DELETE FROM work_items WHERE company_id = $1`,
		`DELETE FROM ae_messages WHERE company_id = $1`,
		`DELETE FROM ae_api_tokens WHERE company_id = $1`,
		`DELETE FROM tool_logs WHERE company_id = $1`,
		`DELETE FROM memory_events WHERE employee_id IN (SELECT id FROM employees WHERE company_id = $1)`,
		`DELETE FROM access_providers WHERE company_id = $1`,
		`DELETE FROM employee_configs WHERE employee_id IN (SELECT id FROM employees WHERE company_id = $1)`,
		`DELETE FROM org_structure WHERE company_id = $1`,
		`DELETE FROM slack_events WHERE company_id = $1`,
		`DELETE FROM tasks WHERE company_id = $1`,
		`DELETE FROM employees WHERE company_id = $1`,
		`DELETE FROM knowledgebase WHERE company_id = $1`,
		`DELETE FROM companies WHERE id = $1`,
	}

	for _, q := range queries {
		if _, err := h.DB.Pool.Exec(ctx, q, companyID); err != nil {
			slog.Warn("nuke: query failed", "err", err)
		}
	}

	http.Redirect(w, r, "/setup?msg=Organisation+deleted.+Start+fresh.", http.StatusSeeOther)
}
