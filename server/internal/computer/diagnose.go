package computer

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Diagnosis is a read-only evidence report for `computer doctor`. Nothing here
// is derived from aggregate Binding/Runner/Agent health; connectivity reflects
// only the resident process + machine state. No secrets (tokens/credentials)
// are ever included.
type Diagnosis struct {
	IdentityState string   `json:"identity_state"`
	ComputerID    string   `json:"computer_id,omitempty"`
	Resident      string   `json:"resident"` // running | starting | stopped
	Bindings      int      `json:"bindings"`
	Connected     bool     `json:"connected"`
	CanonicalHost string   `json:"canonical_host"`
	FixApplied    []string `json:"fix_applied,omitempty"`
}

// Diagnose gathers local Computer evidence without mutating any state.
func (l *Lifecycle) Diagnose() Diagnosis {
	v := l.view()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	health := v.probe(ctx, v.health)

	d := Diagnosis{
		Resident:      "stopped",
		CanonicalHost: cli.OfficialCloudAPIHost,
	}

	switch health["status"] {
	case "running":
		d.Resident = "running"
		d.Connected = true
	case "starting":
		d.Resident = "starting"
	}

	// Identity + bindings are strictly read-only projections (no minting).
	store := NewIdentityStore(RootDir(""))
	ident := store.Peek(l.Profile)
	if id, ok := ident["computer_id"].(string); ok {
		d.ComputerID = id
	}
	if st, ok := ident["identity_state"].(string); ok {
		d.IdentityState = st
	}

	bindings, err := NewBindingsStore(RootDir("")).AllActive()
	if err == nil {
		d.Bindings = len(bindings)
	}
	return d
}

// Fix applies only provably safe stale-state cleanup and reports every
// mutation. It cannot create a Computer, switch users, create a Binding,
// revoke authorization, or delete Agent data.
func (l *Lifecycle) Fix(d Diagnosis) Diagnosis {
	v := l.view()
	var applied []string
	// Stale PID file with a confirmed-stopped resident is the only provably
	// safe mutation: the PID is not running, so the file is dead weight.
	if d.Resident == "stopped" {
		if _, err := os.Stat(v.pidPath); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			h := v.probe(ctx, v.health)
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
