package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

// legacyProfileSnapshot contains local legacy evidence captured before setup
// updates the machine-wide config. It is private to the CLI adapter because it
// includes a credential; only the secret-free verification result crosses
// into the Computer domain model.
type legacyProfileSnapshot struct {
	Source     string
	ComputerID string
	Config     cli.CLIConfig
}

var newLegacyAdoptionAPIClient = cli.NewAPIClient
var establishLegacyWorkspaceConnection = establishWorkspaceConnection

func captureLegacyComputerEvidence() []legacyProfileSnapshot {
	legacyRoot := filepath.Dir(computer.RootDir(""))
	var out []legacyProfileSnapshot
	appendSnapshot := func(source, idPath string, cfg cli.CLIConfig) {
		data, err := os.ReadFile(idPath)
		if err != nil {
			return
		}
		id := strings.TrimSpace(string(data))
		if _, err := uuid.Parse(id); err != nil {
			return
		}
		out = append(out, legacyProfileSnapshot{Source: source, ComputerID: id, Config: cfg})
	}

	defaultCfg, _ := cli.LoadCLIConfigForProfile("")
	appendSnapshot("default legacy config", filepath.Join(legacyRoot, "daemon.id"), defaultCfg)

	profilesRoot := filepath.Join(legacyRoot, "profiles")
	entries, err := os.ReadDir(profilesRoot)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cfg, _ := cli.LoadCLIConfigForProfile(entry.Name())
		appendSnapshot("profile "+entry.Name(), filepath.Join(profilesRoot, entry.Name(), "daemon.id"), cfg)
	}
	return out
}

func adoptVerifiedLegacyComputer(_ *cobra.Command, snapshots []legacyProfileSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	store := computer.NewIdentityStore(computer.RootDir(""))
	if store.Peek("")["identity_state"] == "stable" {
		return nil
	}

	current, err := cli.LoadCLIConfigForProfile("")
	if err != nil || strings.TrimSpace(current.Token) == "" {
		return fmt.Errorf("verify legacy Computer evidence: machine-wide Cloud session is missing")
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	currentUserID, err := verifiedUserID(ctx, newLegacyAdoptionAPIClient(current.ServerURL, "", current.Token))
	if err != nil {
		return fmt.Errorf("verify current Computer user: %w", err)
	}

	evidence := make([]computer.LegacyEvidence, 0, len(snapshots))
	for _, snapshot := range snapshots {
		item := computer.LegacyEvidence{
			Source:               snapshot.Source,
			OriginHost:           snapshot.Config.ServerURL,
			WorkspaceID:          snapshot.Config.WorkspaceID,
			ComputerIDCandidates: []string{snapshot.ComputerID},
		}
		// Never contact localhost or custom origins during Cloud migration.
		if cli.CanonicalizeOfficialCloudAPIURL(strings.TrimRight(strings.TrimSpace(snapshot.Config.ServerURL), "/")) != cli.OfficialCloudAPIURL ||
			strings.TrimSpace(snapshot.Config.Token) == "" {
			evidence = append(evidence, item)
			continue
		}
		legacyClient := newLegacyAdoptionAPIClient(cli.OfficialCloudAPIURL, snapshot.Config.WorkspaceID, snapshot.Config.Token)
		legacyUserID, userErr := verifiedUserID(ctx, legacyClient)
		if userErr == nil {
			item.SignedInUser = legacyUserID
			item.UserVerified = legacyUserID == currentUserID
		}
		if item.UserVerified {
			item.WorkspaceVerified, item.WorkspaceSlug = verifiedWorkspaceMembership(ctx, legacyClient, snapshot.Config.WorkspaceID)
		}
		if item.UserVerified && item.WorkspaceVerified {
			item.ComputerVerified = verifiedComputerRuntime(ctx, legacyClient, snapshot.Config.WorkspaceID, snapshot.ComputerID)
		}
		evidence = append(evidence, item)
	}

	plan := computer.PlanLegacyAdoption(currentUserID, evidence)
	for _, exclusion := range plan.Exclusions {
		fmt.Fprintf(os.Stderr, "Legacy migration: %s — %s. Evidence was retained.\n", exclusion.Source, exclusion.Reason)
	}
	if err := plan.ChoiceError(); err != nil {
		return err
	}
	if plan.ComputerID == "" {
		return nil
	}
	if _, err := store.Adopt(plan.ComputerID); err != nil {
		return err
	}
	for _, connection := range plan.Connections {
		var legacyConfig cli.CLIConfig
		for _, snapshot := range snapshots {
			if snapshot.Config.WorkspaceID == connection.WorkspaceID {
				legacyConfig = snapshot.Config
				break
			}
		}
		if err := establishLegacyWorkspaceConnection(legacyConfig, plan.ComputerID, connection.WorkspaceID, connection.WorkspaceSlug); err != nil {
			return fmt.Errorf("adopted Computer identity but could not restore Workspace %s connection: %w", connection.WorkspaceID, err)
		}
	}
	fmt.Fprintf(os.Stderr, "Adopted verified Computer %s with %d Workspace connection(s); legacy evidence was retained.\n", plan.ComputerID, len(plan.Connections))
	return nil
}

func verifiedUserID(ctx context.Context, client *cli.APIClient) (string, error) {
	var me struct {
		ID string `json:"id"`
	}
	if err := client.GetJSON(ctx, "/api/me", &me); err != nil {
		return "", err
	}
	if _, err := uuid.Parse(me.ID); err != nil {
		return "", fmt.Errorf("server returned an invalid user identity")
	}
	return me.ID, nil
}

func verifiedWorkspaceMembership(ctx context.Context, client *cli.APIClient, workspaceID string) (bool, string) {
	if _, err := uuid.Parse(strings.TrimSpace(workspaceID)); err != nil {
		return false, ""
	}
	var workspaces []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := client.GetJSON(ctx, "/api/workspaces", &workspaces); err != nil {
		return false, ""
	}
	for _, workspace := range workspaces {
		if workspace.ID == workspaceID {
			return true, workspace.Slug
		}
	}
	return false, ""
}

func verifiedComputerRuntime(ctx context.Context, client *cli.APIClient, workspaceID, computerID string) bool {
	var runtimes []struct {
		WorkspaceID string  `json:"workspace_id"`
		DaemonID    *string `json:"daemon_id"`
	}
	if err := client.GetJSON(ctx, "/api/runtimes", &runtimes); err != nil {
		return false
	}
	for _, runtime := range runtimes {
		if runtime.WorkspaceID == workspaceID && runtime.DaemonID != nil && strings.EqualFold(*runtime.DaemonID, computerID) {
			return true
		}
	}
	return false
}
