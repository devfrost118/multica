package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	// scoutMCPServerKey is the managed MCP server name injected into the parent
	// agent's MCP config. Using a fixed name lets callers identify and override
	// the entry and lets the daemon strip stale entries on re-runs.
	scoutMCPServerKey = "multica-scout"

	// scoutPromptCap is the maximum byte length of the prompt accepted by
	// POST /scout/run. Prompts above this cap are rejected with 400.
	scoutPromptCap = 32 * 1024 // 32 KB

	// scoutDigestCap is the maximum byte length of the scout agent's output
	// returned in the /scout/run response. Output above this is truncated.
	scoutDigestCap = 16 * 1024 // 16 KB

	// scoutTimeout is the hard wall-clock deadline imposed on each scout agent
	// execution. The parent agent's MCP call blocks for at most this duration.
	scoutTimeout = 5 * time.Minute

	// scoutMaxConcurrent is the maximum number of simultaneous scout agent
	// executions allowed across the entire daemon. The daemon semaphore is
	// pre-allocated at this size; requests that cannot acquire a slot
	// immediately receive 503.
	scoutMaxConcurrent = 3
)

// scoutEntry is stored in the daemon's per-task scout registry. It holds
// the per-task nonce, the scout agent identity, the parent task's workdir,
// and a usage accumulator so scout token consumption merges into the parent
// task's usage report.
type scoutEntry struct {
	Nonce         string
	Scout         *TaskScoutData
	CallerWorkDir string
	WorkspaceID   string

	// usageMu guards usage. Scout runs are serialised by the semaphore but
	// this mutex handles the edge case where the parent task reads usage
	// while an in-flight scout run is still appending.
	usageMu sync.Mutex
	usage   []TaskUsageEntry
}

// appendUsage adds entries to the accumulated scout usage for this task.
func (e *scoutEntry) appendUsage(entries []TaskUsageEntry) {
	if len(entries) == 0 {
		return
	}
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	e.usage = append(e.usage, entries...)
}

// drainUsage returns the accumulated usage entries and resets the slice.
func (e *scoutEntry) drainUsage() []TaskUsageEntry {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	out := e.usage
	e.usage = nil
	return out
}

// generateScoutNonce returns a 32-byte cryptographically random hex string
// used as the per-task scout authentication token.
func generateScoutNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("scout: rand.Read: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// isCallerProviderForScout reports whether the given provider supports MCP
// injection — i.e. the parent agent running on this provider receives the
// managed multica-scout MCP server entry so it can delegate to the scout.
// opencode is included here even though it cannot itself be a scout provider.
func isCallerProviderForScout(provider string) bool {
	switch provider {
	case "claude", "codex", "opencode":
		return true
	}
	return false
}

// isScoutProvider reports whether the given provider can execute scout runs.
// opencode is intentionally excluded until a dedicated read-only mode ships.
func isScoutProvider(provider string) bool {
	switch provider {
	case "claude", "codex":
		return true
	}
	return false
}

// scoutMCPEntryShape is the JSON structure of the MCP server config entry
// injected for the scout. Matches the Claude-style mcpServers entry format
// consumed by claude, codex, and opencode backends.
type scoutMCPEntryShape struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// injectScoutMCPServer merges a managed "multica-scout" MCP server entry into
// existing Claude-format MCP config JSON ({"mcpServers": {...}}).
//
// Rules:
//   - Managed key wins on collision: an existing "multica-scout" entry is
//     replaced by the daemon's authoritative version.
//   - Fail-soft on malformed existing config: if the existing JSON cannot be
//     parsed, the managed entry is still returned (the broken config is
//     dropped rather than propagated to the agent).
//   - executablePath must be an absolute path (os.Executable() result); the
//     caller is responsible for obtaining it before calling this function.
func injectScoutMCPServer(executablePath, taskID, nonce string, daemonPort int, existing json.RawMessage) json.RawMessage {
	entry := scoutMCPEntryShape{
		Type:    "stdio",
		Command: executablePath,
		Args:    []string{"scout", "serve"},
		Env: map[string]string{
			"MULTICA_DAEMON_PORT":   fmt.Sprintf("%d", daemonPort),
			"MULTICA_SCOUT_TASK_ID": taskID,
			"MULTICA_SCOUT_TOKEN":   nonce,
		},
	}

	// Parse the existing MCP config if present; ignore parse errors (fail-soft).
	var cfg struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &cfg)
	}
	if cfg.McpServers == nil {
		cfg.McpServers = make(map[string]json.RawMessage)
	}

	entryJSON, err := json.Marshal(entry)
	if err != nil {
		// Plain struct — should never fail.
		return existing
	}
	// Managed key wins: always overwrite an existing "multica-scout" entry.
	cfg.McpServers[scoutMCPServerKey] = entryJSON

	out, err := json.Marshal(cfg)
	if err != nil {
		return existing
	}
	return out
}
