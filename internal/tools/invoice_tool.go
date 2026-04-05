package tools

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"time"

	"github.com/17twenty/rally/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InvoiceTool generates and sends invoices for a company.
type InvoiceTool struct {
	DB                  *pgxpool.Pool
	GoogleWorkspaceTool *GoogleWorkspaceTool
}

// Execute dispatches invoice actions.
func (t *InvoiceTool) Execute(ctx context.Context, action string, input map[string]any) (map[string]any, error) {
	switch action {
	case "generate_pdf":
		return t.generatePDF(ctx, input)
	case "send_invoice":
		return t.sendInvoice(ctx, input)
	case "list_invoices":
		return map[string]any{
			"invoices": []any{},
			"note":     "Invoice history stored in Workspace files",
		}, nil
	default:
		return nil, fmt.Errorf("invoice: unknown action %q", action)
	}
}

// getNextInvoiceNumber fetches and increments the invoice counter, returning a
// formatted number like "INV-0042" and the company's financial details.
func (t *InvoiceTool) getNextInvoiceNumber(ctx context.Context, companyID string) (string, int, *domain.CompanyFinancials, error) {
	tx, err := t.DB.Begin(ctx)
	if err != nil {
		return "", 0, nil, fmt.Errorf("invoice: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var f domain.CompanyFinancials
	err = tx.QueryRow(ctx, `
		SELECT id, company_id,
		       COALESCE(bank_name,''), COALESCE(account_name,''),
		       COALESCE(bsb,''), COALESCE(account_number,''),
		       COALESCE(swift_code,''), COALESCE(payment_provider,''),
		       COALESCE(invoice_prefix,'INV'), COALESCE(invoice_counter,1),
		       COALESCE(currency,'AUD'), created_at
		FROM company_financials WHERE company_id = $1 FOR UPDATE`, companyID,
	).Scan(
		&f.ID, &f.CompanyID,
		&f.BankName, &f.AccountName,
		&f.BSB, &f.AccountNumber,
		&f.SwiftCode, &f.PaymentProvider,
		&f.InvoicePrefix, &f.InvoiceCounter,
		&f.InvoiceCurrency, &f.CreatedAt,
	)
	if err != nil {
		// No financials row yet — use defaults.
		f = domain.CompanyFinancials{
			CompanyID:       companyID,
			InvoicePrefix:   "INV",
			InvoiceCounter:  1,
			InvoiceCurrency: "AUD",
		}
	}

	counter := f.InvoiceCounter
	invoiceNum := fmt.Sprintf("%s-%04d", f.InvoicePrefix, counter)

	if f.ID != "" {
		_, err = tx.Exec(ctx,
			`UPDATE company_financials SET invoice_counter=invoice_counter+1, updated_at=NOW() WHERE company_id=$1`,
			companyID,
		)
		if err != nil {
			return "", 0, nil, fmt.Errorf("invoice: increment counter: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", 0, nil, fmt.Errorf("invoice: commit tx: %w", err)
	}

	return invoiceNum, counter, &f, nil
}

// generateInvoiceHTML renders the invoice as an HTML string.
func generateInvoiceHTML(inv domain.Invoice, f domain.CompanyFinancials) string {
	const tmplSrc = `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<style>
  body { font-family: Arial, sans-serif; color: #333; margin: 0; padding: 40px; }
  .header { display: flex; justify-content: space-between; margin-bottom: 40px; }
  h1 { font-size: 36px; color: #1a1a1a; margin: 0 0 4px 0; }
  .meta { font-size: 14px; color: #555; margin-bottom: 4px; }
  table { width: 100%; border-collapse: collapse; margin: 24px 0; }
  th { background: #f4f4f4; padding: 10px 12px; text-align: left; font-size: 13px; border-bottom: 2px solid #ddd; }
  td { padding: 10px 12px; font-size: 13px; border-bottom: 1px solid #eee; }
  .amount { text-align: right; }
  .total-row td { font-weight: bold; font-size: 15px; border-top: 2px solid #333; border-bottom: none; }
  .section { margin-top: 32px; }
  .section-title { font-size: 13px; font-weight: bold; color: #888; text-transform: uppercase; margin-bottom: 8px; }
  .bank-detail { font-size: 13px; margin-bottom: 4px; }
  .notes { font-size: 13px; color: #555; white-space: pre-wrap; }
</style>
</head>
<body>
<div class="header">
  <div>
    <h1>INVOICE</h1>
    <div class="meta"><strong>Invoice #:</strong> {{.Invoice.InvoiceNumber}}</div>
    <div class="meta"><strong>Issued:</strong> {{.Invoice.IssuedAt}}</div>
    <div class="meta"><strong>Due:</strong> {{.Invoice.DueAt}}</div>
  </div>
  <div style="text-align:right">
    <div class="meta"><strong>Billed To:</strong></div>
    <div class="meta">{{.Invoice.IssuedTo}}</div>
    <div class="meta">{{.Invoice.IssuedToEmail}}</div>
  </div>
</div>

<table>
  <thead>
    <tr>
      <th>Description</th>
      <th class="amount">Qty</th>
      <th class="amount">Unit Price</th>
      <th class="amount">Amount</th>
    </tr>
  </thead>
  <tbody>
    {{range .Invoice.LineItems}}
    <tr>
      <td>{{.Description}}</td>
      <td class="amount">{{.Quantity}}</td>
      <td class="amount">{{printf "%.2f" .UnitPrice}} {{$.Invoice.Currency}}</td>
      <td class="amount">{{printf "%.2f" .Amount}} {{$.Invoice.Currency}}</td>
    </tr>
    {{end}}
    <tr class="total-row">
      <td colspan="3">Total</td>
      <td class="amount">{{printf "%.2f" .Invoice.TotalAmount}} {{.Invoice.Currency}}</td>
    </tr>
  </tbody>
</table>

{{if or .Financials.BankName .Financials.AccountNumber}}
<div class="section">
  <div class="section-title">Bank Details</div>
  {{if .Financials.BankName}}<div class="bank-detail"><strong>Bank:</strong> {{.Financials.BankName}}</div>{{end}}
  {{if .Financials.AccountName}}<div class="bank-detail"><strong>Account Name:</strong> {{.Financials.AccountName}}</div>{{end}}
  {{if .Financials.BSB}}<div class="bank-detail"><strong>BSB:</strong> {{.Financials.BSB}}</div>{{end}}
  {{if .Financials.AccountNumber}}<div class="bank-detail"><strong>Account Number:</strong> {{.Financials.AccountNumber}}</div>{{end}}
  {{if .Financials.SwiftCode}}<div class="bank-detail"><strong>SWIFT:</strong> {{.Financials.SwiftCode}}</div>{{end}}
</div>
{{end}}

{{if .Invoice.Notes}}
<div class="section">
  <div class="section-title">Notes</div>
  <div class="notes">{{.Invoice.Notes}}</div>
</div>
{{end}}

</body>
</html>`

	tmpl, err := template.New("invoice").Parse(tmplSrc)
	if err != nil {
		return "<html><body><h1>Invoice render error</h1></body></html>"
	}

	data := struct {
		Invoice    domain.Invoice
		Financials domain.CompanyFinancials
	}{Invoice: inv, Financials: f}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "<html><body><h1>Invoice render error</h1></body></html>"
	}
	return buf.String()
}

// buildInvoice constructs a domain.Invoice from the action input map.
func (t *InvoiceTool) buildInvoice(ctx context.Context, input map[string]any) (domain.Invoice, *domain.CompanyFinancials, error) {
	companyID, _ := input["company_id"].(string)
	if companyID == "" {
		return domain.Invoice{}, nil, fmt.Errorf("invoice: company_id required")
	}
	issuedTo, _ := input["issued_to"].(string)
	issuedToEmail, _ := input["issued_to_email"].(string)
	notes, _ := input["notes"].(string)

	dueDays := 30
	if v, ok := input["due_days"].(float64); ok {
		dueDays = int(v)
	}

	invoiceNum, _, financials, err := t.getNextInvoiceNumber(ctx, companyID)
	if err != nil {
		return domain.Invoice{}, nil, err
	}

	now := time.Now()
	issuedAt := now.Format("2006-01-02")
	dueAt := now.AddDate(0, 0, dueDays).Format("2006-01-02")

	var lineItems []domain.InvoiceLineItem
	var total float64

	if rawItems, ok := input["line_items"].([]any); ok {
		for _, rawItem := range rawItems {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			desc, _ := item["description"].(string)
			qty := float64(1)
			if v, ok := item["quantity"].(float64); ok {
				qty = v
			}
			unitPrice := float64(0)
			if v, ok := item["unit_price"].(float64); ok {
				unitPrice = v
			}
			amount := qty * unitPrice
			total += amount
			lineItems = append(lineItems, domain.InvoiceLineItem{
				Description: desc,
				Quantity:    qty,
				UnitPrice:   unitPrice,
				Amount:      amount,
			})
		}
	}

	currency := "AUD"
	if financials != nil && financials.InvoiceCurrency != "" {
		currency = financials.InvoiceCurrency
	}

	inv := domain.Invoice{
		CompanyID:     companyID,
		InvoiceNumber: invoiceNum,
		IssuedTo:      issuedTo,
		IssuedToEmail: issuedToEmail,
		LineItems:     lineItems,
		TotalAmount:   total,
		Currency:      currency,
		Status:        "issued",
		Notes:         notes,
		IssuedAt:      issuedAt,
		DueAt:         dueAt,
	}

	fin := domain.CompanyFinancials{}
	if financials != nil {
		fin = *financials
	}

	return inv, &fin, nil
}

func (t *InvoiceTool) generatePDF(ctx context.Context, input map[string]any) (map[string]any, error) {
	inv, financials, err := t.buildInvoice(ctx, input)
	if err != nil {
		return nil, err
	}

	htmlContent := generateInvoiceHTML(inv, *financials)

	bt := &BrowserTool{}
	result, err := bt.Execute(ctx, "print_pdf", map[string]any{"html": htmlContent})
	if err != nil {
		// Return HTML even if PDF generation fails.
		return map[string]any{
			"invoice_number": inv.InvoiceNumber,
			"html":           htmlContent,
			"pdf_base64":     "",
			"error":          err.Error(),
		}, nil
	}

	pdfBase64, _ := result["pdf_base64"].(string)
	return map[string]any{
		"invoice_number": inv.InvoiceNumber,
		"html":           htmlContent,
		"pdf_base64":     pdfBase64,
	}, nil
}

func (t *InvoiceTool) sendInvoice(ctx context.Context, input map[string]any) (map[string]any, error) {
	pdfResult, err := t.generatePDF(ctx, input)
	if err != nil {
		return nil, err
	}

	invoiceNum, _ := pdfResult["invoice_number"].(string)
	htmlContent, _ := pdfResult["html"].(string)
	issuedToEmail, _ := input["issued_to_email"].(string)
	issuedTo, _ := input["issued_to"].(string)

	companyID, _ := input["company_id"].(string)
	companyName := companyID
	if companyID != "" && t.DB != nil {
		var name string
		if err := t.DB.QueryRow(ctx, `SELECT COALESCE(name,'') FROM companies WHERE id=$1`, companyID).Scan(&name); err == nil {
			companyName = name
		}
	}

	sent := false
	if t.GoogleWorkspaceTool != nil && issuedToEmail != "" {
		subject := fmt.Sprintf("Invoice %s from %s", invoiceNum, companyName)
		summary := fmt.Sprintf("Dear %s,\n\nPlease find your invoice %s attached.\n\n%s",
			issuedTo, invoiceNum, stripHTMLForEmail(htmlContent))
		_, emailErr := t.GoogleWorkspaceTool.Execute(ctx, "send_email", map[string]any{
			"to":      issuedToEmail,
			"subject": subject,
			"body":    summary,
		})
		if emailErr == nil {
			sent = true
		}
	}

	return map[string]any{
		"invoice_number": invoiceNum,
		"sent":           sent,
	}, nil
}

// stripHTMLForEmail provides a minimal plain-text summary from the invoice HTML.
// For simplicity we just return a short description rather than parse the HTML.
func stripHTMLForEmail(html string) string {
	if len(html) > 500 {
		return "(See attached invoice for full details)"
	}
	return html
}
