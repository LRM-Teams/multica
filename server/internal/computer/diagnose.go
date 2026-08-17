package computer

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

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
	FixApplied               []string `json:"fix_applied,omitempty"`
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
	if value, ok := health["server_url"].(string); ok {
		d.ResidentServiceOrigin = value
	}
	if value, ok := health["release_channel"].(string); ok {
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
	return d
}

// Fix applies only provably safe stale-state cleanup and reports every
// mutation. It cannot create a Computer, switch users, create a Workspace connection,
// revoke authorization, or delete Agent data.
func (l *Lifecycle) Fix(d Diagnosis) Diagnosis {
	v := l.view()
	var applied []string
	// Stale PID file with a confirmed-stopped resident is the only provably
	// safe mutation: the PID is not running, so the file is dead weight.
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
	d.FixApplied = applied
	return d
}
