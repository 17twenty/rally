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
	"github.com/17twenty/rally/internal/db/dao"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/llm"
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

func (h *CompanyHandler) q() *dao.Queries { return dao.New(h.DB.Pool) }

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
	rows, err := h.q().ListCompanies(r.Context())
	if err != nil {
		http.Error(w, "failed to load companies", http.StatusInternalServerError)
		return
	}

	companies := make([]domain.Company, len(rows))
	for i, c := range rows {
		companies[i] = domain.Company{
			ID:        c.ID,
			Name:      c.Name,
			Mission:   db.Deref(c.Mission),
			Status:    c.Status,
			CreatedAt: db.PgTime(c.CreatedAt),
		}
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

	_, err := h.q().InsertCompany(ctx, dao.InsertCompanyParams{
		ID:      companyID,
		Name:    name,
		Mission: db.Ref(mission),
		Status:  "pending",
	})
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
		_, err := h.q().InsertEmployee(ctx, dao.InsertEmployeeParams{
			ID:          empID,
			CompanyID:   companyID,
			Name:        db.Ref(empName),
			Role:        empRole,
			Specialties: db.Ref(empSpec),
			Type:        "human",
			Status:      "active",
		})
		if err != nil {
			http.Error(w, "failed to add employee", http.StatusInternalServerError)
			return
		}
	}

	// Company created with status='pending'. Use /companies/{id}/build to hire the CEO.
	http.Redirect(w, r, "/companies/"+companyID+"?msg=Company+created.+Click+Build+to+hire+your+CEO.", http.StatusSeeOther)
}

// Show handles GET /companies/{id}.
func (h *CompanyHandler) Show(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	c, err := h.q().GetCompany(ctx, id)
	if err != nil {
		http.Error(w, "company not found", http.StatusNotFound)
		return
	}
	company := domain.Company{
		ID:        c.ID,
		Name:      c.Name,
		Mission:   db.Deref(c.Mission),
		Status:    c.Status,
		CreatedAt: db.PgTime(c.CreatedAt),
	}

	empRows, err := h.q().ListEmployeesByCompany(ctx, id)
	if err != nil {
		http.Error(w, "failed to load employees", http.StatusInternalServerError)
		return
	}

	employees := make([]domain.Employee, len(empRows))
	for i, e := range empRows {
		employees[i] = domain.Employee{
			ID:              e.ID,
			CompanyID:       e.CompanyID,
			Name:            db.Deref(e.Name),
			Role:            e.Role,
			Specialties:     db.Deref(e.Specialties),
			Type:            e.Type,
			Status:          e.Status,
			SlackUserID:     db.Deref(e.SlackUserID),
			ContainerID:     db.Deref(e.ContainerID),
			ContainerStatus: db.Deref(e.ContainerStatus),
			CreatedAt:       db.PgTime(e.CreatedAt),
		}
	}

	flashMsg := r.URL.Query().Get("msg")

	policyDoc, _ := h.q().GetCompanyPolicy(ctx, id)

	var financials *domain.CompanyFinancials
	fin, err := h.q().GetCompanyFinancials(ctx, id)
	if err == nil {
		financials = &domain.CompanyFinancials{
			ID:              fin.ID,
			CompanyID:       fin.CompanyID,
			BankName:        db.Deref(fin.BankName),
			AccountName:     db.Deref(fin.AccountName),
			BSB:             db.Deref(fin.Bsb),
			AccountNumber:   db.Deref(fin.AccountNumber),
			SwiftCode:       db.Deref(fin.SwiftCode),
			PaymentProvider: db.Deref(fin.PaymentProvider),
			InvoicePrefix:   db.Deref(fin.InvoicePrefix),
			InvoiceCurrency: db.Deref(fin.Currency),
			CreatedAt:       db.PgTime(fin.CreatedAt),
		}
		if fin.InvoiceCounter != nil {
			financials.InvoiceCounter = int(*fin.InvoiceCounter)
		}
	}

	// Load proposed hires.
	var proposedHires []pages.ProposedHireRow
	if hires, phErr := h.q().ListProposedHiresByCompany(ctx, id); phErr == nil {
		for _, ph := range hires {
			proposedHires = append(proposedHires, pages.ProposedHireRow{
				ID:           ph.ID,
				Role:         ph.Role,
				Department:   db.Deref(ph.Department),
				Rationale:    db.Deref(ph.Rationale),
				ReportsTo:    db.Deref(ph.ReportsTo),
				Status:       ph.Status,
				ProposerName: ph.ProposerName,
				CreatedAt:    db.PgTime(ph.CreatedAt),
			})
		}
	}

	templ.Handler(pages.CompanyShow(company, employees, flashMsg, policyDoc, financials, proposedHires)).ServeHTTP(w, r)
}

// GetPolicy handles GET /companies/{id}/policy.
func (h *CompanyHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	policyDoc, err := h.q().GetCompanyPolicy(r.Context(), id)
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

	fin, err := h.q().GetCompanyFinancials(ctx, id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"financials": nil})
		return
	}

	result := domain.CompanyFinancials{
		ID:              fin.ID,
		CompanyID:       fin.CompanyID,
		BankName:        db.Deref(fin.BankName),
		AccountName:     db.Deref(fin.AccountName),
		BSB:             db.Deref(fin.Bsb),
		AccountNumber:   db.Deref(fin.AccountNumber),
		SwiftCode:       db.Deref(fin.SwiftCode),
		PaymentProvider: db.Deref(fin.PaymentProvider),
		InvoicePrefix:   db.Deref(fin.InvoicePrefix),
		InvoiceCurrency: db.Deref(fin.Currency),
		CreatedAt:       db.PgTime(fin.CreatedAt),
	}
	if fin.InvoiceCounter != nil {
		result.InvoiceCounter = int(*fin.InvoiceCounter)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"financials": result})
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

	err := h.q().UpsertCompanyFinancials(ctx, dao.UpsertCompanyFinancialsParams{
		ID:              newID(),
		CompanyID:       id,
		BankName:        db.Ref(bankName),
		AccountName:     db.Ref(accountName),
		Bsb:             db.Ref(bsb),
		AccountNumber:   db.Ref(accountNumber),
		SwiftCode:       db.Ref(swiftCode),
		PaymentProvider: db.Ref(paymentProvider),
		InvoicePrefix:   db.Ref(invoicePrefix),
		Currency:        db.Ref(currency),
	})
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

	err := h.q().UpdateCompanyPolicy(ctx, dao.UpdateCompanyPolicyParams{
		ID:        id,
		PolicyDoc: db.Ref(content),
	})
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
