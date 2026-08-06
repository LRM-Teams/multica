package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/turntransport"
)

var errAgentRuntimeTurnBusy = errors.New("agent runtime turn already active")

// agentRuntimeTurnSlotKey is the provider-neutral logical owner of one
// canonical provider session. D4 provider pools may consume this key, but the
// turn coordinator deliberately owns no provider backend or fingerprint.
type agentRuntimeTurnSlotKey struct {
	AgentID   string
	RuntimeID string
}

// agentRuntimeTurnRequest contains the D1-D3 inputs needed before a D4
// provider adapter may execute one turn. It is dormant until D6 routes live
// claims through agentRuntimeTurnCoordinator.Begin.
type agentRuntimeTurnRequest struct {
	WorkspaceID            string
	AgentID                string
	RuntimeID              string
	TurnID                 string
	PriorSessionID         string
	RuntimeStateGeneration int64
	MulticaBinary          string
	Token                  string
	Environment            map[string]string
}

// agentRuntimeTurn is the prepared, single-owner handoff from D1-D3 into the
// D4 provider pool. It contains no raw token. Close is idempotent and fences
// current-turn authority by transport generation.
type agentRuntimeTurn struct {
	SlotKey                agentRuntimeTurnSlotKey
	AgentID                string
	RuntimeID              string
	PriorSessionID         string
	RuntimeStateGeneration int64
	WorkDir                string
	Workspace              execenv.AgentWorkspaceLayout
	StableEnvironment      map[string]string
	WrapperPath            string

	coordinator *agentRuntimeTurnCoordinator
	binding     turntransport.Binding
	closeOnce   sync.Once
	closeErr    error
}

// agentRuntimeTurnCoordinator is a daemon-local safety fence around the
// server's authoritative D5 serialization gate. It prevents two local turns
// for one agent×runtime slot from replacing each other's current-turn
// envelope even if a stale claim is still unwinding.
type agentRuntimeTurnCoordinator struct {
	cfg    Config
	logger *slog.Logger

	mu     sync.Mutex
	active map[agentRuntimeTurnSlotKey]string
}

func newAgentRuntimeTurnCoordinator(cfg Config, logger *slog.Logger) *agentRuntimeTurnCoordinator {
	return &agentRuntimeTurnCoordinator{
		cfg:    cfg,
		logger: logger,
		active: make(map[agentRuntimeTurnSlotKey]string),
	}
}

func (c *agentRuntimeTurnCoordinator) Begin(request agentRuntimeTurnRequest) (*agentRuntimeTurn, error) {
	if c == nil {
		return nil, errors.New("agent runtime turn coordinator is not configured")
	}
	if err := validateAgentRuntimeTurnRequest(request); err != nil {
		return nil, err
	}

	// Token is request.Token → Bind; strip legacy raw credential keys from the
	// process-identity map so D3 SplitEnvironment does not fail-close production
	// agentEnv that still carries MULTICA_TOKEN_FILE for the CLI wrapper path.
	env := stripProviderCredentialTransport(request.Environment)
	stableEnvironment, currentTurnEnvironment, err := splitAgentProcessEnvironment(env)
	if err != nil {
		return nil, fmt.Errorf("split agent runtime environment: %w", err)
	}
	if stableEnvironment["MULTICA_WORKSPACE_ID"] != request.WorkspaceID {
		return nil, errors.New("stable MULTICA_WORKSPACE_ID does not match turn workspace")
	}
	if stableEnvironment["MULTICA_AGENT_ID"] != request.AgentID {
		return nil, errors.New("stable MULTICA_AGENT_ID does not match turn agent")
	}
	if currentTurnEnvironment["MULTICA_TASK_ID"] != request.TurnID {
		return nil, errors.New("current-turn MULTICA_TASK_ID does not match turn")
	}

	key := agentRuntimeTurnSlotKey{
		AgentID:   request.AgentID,
		RuntimeID: request.RuntimeID,
	}
	if !c.reserve(key, request.TurnID) {
		return nil, fmt.Errorf("%w: agent_id=%s runtime_id=%s", errAgentRuntimeTurnBusy, key.AgentID, key.RuntimeID)
	}
	releaseReservation := true
	defer func() {
		if releaseReservation {
			c.release(key, request.TurnID)
		}
	}()

	workspace, err := execenv.ProvisionAgentWorkspace(
		c.cfg.WorkspacesRoot,
		request.WorkspaceID,
		request.AgentID,
		c.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("provision canonical agent workspace: %w", err)
	}
	turnWorkDir := workspace.AgentRoot

	transport, err := prepareStableAgentCLITransport(
		c.cfg,
		request.WorkspaceID,
		request.AgentID,
		request.MulticaBinary,
	)
	if err != nil {
		return nil, fmt.Errorf("prepare stable agent CLI transport: %w", err)
	}
	stableEnvironment["PATH"] = prependPath(filepath.Dir(transport.WrapperPath()), stableEnvironment["PATH"])

	binding, err := transport.Bind(request.TurnID, request.Token, currentTurnEnvironment)
	if err != nil {
		return nil, fmt.Errorf("bind current agent runtime turn: %w", err)
	}

	releaseReservation = false
	return &agentRuntimeTurn{
		SlotKey:                key,
		AgentID:                request.AgentID,
		RuntimeID:              request.RuntimeID,
		PriorSessionID:         request.PriorSessionID,
		RuntimeStateGeneration: request.RuntimeStateGeneration,
		WorkDir:                turnWorkDir,
		Workspace:              *workspace,
		StableEnvironment:      cloneEnvironment(stableEnvironment),
		WrapperPath:            transport.WrapperPath(),
		coordinator:            c,
		binding:                binding,
	}, nil
}

func (t *agentRuntimeTurn) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		var closeErrors []error
		if _, err := turntransport.Unbind(t.binding); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("unbind current agent runtime turn: %w", err))
		}
		if t.coordinator != nil {
			t.coordinator.release(t.SlotKey, t.binding.TurnID)
		}
		t.closeErr = errors.Join(closeErrors...)
	})
	return t.closeErr
}

func (c *agentRuntimeTurnCoordinator) reserve(key agentRuntimeTurnSlotKey, turnID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.active[key]; exists {
		return false
	}
	c.active[key] = turnID
	return true
}

func (c *agentRuntimeTurnCoordinator) release(key agentRuntimeTurnSlotKey, turnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[key] == turnID {
		delete(c.active, key)
	}
}

func (c *agentRuntimeTurnCoordinator) hasActiveTurn(agentID, runtimeID string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.active[agentRuntimeTurnSlotKey{
		AgentID:   strings.TrimSpace(agentID),
		RuntimeID: strings.TrimSpace(runtimeID),
	}]
	return ok
}

func (c *agentRuntimeTurnCoordinator) hasActiveAgentTurn(agentID, turnID string) bool {
	_, ok := c.activeAgentTurnRuntime(agentID, turnID)
	return ok
}

// activeAgentTurnRuntime resolves the machine-owned runtime for a current
// Agent turn. Local Credential Proxy callers must not accept a runtime ID from
// the Agent process when selecting its credential cache.
func (c *agentRuntimeTurnCoordinator) activeAgentTurnRuntime(agentID, turnID string) (string, bool) {
	if c == nil {
		return "", false
	}
	agentID = strings.TrimSpace(agentID)
	turnID = strings.TrimSpace(turnID)
	if agentID == "" || turnID == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, activeTurnID := range c.active {
		if key.AgentID == agentID && activeTurnID == turnID {
			return key.RuntimeID, true
		}
	}
	return "", false
}

func validateAgentRuntimeTurnRequest(request agentRuntimeTurnRequest) error {
	for name, value := range map[string]string{
		"workspace_id": request.WorkspaceID,
		"agent_id":     request.AgentID,
		"runtime_id":   request.RuntimeID,
		"turn_id":      request.TurnID,
	} {
		if !isCanonicalRuntimeUUID(value) {
			return fmt.Errorf("%s must be a canonical full UUID", name)
		}
	}
	if request.RuntimeStateGeneration <= 0 {
		return errors.New("runtime_state_generation must be positive")
	}
	if strings.TrimSpace(request.MulticaBinary) == "" || !filepath.IsAbs(request.MulticaBinary) {
		return errors.New("multica binary path must be absolute")
	}
	if request.Token == "" {
		return errors.New("turn token is required")
	}
	return nil
}

func isCanonicalRuntimeUUID(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || value != strings.ToLower(trimmed) {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func prependPath(dir, existing string) string {
	if existing == "" {
		existing = os.Getenv("PATH")
	}
	dir = filepath.Clean(dir)
	parts := filepath.SplitList(existing)
	filtered := make([]string, 0, len(parts)+1)
	filtered = append(filtered, dir)
	for _, part := range parts {
		if part == "" || filepath.Clean(part) == dir {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, string(os.PathListSeparator))
}

func cloneEnvironment(environment map[string]string) map[string]string {
	cloned := make(map[string]string, len(environment))
	for key, value := range environment {
		cloned[key] = value
	}
	return cloned
}
