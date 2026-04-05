package handlers

import (
	"net/http"

	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/workspace"
	"github.com/17twenty/rally/templates/pages"
	"github.com/a-h/templ"
)

// WorkspaceHandler holds dependencies for workspace HTTP handlers.
type WorkspaceHandler struct {
	DB    *db.DB
	Store *workspace.WorkspaceStore
}

// NewWorkspaceHandler creates a WorkspaceHandler wiring up the store from the DB pool.
func NewWorkspaceHandler(database *db.DB) *WorkspaceHandler {
	h := &WorkspaceHandler{DB: database}
	if database != nil {
		h.Store = &workspace.WorkspaceStore{DB: database.Pool}
	}
	return h
}

// List handles GET /workspace — shows all files for a company.
func (h *WorkspaceHandler) List(w http.ResponseWriter, r *http.Request) {
	companyID := r.URL.Query().Get("company_id")
	ctx := r.Context()

	var files []workspace.WorkspaceFile
	if h.Store != nil && companyID != "" {
		var err error
		files, err = h.Store.ListFiles(ctx, companyID, "")
		if err != nil {
			http.Error(w, "failed to load files", http.StatusInternalServerError)
			return
		}
	}

	templ.Handler(pages.WorkspaceList(companyID, files)).ServeHTTP(w, r)
}

// Detail handles GET /workspace/{id} — shows file detail with comments and versions.
func (h *WorkspaceHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	if h.Store == nil {
		http.Error(w, "store unavailable", http.StatusServiceUnavailable)
		return
	}

	f, err := h.Store.GetFile(ctx, id)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	comments, _ := h.Store.GetComments(ctx, id)
	versions, _ := h.Store.GetVersions(ctx, id)

	templ.Handler(pages.WorkspaceDetail(*f, comments, versions)).ServeHTTP(w, r)
}

// Create handles POST /workspace — creates a new file with status='pending'.
func (h *WorkspaceHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	companyID := r.FormValue("company_id")
	path := r.FormValue("path")
	title := r.FormValue("title")
	content := r.FormValue("content")
	createdBy := r.FormValue("created_by")
	if createdBy == "" {
		createdBy = "admin"
	}

	if companyID == "" || path == "" || content == "" {
		http.Error(w, "company_id, path, and content are required", http.StatusBadRequest)
		return
	}

	f := workspace.WorkspaceFile{
		ID:        newID(),
		CompanyID: companyID,
		Path:      path,
		Title:     title,
		Content:   content,
		MimeType:  "text/plain",
		CreatedBy: createdBy,
	}

	if h.Store != nil {
		if err := h.Store.SaveFile(r.Context(), f); err != nil {
			http.Error(w, "failed to save file", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/workspace?company_id="+companyID, http.StatusSeeOther)
}

// Approve handles POST /workspace/{id}/approve — sets status='active'.
func (h *WorkspaceHandler) Approve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	approvedBy := r.FormValue("approved_by")
	if approvedBy == "" {
		approvedBy = "admin"
	}

	if h.Store != nil {
		if err := h.Store.ApproveFile(ctx, id, approvedBy); err != nil {
			http.Error(w, "failed to approve file", http.StatusInternalServerError)
			return
		}
	}

	// Redirect back to detail or list.
	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = "/workspace"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

// AddComment handles POST /workspace/{id}/comment — adds a comment to a file.
func (h *WorkspaceHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}

	body := r.FormValue("body")
	authorID := r.FormValue("author_id")
	if authorID == "" {
		authorID = "admin"
	}

	if body == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}

	comment := workspace.WorkspaceComment{
		ID:       newID(),
		FileID:   id,
		AuthorID: authorID,
		Body:     body,
	}

	if h.Store != nil {
		if err := h.Store.AddComment(ctx, comment); err != nil {
			http.Error(w, "failed to add comment", http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/workspace/"+id, http.StatusSeeOther)
}
