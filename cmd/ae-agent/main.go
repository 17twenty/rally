package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	// Required env vars
	rallyURL := requireEnv("RALLY_API_URL")
	apiToken := requireEnv("RALLY_API_TOKEN")
	employeeID := requireEnv("EMPLOYEE_ID")
	companyID := requireEnv("COMPANY_ID")
	aeName := os.Getenv("AE_NAME")
	aeRole := os.Getenv("AE_ROLE")
	soulMD := os.Getenv("SOUL_MD")

	// Parse config for model ref and heartbeat interval.
	// Fallback model only used if AE_CONFIG is missing; hiring flow sets the real value.
	modelRef := envOr("DEFAULT_MODEL", "greenthread-gpt-oss-120b")
	heartbeatSeconds := 300
	if cfgJSON := os.Getenv("AE_CONFIG"); cfgJSON != "" {
		var cfg struct {
			Cognition struct {
				DefaultModelRef string `json:"DefaultModelRef"`
			}
			Runtime struct {
				HeartbeatSeconds int `json:"HeartbeatSeconds"`
			}
		}
		if err := json.Unmarshal([]byte(cfgJSON), &cfg); err == nil {
			if cfg.Cognition.DefaultModelRef != "" {
				modelRef = cfg.Cognition.DefaultModelRef
			}
			if cfg.Runtime.HeartbeatSeconds > 0 {
				heartbeatSeconds = cfg.Runtime.HeartbeatSeconds
			}
		}
	}

	// Allow override via env
	if s := os.Getenv("HEARTBEAT_SECONDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			heartbeatSeconds = v
		}
	}

	maxTurns := 25
	if s := os.Getenv("AE_MAX_TURNS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			maxTurns = v
		}
	}

	slog.Info("ae-agent starting",
		"employee_id", employeeID,
		"name", aeName,
		"role", aeRole,
		"model", modelRef,
		"heartbeat_seconds", heartbeatSeconds,
		"max_turns", maxTurns,
	)

	rally := NewRallyClient(rallyURL, apiToken, employeeID, companyID)

	// Register with Rally
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := rally.Register(ctx); err != nil {
		slog.Warn("register failed (Rally may not be ready yet)", "err", err)
	}

	// Fetch remote tool definitions from Rally.
	var remoteToolDefs []RemoteToolDef
	if defs, err := rally.FetchToolDefinitions(ctx); err != nil {
		slog.Warn("failed to fetch remote tools (will use local only)", "err", err)
	} else {
		remoteToolDefs = defs
		slog.Info("loaded remote tools", "count", len(remoteToolDefs))
	}

	// Start health server
	go startHealthServer()

	// Set up the cycle runner
	cycle := &AgentCycle{
		Rally: rally,
		LocalTools: &LocalToolDispatcher{
			WorkspacePath: envOr("WORKSPACE_PATH", "/workspace"),
			ScratchPath:   envOr("SCRATCH_PATH", "/home/ae/scratch"),
		},
		SoulMD:         soulMD,
		AEName:         aeName,
		AERole:         aeRole,
		ModelRef:       modelRef,
		MaxTurns:       maxTurns,
		RemoteToolDefs: remoteToolDefs,
		ScratchPath:    envOr("SCRATCH_PATH", "/home/ae/scratch"),
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Main heartbeat loop
	ticker := time.NewTicker(time.Duration(heartbeatSeconds) * time.Second)
	defer ticker.Stop()

	cycleCount := 0

	// Run first cycle immediately
	runCycle(ctx, cycle, rally, &cycleCount)

	for {
		select {
		case <-ticker.C:
			runCycle(ctx, cycle, rally, &cycleCount)
		case <-stop:
			slog.Info("ae-agent shutting down")
			cancel()
			return
		}
	}
}

func runCycle(ctx context.Context, cycle *AgentCycle, rally *RallyClient, count *int) {
	*count++
	slog.Info("heartbeat cycle", "cycle", *count)

	if err := rally.Heartbeat(ctx, *count); err != nil {
		slog.Warn("heartbeat report failed", "err", err)
	}

	if err := cycle.Run(ctx); err != nil {
		slog.Warn("cycle error", "err", err)
	}
}

func startHealthServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	if err := http.ListenAndServe(":9090", mux); err != nil {
		slog.Warn("health server error", "err", err)
	}
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
