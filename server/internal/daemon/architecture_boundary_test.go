package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDaemonProductionOwnsNoComputerLifecycle(t *testing.T) {
	hostType := regexp.MustCompile(`\*computer\.Host(?:[^A-Za-z0-9_]|$)`)
	forbiddenFiles := map[string]struct{}{
		"machine_upgrade.go":          {},
		"machine_upgrade_log.go":      {},
		"machine_upgrade_recovery.go": {},
		"machine_upgrade_takeover.go": {},
		"release_detection.go":        {},
		"stage_update.go":             {},
		"update_observation.go":       {},
	}
	forbiddenOwners := []string{
		"func (d *Daemon) Run(",
		"handleMachineUpgrade(",
		"machineUpgradeJournal",
		"MachineUpgradeTakeover",
		"CreateMachineUpgrade(",
		"AcceptMachineUpgrade(",
		"ReportMachineUpgradeProgress(",
		"handleUpdate(",
		"triggerRestart(",
		"runStageUpdate(",
		"releaseDetectionLoop(",
		"updateObservationCoordinator",
		"listenHealth(",
		"healthHandler(",
		"machineAttestationHandler(",
		"SetHumanToken(",
		"HumanToken(",
		"ClearHumanToken(",
		"humanToken",
		"func (c *Client) RenewToken(",
		"func (c *Client) ListWorkspaces(",
		"func (d *Daemon) resolveAuth(",
		"func (d *Daemon) preflightAuth(",
		"func (d *Daemon) tokenRenewalLoop(",
		"func (d *Daemon) tryRenewToken(",
	}

	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if _, forbidden := forbiddenFiles[filepath.Base(path)]; forbidden {
			t.Errorf("%s restores a Computer lifecycle owner inside internal/daemon", path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, owner := range forbiddenOwners {
			if strings.Contains(string(body), owner) {
				t.Errorf("%s owns Computer lifecycle symbol %q", path, owner)
			}
		}
		if hostType.Match(body) || strings.Contains(string(body), "computer.NewHost(") || strings.Contains(string(body), "daemonProcessComputerHost") {
			t.Errorf("%s mixes the Computer Host into daemon execution", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
