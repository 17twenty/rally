package handlers

import (
	"net/http"
	"strings"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/domain"
	"github.com/17twenty/rally/internal/kb"
	"github.com/17twenty/rally/templates/pages"
	"github.com/a-h/templ"
)

// KBHandler holds dependencies for knowledgebase HTTP handlers.
type KBHandler struct {
	DB    *db.DB
	Store *kb.KBStore
}

// NewKBHandler creates a KBHandler wiring up the store from the DB pool.
func NewKBHandler(database *db.DB) *KBHandler {
	h := &KBHandler{DB: database}
	if database != nil {
		h.Store = &kb.KBStore{DB: database.Pool}
	}
	return h
}

// List handles GET /kb — shows all KB entries for a company.
func (h *KBHandler) List(w http.ResponseWriter, r *http.Request) {
	companyID := r.URL.Query().Get("company_id")
	ctx := r.Context()

	var entries []domain.KnowledgebaseEntry
	if h.Store != nil && companyID != "" {
		var err error
		entries, err = h.Store.GetAll(ctx, companyID)
		if err != nil {
			http.Error(w, "failed to load entries", http.StatusInternalServerError)
			return
		}
	}

	templ.Handler(pages.KBList(companyID, entries)).ServeHTTP(w, r)
}

// Create handles POST /kb — proposes a new KB entry.
func (h *KBHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	companyID := r.FormValue("company_id")
	title := r.FormValue("title")
	content := r.FormValue("content")
	tagsRaw := r.FormValue("tags")

	if companyID == "" || title == "" || content == "" {
		http.Error(w, "company_id, title, and content are required", http.StatusBadRequest)
		return
	}

	var tags []string
	for _, t := range strings.Split(tagsRaw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}

	entry := domain.KnowledgebaseEntry{
		ID:        newID(),
		CompanyID: companyID,
		Title:     title,
		Content:   content,
		Tags:      tags,
	}

	if h.Store != nil {
		if err := h.Store.Save(r.Context(), entry); err != nil {
			http.Error(w, "failed to save entry", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/kb?company_id="+companyID, http.StatusSeeOther)
}

// Approve handles POST /kb/{id}/approve — approves a pending KB entry.
func (h *KBHandler) Approve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	approvedBy := r.FormValue("approved_by")
	if approvedBy == "" {
		approvedBy = "admin"
	}

	companyID := r.FormValue("company_id")

	if h.Store != nil {
		if err := h.Store.Approve(r.Context(), id, approvedBy); err != nil {
			http.Error(w, "failed to approve entry", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/kb?company_id="+companyID, http.StatusSeeOther)
}
