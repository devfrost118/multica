package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/daemon/repocache"
	"github.com/multica-ai/multica/server/pkg/agent"
)

// HealthResponse is returned by the daemon's local health endpoint.
type HealthResponse struct {
	Status          string            `json:"status"`
	PID             int               `json:"pid"`
	Uptime          string            `json:"uptime"`
	DaemonID        string            `json:"daemon_id"`
	DeviceName      string            `json:"device_name"`
	ServerURL       string            `json:"server_url"`
	CLIVersion      string            `json:"cli_version"`
	ActiveTaskCount int64             `json:"active_task_count"`
	Agents          []string          `json:"agents"`
	Workspaces      []healthWorkspace `json:"workspaces"`
}

type healthWorkspace struct {
	ID       string   `json:"id"`
	Runtimes []string `json:"runtimes"`
}

// listenHealth binds the health port. Returns the listener or an error if
// another daemon is already running (port taken).
func (d *Daemon) listenHealth() (net.Listener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", d.cfg.HealthPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("another daemon is already running on %s: %w", addr, err)
	}
	return ln, nil
}

// repoCheckoutRequest is the body of a POST /repo/checkout request.
type repoCheckoutRequest struct {
	URL         string `json:"url"`
	WorkspaceID string `json:"workspace_id"`
	WorkDir     string `json:"workdir"`
	Ref         string `json:"ref,omitempty"`
	AgentName   string `json:"agent_name"`
	TaskID      string `json:"task_id"`
}

// healthHandler returns the /health HTTP handler. Extracted from serveHealth
// so tests can exercise it without spinning up a listener.
func (d *Daemon) healthHandler(startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		var wsList []healthWorkspace
		for id, ws := range d.workspaces {
			wsList = append(wsList, healthWorkspace{
				ID:       id,
				Runtimes: ws.runtimeIDs,
			})
		}
		d.mu.Unlock()

		agents := make([]string, 0, len(d.cfg.Agents))
		for name := range d.cfg.Agents {
			agents = append(agents, name)
		}

		// "starting" until preflight (PAT renew + initial workspace sync +
		// runtime registration) completes; "running" once the daemon can
		// actually claim tasks. The health port is bound before preflight for
		// liveness/diagnostics, so callers must not treat a reachable endpoint
		// as ready — they gate on this status. Consumers that only know
		// "running" (older CLI/desktop) safely treat "starting" as not-ready.
		status := "starting"
		if d.ready.Load() {
			status = "running"
		}

		resp := HealthResponse{
			Status:          status,
			PID:             os.Getpid(),
			Uptime:          time.Since(startedAt).Truncate(time.Second).String(),
			DaemonID:        d.cfg.DaemonID,
			DeviceName:      d.cfg.DeviceName,
			ServerURL:       d.cfg.ServerBaseURL,
			CLIVersion:      d.cfg.CLIVersion,
			ActiveTaskCount: d.activeTasks.Load(),
			Agents:          agents,
			Workspaces:      wsList,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// shutdownHandler triggers a graceful daemon shutdown by cancelling the
// top-level context. Used by `multica daemon stop` so we don't depend on
// OS-signal delivery, which is unreliable on Windows once the daemon is
// spawned with DETACHED_PROCESS (no shared console with the stop caller).
// The listener is bound to 127.0.0.1 only, so only local processes can hit
// this endpoint.
func (d *Daemon) shutdownHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "shutting down"})
		if d.cancelFunc != nil {
			// Cancel asynchronously so the response flushes first; otherwise
			// srv.Close() races with the writer.
			go d.cancelFunc()
		}
	}
}

// scoutRunRequest is the body of POST /scout/run.
type scoutRunRequest struct {
	TaskID string `json:"task_id"`
	Nonce  string `json:"nonce"`
	Prompt string `json:"prompt"`
}

// scoutRunResponse is the body returned by POST /scout/run on success.
type scoutRunResponse struct {
	Output   string           `json:"output"`
	Truncated bool            `json:"truncated,omitempty"`
	Usage    []TaskUsageEntry `json:"usage,omitempty"`
}

// scoutRunHandler returns the POST /scout/run HTTP handler.
//
// The endpoint:
//  1. Validates the task_id + nonce against the daemon's scout registry.
//  2. Validates the prompt length cap (scoutPromptCap).
//  3. Acquires the daemon scout semaphore (non-blocking; 503 when full).
//  4. Picks the first configured scout-capable provider (claude or codex).
//  5. Runs the scout agent in the parent task's workdir with a 5-minute timeout.
//  6. Caps the response digest at scoutDigestCap bytes.
//  7. Appends the scout's token usage to the parent task's registry entry.
//
// The workdir is NEVER accepted from the request body — it always comes from
// the registry entry (no workdir spoofing).
func (d *Daemon) scoutRunHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req scoutRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.TaskID == "" {
			http.Error(w, "task_id is required", http.StatusBadRequest)
			return
		}
		if req.Nonce == "" {
			http.Error(w, "nonce is required", http.StatusBadRequest)
			return
		}

		// Prompt length cap.
		if len(req.Prompt) > scoutPromptCap {
			http.Error(w, fmt.Sprintf("prompt exceeds %d byte limit", scoutPromptCap), http.StatusRequestEntityTooLarge)
			return
		}

		// Validate task_id + nonce against the registry.
		d.scoutMu.Lock()
		entry, ok := d.scoutRegistry[req.TaskID]
		d.scoutMu.Unlock()
		if !ok {
			http.Error(w, "unknown task_id", http.StatusNotFound)
			return
		}
		if entry.Nonce != req.Nonce {
			http.Error(w, "invalid nonce", http.StatusUnauthorized)
			return
		}

		// Acquire daemon scout semaphore (non-blocking).
		select {
		case d.scoutSem <- struct{}{}:
		default:
			http.Error(w, "scout semaphore full: try again later", http.StatusServiceUnavailable)
			return
		}
		defer func() { <-d.scoutSem }()

		// Find the first scout-capable provider configured in this daemon.
		scoutProvider, scoutAgentEntry, found := d.firstScoutProvider()
		if !found {
			http.Error(w, "no scout-capable provider (claude or codex) configured", http.StatusServiceUnavailable)
			return
		}

		// Derive a scout-scoped logger.
		scoutLog := d.logger.With(
			slog.String("scout_task", shortID(req.TaskID)),
			slog.String("provider", scoutProvider),
		)

		// Build the scout agent's prompt: scout instructions + caller's query.
		scoutPrompt := buildScoutPrompt(entry.Scout, req.Prompt)

		// Build the scout agent's environment. The scout runs with the daemon's
		// own auth token (task-scoped tokens are not available here) and the
		// parent task's workspace ID so it can make Multica API calls.
		scoutEnv := map[string]string{
			"MULTICA_TOKEN":         d.client.Token(),
			"MULTICA_SERVER_URL":    d.cfg.ServerBaseURL,
			"MULTICA_DAEMON_PORT":   fmt.Sprintf("%d", d.cfg.HealthPort),
			"MULTICA_WORKSPACE_ID":  entry.WorkspaceID,
			"MULTICA_SCOUT_TASK_ID": req.TaskID,
			"MULTICA_SCOUT_TOKEN":   req.Nonce,
		}
		// Prepend the daemon binary's directory to PATH so `multica` commands
		// inside the scout agent always resolve correctly (same as runTask).
		if selfBin, err := os.Executable(); err == nil {
			binDir := filepath.Dir(selfBin)
			scoutEnv["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")
		}

		scoutBackend, err := agent.New(scoutProvider, agent.Config{
			ExecutablePath: scoutAgentEntry.Path,
			Env:            scoutEnv,
			Logger:         scoutLog,
		})
		if err != nil {
			scoutLog.Error("scout: failed to create agent backend", "error", err)
			http.Error(w, "internal error: could not create scout agent", http.StatusInternalServerError)
			return
		}

		// Run the scout with a hard 5-minute deadline.
		// Prompt/custom_env are not logged in full — only byte counts.
		scoutLog.Debug("scout: running",
			"workdir", entry.CallerWorkDir,
			"prompt_bytes", len(scoutPrompt),
		)
		runCtx, cancel := context.WithTimeout(r.Context(), scoutTimeout)
		defer cancel()

		result, _, runErr := d.executeAndDrain(runCtx, scoutBackend, scoutPrompt, agent.ExecOptions{
			Cwd:     entry.CallerWorkDir,
			Timeout: scoutTimeout,
		}, scoutLog, req.TaskID)
		if runErr != nil {
			scoutLog.Warn("scout: executeAndDrain error (non-fatal for parent task)", "error", runErr)
			http.Error(w, "scout execution error: "+runErr.Error(), http.StatusInternalServerError)
			return
		}

		scoutLog.Debug("scout: finished", "status", result.Status, "output_bytes", len(result.Output))

		// Accumulate scout usage into the parent task's registry entry.
		if len(result.Usage) > 0 {
			var usageEntries []TaskUsageEntry
			for model, u := range result.Usage {
				if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 {
					continue
				}
				usageEntries = append(usageEntries, TaskUsageEntry{
					Provider:         scoutProvider,
					Model:            model,
					InputTokens:      u.InputTokens,
					OutputTokens:     u.OutputTokens,
					CacheReadTokens:  u.CacheReadTokens,
					CacheWriteTokens: u.CacheWriteTokens,
				})
			}
			entry.appendUsage(usageEntries)
		}

		// Digest cap: truncate output if it exceeds scoutDigestCap bytes.
		output := result.Output
		truncated := false
		if len(output) > scoutDigestCap {
			// Truncate at a UTF-8 boundary.
			output = truncateAtRune(output, scoutDigestCap)
			truncated = true
		}

		resp := scoutRunResponse{
			Output:    output,
			Truncated: truncated,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// firstScoutProvider returns the first scout-capable provider (claude or codex)
// that is configured in the daemon, along with its AgentEntry.
// The search order prefers "claude" over "codex" for consistency.
func (d *Daemon) firstScoutProvider() (string, AgentEntry, bool) {
	for _, p := range []string{"claude", "codex"} {
		if e, ok := d.cfg.Agents[p]; ok {
			return p, e, true
		}
	}
	return "", AgentEntry{}, false
}

// buildScoutPrompt combines the scout agent's instructions and the caller's
// query into a single prompt string. The instructions are presented as a
// system-level preamble so the scout knows its identity before seeing the task.
func buildScoutPrompt(scout *TaskScoutData, callerQuery string) string {
	if scout == nil {
		return callerQuery
	}
	instructions := strings.TrimSpace(scout.Instructions)
	query := strings.TrimSpace(callerQuery)
	if instructions == "" {
		return query
	}
	if query == "" {
		return instructions
	}
	return instructions + "\n\n---\n\n" + query
}

// truncateAtRune truncates s to at most maxBytes bytes, cutting at the last
// valid UTF-8 rune boundary at or before the limit. This avoids splitting
// a multi-byte character at the cap boundary.
func truncateAtRune(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := 0
	for n < len(s) {
		_, size := utf8.DecodeRuneInString(s[n:])
		if n+size > maxBytes {
			break
		}
		n += size
	}
	return s[:n]
}

// serveHealth runs the health HTTP server on the given listener.
// Blocks until ctx is cancelled.
func (d *Daemon) serveHealth(ctx context.Context, ln net.Listener, startedAt time.Time) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", d.healthHandler(startedAt))
	mux.HandleFunc("/shutdown", d.shutdownHandler())
	mux.HandleFunc("/scout/run", d.scoutRunHandler())

	mux.HandleFunc("/repo/checkout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req repoCheckoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}
		if req.WorkspaceID == "" {
			http.Error(w, "workspace_id is required", http.StatusBadRequest)
			return
		}
		if req.WorkDir == "" {
			http.Error(w, "workdir is required", http.StatusBadRequest)
			return
		}

		if d.repoCache == nil {
			http.Error(w, "repo cache not initialized", http.StatusInternalServerError)
			return
		}

		if err := d.ensureRepoReady(r.Context(), req.WorkspaceID, req.URL); err != nil {
			statusCode := http.StatusInternalServerError
			if errors.Is(err, ErrRepoNotConfigured) {
				statusCode = http.StatusBadRequest
			}
			d.logger.Error("repo checkout readiness failed", "workspace_id", req.WorkspaceID, "url", req.URL, "error", err)
			http.Error(w, err.Error(), statusCode)
			return
		}

		result, err := d.repoCache.CreateWorktree(repocache.WorktreeParams{
			WorkspaceID:         req.WorkspaceID,
			RepoURL:             req.URL,
			WorkDir:             req.WorkDir,
			Ref:                 req.Ref,
			AgentName:           req.AgentName,
			TaskID:              req.TaskID,
			CoAuthoredByEnabled: d.workspaceCoAuthoredByEnabled(req.WorkspaceID),
		})
		if err != nil {
			d.logger.Error("repo checkout failed", "url", req.URL, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	d.logger.Info("health server listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		d.logger.Warn("health server error", "error", err)
	}
}
