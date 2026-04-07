package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/17twenty/rally/internal/container"
	"github.com/17twenty/rally/internal/db"
	"github.com/17twenty/rally/internal/handlers"
	"github.com/17twenty/rally/internal/llm"
	"github.com/17twenty/rally/internal/observability"
	"github.com/17twenty/rally/internal/queue"
	"github.com/17twenty/rally/internal/slack"
	"github.com/17twenty/rally/internal/tools"
	"github.com/17twenty/rally/internal/vault"
	"github.com/17twenty/rally/internal/workspace"
)

func main() {
	mux := http.NewServeMux()

	// Static assets
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))

	// Open DB (required).
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	database, err := db.Open(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	slog.Info("database connected")

	// LLM router — must be created before queue so workers can use it.
	llmRouter := llm.NewDefaultRouter()

	// Initialize credential vault (plaintext storage — DB is the security boundary)
	credVault := vault.NewVault(database.Pool)

	// Container manager for AE provisioning
	var containerMgr *container.Manager
	containerMgr, err = container.NewManager(
		os.Getenv("RALLY_WORKSPACE_ROOT"),
		os.Getenv("RALLY_API_URL"),
	)
	if err != nil {
		slog.Warn("container manager init failed — AE containers disabled", "err", err)
	} else {
		if netErr := containerMgr.EnsureNetwork(context.Background()); netErr != nil {
			slog.Warn("docker network setup failed", "err", netErr)
		}
		slog.Info("container manager initialized")
	}

	// Initialize job queue (needs LLM router + container manager)
	// Slack client (used by workers and Google Workspace tool)
	// Priority: env var > vault stored token
	var slackClient *slack.SlackClient
	if slackToken := os.Getenv("SLACK_BOT_TOKEN"); slackToken != "" {
		slackClient = slack.NewClient(slackToken)
	} else if credVault != nil {
		// Try loading from vault (stored via OAuth flow).
		if token, err := credVault.Get(context.Background(), "rally-system", "slack"); err == nil && token != "" {
			slackClient = slack.NewClient(token)
			slog.Info("slack: loaded bot token from vault")
		}
	}

	// Workspace store (used by workers)
	wsStore := &workspace.WorkspaceStore{DB: database.Pool}

	if _, err = queue.InitQueue(context.Background(), database.Pool, queue.WorkerDeps{
		LLMRouter:        llmRouter,
		ContainerManager: containerMgr,
		WorkspaceStore:   wsStore,
		SlackClient:      slackClient,
	}); err != nil {
		log.Fatalf("queue.InitQueue: %v", err)
	}
	slog.Info("job queue initialized", "client_nil", queue.Client == nil)

	// Setup wizard (first-time bootstrap)
	setupH := &handlers.SetupHandler{DB: database, Vault: credVault}
	mux.HandleFunc("GET /setup", setupH.Show)
	mux.HandleFunc("POST /setup", setupH.Create)

	// Dashboard
	dh := &handlers.DashboardHandler{DB: database}
	mux.HandleFunc("GET /", dh.Show)

	// Company routes (InvoiceTool set after googleWorkspaceTool is initialized below)
	ch := &handlers.CompanyHandler{DB: database, LLMRouter: llmRouter, ContainerManager: containerMgr}
	mux.HandleFunc("GET /companies", ch.List)
	mux.HandleFunc("GET /companies/new", ch.New)
	mux.HandleFunc("POST /companies", ch.Create)
	mux.HandleFunc("GET /companies/{id}", ch.Show)
	mux.HandleFunc("POST /companies/{id}/build", ch.Build)
	mux.HandleFunc("POST /companies/{id}/nuke", ch.Nuke)
	mux.HandleFunc("GET /companies/{id}/status", ch.Status)
	mux.HandleFunc("GET /companies/{id}/policy", ch.GetPolicy)
	mux.HandleFunc("POST /companies/{id}/policy", ch.SetPolicy)
	mux.HandleFunc("GET /companies/{id}/financials", ch.GetFinancials)
	mux.HandleFunc("POST /companies/{id}/financials", ch.SetFinancials)
	mux.HandleFunc("POST /companies/{id}/invoice", ch.CreateInvoice)

	// Agent routes
	ah := &handlers.AgentHandler{DB: database}
	mux.HandleFunc("GET /agents", ah.List)
	mux.HandleFunc("GET /agents/{id}", ah.Detail)
	mux.HandleFunc("GET /logs", ah.Logs)

	// AE API handler — shared by AE containers and chat UI.
	aeAPI := &handlers.AEAPIHandler{DB: database, LLMRouter: llmRouter, Vault: credVault, SlackClient: slackClient, WorkspaceStore: wsStore}

	// Chat routes
	chh := &handlers.ChatHandler{DB: database, AE: aeAPI}
	mux.HandleFunc("GET /chat", chh.Show)
	mux.HandleFunc("POST /chat/message", chh.Message)
	mux.HandleFunc("GET /chat/history", chh.History)

	// Task routes
	th := &handlers.TaskHandler{DB: database}
	mux.HandleFunc("GET /tasks", th.List)
	mux.HandleFunc("GET /tasks/new", th.New)
	mux.HandleFunc("POST /tasks", th.Create)
	mux.HandleFunc("GET /tasks/{id}", th.Show)
	mux.HandleFunc("POST /tasks/{id}/status", th.UpdateStatus)
	mux.HandleFunc("POST /work-items/{id}/status", th.UpdateWorkItemStatus)
	mux.HandleFunc("POST /work-items/{id}/delete", th.DeleteWorkItem)

	// KB routes
	kbh := handlers.NewKBHandler(database)
	mux.HandleFunc("GET /kb", kbh.List)
	mux.HandleFunc("POST /kb", kbh.Create)
	mux.HandleFunc("POST /kb/{id}/approve", kbh.Approve)

	// Workspace routes
	wsh := handlers.NewWorkspaceHandler(database)
	mux.HandleFunc("GET /workspace", wsh.List)
	mux.HandleFunc("GET /workspace/{id}", wsh.Detail)
	mux.HandleFunc("POST /workspace", wsh.Create)
	mux.HandleFunc("POST /workspace/{id}/approve", wsh.Approve)
	mux.HandleFunc("POST /workspace/{id}/comment", wsh.AddComment)

	// Credential routes
	credh := &handlers.CredentialHandler{DB: database, Vault: credVault}
	mux.HandleFunc("GET /credentials", credh.List)
	mux.HandleFunc("POST /credentials", credh.Store)
	mux.HandleFunc("POST /credentials/{id}/revoke", credh.Revoke)
	mux.HandleFunc("GET /credentials/audit", credh.AuditLog)

	// Google Workspace tool (shared instance; per-AE EmployeeID set at dispatch time)
	var googleWorkspaceTool *tools.GoogleWorkspaceTool
	if credVault != nil {
		googleWorkspaceTool = &tools.GoogleWorkspaceTool{
			Vault:       credVault,
			SlackClient: slackClient,
		}
	}
	_ = googleWorkspaceTool // referenced by gateway at dispatch time

	// Invoice tool — wired after googleWorkspaceTool is available.
	if database != nil {
		ch.InvoiceTool = &tools.InvoiceTool{
			DB:                  database.Pool,
			GoogleWorkspaceTool: googleWorkspaceTool,
		}
	}

	// Google OAuth routes
	googleOAuthH := &handlers.GoogleOAuthHandler{Vault: credVault}
	mux.HandleFunc("GET /oauth/google", googleOAuthH.Authorize)
	mux.HandleFunc("GET /oauth/google/callback", googleOAuthH.Callback)

	// Figma OAuth routes
	figmaOAuthH := &handlers.FigmaOAuthHandler{Vault: credVault}
	mux.HandleFunc("GET /oauth/figma", figmaOAuthH.Authorize)
	mux.HandleFunc("GET /oauth/figma/callback", figmaOAuthH.Callback)

	// Slack routes
	sh := &handlers.SlackHandler{DB: database}
	mux.HandleFunc("POST /slack/events", sh.Events)

	oauthH := &handlers.SlackOAuthHandler{DB: database, Vault: credVault, SlackClient: &slackClient}
	mux.HandleFunc("GET /slack/oauth/callback", oauthH.OAuthCallback)
	mux.HandleFunc("GET /slack/install", oauthH.Install)

	// AE API routes (used by AE agent containers)
	aeAuth := func(h http.HandlerFunc) http.Handler {
		return handlers.AEAuthMiddleware(database, http.HandlerFunc(h))
	}
	mux.Handle("POST /api/ae/register", aeAuth(aeAPI.Register))
	mux.Handle("POST /api/ae/heartbeat", aeAuth(aeAPI.Heartbeat))
	mux.Handle("GET /api/ae/observations", aeAuth(aeAPI.Observations))
	mux.Handle("POST /api/ae/llm/complete", aeAuth(aeAPI.LLMComplete))
	mux.Handle("POST /api/ae/llm/chat", aeAuth(aeAPI.LLMChat))
	mux.Handle("POST /api/ae/slack/send", aeAuth(aeAPI.SlackSend))
	mux.Handle("POST /api/ae/memory", aeAuth(aeAPI.StoreMemory))
	mux.Handle("GET /api/ae/memory/search", aeAuth(aeAPI.SearchMemory))
	mux.Handle("POST /api/ae/logs", aeAuth(aeAPI.SubmitLog))
	mux.Handle("GET /api/ae/credentials", aeAuth(aeAPI.ListCredentials))
	mux.Handle("GET /api/ae/credentials/{provider}", aeAuth(aeAPI.FetchCredential))
	mux.Handle("POST /api/ae/tools/execute", aeAuth(aeAPI.ExecuteTool))
	mux.Handle("GET /api/ae/tools/list", aeAuth(aeAPI.ListTools))
	mux.Handle("GET /api/ae/backlog", aeAuth(aeAPI.BacklogList))
	mux.Handle("POST /api/ae/backlog", aeAuth(aeAPI.BacklogAdd))
	mux.Handle("PATCH /api/ae/backlog/{id}", aeAuth(aeAPI.BacklogUpdate))
	mux.Handle("POST /api/ae/delegate", aeAuth(aeAPI.Delegate))
	mux.Handle("POST /api/ae/escalate", aeAuth(aeAPI.Escalate))
	mux.Handle("POST /api/ae/messages", aeAuth(aeAPI.AESendMessage))
	mux.Handle("PATCH /api/ae/tasks/{id}", aeAuth(aeAPI.UpdateTask))
	mux.Handle("POST /api/ae/propose-hire", aeAuth(aeAPI.ProposeHire))
	mux.Handle("GET /api/ae/team", aeAuth(aeAPI.ListTeam))

	// Hire approval (web UI, not AE auth)
	mux.HandleFunc("POST /companies/{id}/hires/{hire_id}/approve", aeAPI.ApproveHire)
	mux.HandleFunc("POST /companies/{id}/hires/{hire_id}/reject", aeAPI.RejectHire)

	// Wrap all handlers with request logger middleware
	handler := observability.RequestLogger(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8432"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Graceful shutdown on SIGINT/SIGTERM
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("starting server", "addr", addr)
		slog.Info("tip: if this is your first time, visit /setup to bootstrap Rally for Rally")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	<-stop
	slog.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if queue.Client != nil {
		if err := queue.Client.Stop(ctx); err != nil {
			slog.Warn("queue stop error", "err", err)
		}
	}

	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("server shutdown error", "err", err)
	}

	slog.Info("server stopped")
}
