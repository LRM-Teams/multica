package computer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

const doctorResidueMaxAge = 24 * time.Hour

// Diagnosis is a read-only evidence report for `computer doctor`. Nothing here
// is derived from aggregate Workspace/Runner/Agent health; connectivity reflects
// only the resident process + machine state. No secrets (tokens/credentials)
// are ever included.
type Diagnosis struct {
	IdentityState            string   `json:"identity_state"`
	ComputerID               string   `json:"computer_id,omitempty"`
	LegacyIdentityCandidates []string `json:"legacy_identity_candidates,omitempty"`
	Resident                 string   `json:"resident"` // running | starting | stopped
	WorkspaceConnections     int      `json:"workspace_connections"`
	Connected                bool     `json:"connected"`
	CanonicalHost            string   `json:"canonical_host"`
	Environment              string   `json:"environment,omitempty"`
	ServiceOrigin            string   `json:"service_origin,omitempty"`
	PackageSource            string   `json:"package_source,omitempty"`
	ResidentEnvironment      string   `json:"resident_environment,omitempty"`
	ResidentServiceOrigin    string   `json:"resident_service_origin,omitempty"`
	ResidentPackageSource    string   `json:"resident_package_source,omitempty"`
	ConfigurationDrift       bool     `json:"configuration_drift"`
	SelectedWorkspaceID      string   `json:"selected_workspace_id,omitempty"`
	SelectedWorkspaceSlug    string   `json:"selected_workspace_slug,omitempty"`
	SelectedConnectionActive bool     `json:"selected_connection_active,omitempty"`
	// WorkspaceDaemons is one entry per persisted WorkspaceDaemon on this machine.
	// Owned is true only when the currently running resident (identified by
	// its own live pid, from the health probe) is the one that persisted
	// this pid as its child. A pid that is Alive but not Owned is a
	// WorkspaceDaemon nothing on this machine currently controls.
	WorkspaceDaemons []WorkspaceDaemonOwnership `json:"workspaceDaemons,omitempty"`
	UnownedLive      []WorkspaceDaemonOwnership `json:"unownedLive,omitempty"`
	FixApplied       []string                   `json:"fix_applied,omitempty"`
}

// WorkspaceDaemonOwnership is one on-disk WorkspaceDaemon's live-pid evidence
// against the current resident's ownership record.
type WorkspaceDaemonOwnership struct {
	WorkspaceID string `json:"workspaceId"`
	PID         int    `json:"pid"`
	Alive       bool   `json:"alive"`
	Owned       bool   `json:"owned"`
}

// Diagnose gathers local Computer evidence without mutating any state.
func (l *Lifecycle) Diagnose() Diagnosis {
	v := l.view()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	health := v.probe(ctx, v.service)

	d := Diagnosis{
		Resident:      "stopped",
		CanonicalHost: cli.OfficialCloudAPIHost,
	}
	if session, ok := readSessionProjection(); ok {
		d.Environment = session.Environment
		d.ServiceOrigin = session.Origin
		d.PackageSource = packageSourceForReleaseChannel(session.ReleaseChannel)
	}

	switch health["status"] {
	case "running":
		d.Resident = "running"
		d.Connected, _ = health["connected"].(bool)
	case "starting":
		d.Resident = "starting"
	}
	if value, ok := health["environment"].(string); ok {
		d.ResidentEnvironment = value
	}
	if value, ok := health["serverUrl"].(string); ok {
		d.ResidentServiceOrigin = value
	}
	if value, ok := health["releaseChannel"].(string); ok {
		d.ResidentPackageSource = packageSourceForReleaseChannel(value)
	}
	if d.Resident != "stopped" {
		d.ConfigurationDrift = d.Environment != d.ResidentEnvironment ||
			d.ServiceOrigin != d.ResidentServiceOrigin ||
			d.PackageSource != d.ResidentPackageSource
	}

	// Identity + connections are strictly read-only projections (no minting).
	store := NewIdentityStore(RootDir(""))
	ident := store.Peek("")
	if id, ok := ident["computer_id"].(string); ok {
		d.ComputerID = id
	}
	if st, ok := ident["identity_state"].(string); ok {
		d.IdentityState = st
	}
	if candidates, ok := ident["legacy_identity_candidates"].([]string); ok {
		d.LegacyIdentityCandidates = append([]string(nil), candidates...)
	}

	bindings, err := NewBindingsStore(RootDir("")).AllActive()
	if err == nil {
		d.WorkspaceConnections = len(bindings)
	}

	residentPID, residentPIDKnown := healthPID(health)
	if states, err := listRunnerStates(RootDir("")); err == nil {
		for _, state := range states {
			alive, known := processAlive(state.RunnerPID)
			alive = known && alive
			owned := alive && d.Resident == "running" && residentPIDKnown && residentPID == state.OwnerPID
			ownership := WorkspaceDaemonOwnership{WorkspaceID: state.WorkspaceID, PID: state.RunnerPID, Alive: alive, Owned: owned}
			d.WorkspaceDaemons = append(d.WorkspaceDaemons, ownership)
			if alive && !owned {
				d.UnownedLive = append(d.UnownedLive, ownership)
			}
		}
	}
	return d
}

// healthPID extracts the resident's own pid from a health probe map. The
// value is a float64 after a real JSON round trip and may be an int in
// tests that build the map directly.
func healthPID(health map[string]any) (int, bool) {
	switch value := health["pid"].(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	case int64:
		return int(value), true
	default:
		return 0, false
	}
}

// Fix applies only provably safe stale-state cleanup and reports every
// mutation. It cannot create a Computer, switch users, create a Workspace connection,
// revoke authorization, or delete Agent data.
func (l *Lifecycle) Fix(d Diagnosis) Diagnosis {
	v := l.view()
	var applied []string
	// A stale PID file with a confirmed-stopped resident is dead weight.
	if d.Resident == "stopped" {
		if _, err := os.Stat(v.pidPath); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			h := v.probe(ctx, v.service)
			cancel()
			if !Alive(h) {
				if os.Remove(v.pidPath) == nil {
					applied = append(applied, fmt.Sprintf("removed stale %s", v.pidPath))
				}
			}
		}
	}
	now := time.Now()
	applied = append(applied, cleanupAbandonedBindingState(RootDir(""), now)...)
	applied = append(applied, cleanupExpiredUpgradeStaging(now)...)
	applied = append(applied, reclaimOrphanedRunners(RootDir(""))...)
	d.FixApplied = applied
	return d
}

// reclaimOrphanedRunners terminates WorkspaceDaemon processes whose owning
// Computer is gone, freeing the slot so the next `computer start` spawns a
// fresh process instead of finding the machine wedged.
//
// This is the one Fix step that signals a live process, so its fence matters:
// findReclaimableRunners refuses any slot whose recorded owner pid is still
// alive, which means a WorkspaceDaemon the running Computer supervises can
// never be a candidate here. Only a WorkspaceDaemon whose owning Computer is
// confirmed dead — the self-locking state that used to require a manual kill —
// is reclaimed.
func reclaimOrphanedRunners(root string) []string {
	reclaimable, err := findReclaimableRunners(root, nil)
	if err != nil || len(reclaimable) == 0 {
		return nil
	}
	options := runnerReclaimOptions{StateRoot: root, PollInterval: 200 * time.Millisecond, Grace: 2 * time.Second}
	if token, err := ReadControlToken(""); err == nil && strings.TrimSpace(token) != "" {
		options.Drain = func(ctx context.Context, endpoint string, identity WorkspaceDaemonIdentity) error {
			return RequestWorkspaceDaemonDrain(ctx, endpoint, token, identity)
		}
	}
	applied := make([]string, 0, len(reclaimable))
	for _, runner := range reclaimable {
		if err := reclaimRunnerProcess(runner, options); err != nil {
			applied = append(applied, fmt.Sprintf("could NOT terminate orphaned WorkspaceDaemon pid %d (workspace %s): %v", runner.PID, runner.WorkspaceID, err))
			continue
		}
		applied = append(applied, fmt.Sprintf("terminated orphaned WorkspaceDaemon pid %d (workspace %s)", runner.PID, runner.WorkspaceID))
	}
	return applied
}

func cleanupAbandonedBindingState(root string, now time.Time) []string {
	if root == "" {
		return nil
	}
	bindings, err := NewBindingsStore(root).All()
	if err != nil {
		return nil
	}
	attached := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		attached[binding.Environment+"\x00"+binding.WorkspaceID] = struct{}{}
	}

	childrenRoot := filepath.Join(root, "binding-children")
	environments, err := os.ReadDir(childrenRoot)
	if err != nil {
		return nil
	}
	var applied []string
	for _, environment := range environments {
		if !environment.IsDir() {
			continue
		}
		workspaces, err := os.ReadDir(filepath.Join(childrenRoot, environment.Name()))
		if err != nil {
			continue
		}
		for _, workspace := range workspaces {
			if !workspace.IsDir() {
				continue
			}
			if _, ok := attached[environment.Name()+"\x00"+workspace.Name()]; ok {
				continue
			}
			info, err := workspace.Info()
			if err != nil || now.Sub(info.ModTime()) <= doctorResidueMaxAge {
				continue
			}
			source := filepath.Join(childrenRoot, environment.Name(), workspace.Name())
			quarantineRoot := filepath.Join(root, ".quarantine")
			if err := os.MkdirAll(quarantineRoot, 0o700); err != nil {
				continue
			}
			name := now.UTC().Format("20060102T150405.000000000Z") + "-" + environment.Name() + "-" + workspace.Name()
			destination := filepath.Join(quarantineRoot, name)
			if os.Rename(source, destination) == nil {
				applied = append(applied, fmt.Sprintf("quarantined abandoned Binding state %s to %s", source, destination))
			}
		}
	}
	return applied
}

func cleanupExpiredUpgradeStaging(now time.Time) []string {
	root, err := cli.MachineStateRoot()
	if err != nil {
		return nil
	}
	stagingRoot := filepath.Join(root, "upgrade-staging")
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return nil
	}
	var applied []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= doctorResidueMaxAge {
			continue
		}
		path := filepath.Join(stagingRoot, entry.Name())
		if os.RemoveAll(path) == nil {
			applied = append(applied, fmt.Sprintf("removed expired upgrade staging %s", path))
		}
	}
	return applied
}
