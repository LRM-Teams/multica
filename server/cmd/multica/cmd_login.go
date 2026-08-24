package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// tryResolveAppURL returns the app URL if configured, or "" if not available.
// Unlike resolveAppURL, it never calls os.Exit.
func tryResolveAppURL(cmd *cobra.Command) string {
	if val := configuredAppURL(cmd); val != "" {
		return val
	}
	for _, key := range []string{"MULTICA_APP_URL", "FRONTEND_ORIGIN"} {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			return strings.TrimRight(val, "/")
		}
	}
	return ""
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate and configure one workspace",
	Long:  "Log in to Multica, then configure the workspace selected by multica setup /<workspace>.",
	// Up to one positional is accepted so `--token mul_...` / `--token mcn_...`
	// (space form) can recover the token in runAuthLogin even though pflag
	// won't bind it.
	Args: cobra.MaximumNArgs(1),
	RunE: runLogin,
}

// tokenPromptSentinel is the value pflag assigns to `--token` when the flag
// is supplied without an explicit value. runAuthLoginToken treats it as
// "prompt me interactively", preserving the legacy `multica login --token`
// no-value form alongside the documented `--token mul_...` / `--token mcn_...`
// value form.
const tokenPromptSentinel = "\x00prompt"

func init() {
	loginCmd.Flags().String("token", "", "Authenticate using a personal access token (`mul_...` user PAT or `mcn_...` Cloud Node PAT). Pass `--token mul_...` / `--token mcn_...` to supply it inline, or `--token` alone to be prompted interactively.")
	// NoOptDefVal lets `--token` (no value) keep its old prompt-mode behavior
	// while `--token mul_...` / `--token mcn_...` and the `=value` form
	// consume the value normally.
	loginCmd.Flags().Lookup("token").NoOptDefVal = tokenPromptSentinel
	loginCmd.Flags().String(callbackHostFlag, "", "ComputerCore the OAuth callback URL points at (auto-detected from the server's route when empty). Use this for Windows WSL / reverse-proxy / FQDN setups where auto-detection picks the wrong interface.")
	loginCmd.Flags().String("workspace", "", "Set the default workspace by id or slug after login, instead of auto-picking the first one (env: MULTICA_WORKSPACE).")
}

func runLogin(cmd *cobra.Command, args []string) error {
	// Run the standard auth login flow.
	if err := runAuthLogin(cmd, args); err != nil {
		return err
	}

	// Resolve and persist exactly the workspace requested by setup. The legacy
	// path discovered every membership and made the connected computer appear in
	// unrelated workspaces.
	if err := configureSelectedWorkspace(cmd); err != nil {
		fmt.Fprintf(os.Stderr, "\nCould not configure the selected workspace: %v\n", err)
		fmt.Fprintf(os.Stderr, "Run 'multica workspace list' and re-run 'multica setup /<workspace-slug>'.\n")
		return err
	}

	fmt.Fprintln(os.Stderr, "\nWorkspace selected. Setup will ensure the machine-wide Computer is running.")
	return nil
}

func configureSelectedWorkspace(cmd *cobra.Command) error {
	serverURL := resolveServerURL(cmd)
	token := resolveToken(cmd)
	if token == "" {
		return fmt.Errorf("not authenticated")
	}

	client := cli.NewAPIClient(serverURL, "", token)
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var workspaces []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := client.GetJSON(ctx, "/api/workspaces", &workspaces); err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		var err error
		workspaces, err = waitForWorkspaceCreation(cmd, client)
		if err != nil {
			return err
		}
		if len(workspaces) == 0 {
			fmt.Fprintln(os.Stderr, "\nNo workspaces found.")
			return nil
		}
	}

	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}

	want := strings.TrimSpace(cli.FlagOrEnv(cmd, "workspace", "MULTICA_WORKSPACE", ""))
	if want == "" {
		return fmt.Errorf("workspace is required; use `multica setup /<workspace-slug>`")
	}
	var selected *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	for i := range workspaces {
		if workspaces[i].ID == want || workspaces[i].Slug == want {
			selected = &workspaces[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("workspace %q not found among your workspaces — run `multica workspace list` to see available ids/slugs", want)
	}
	cfg.WorkspaceID = selected.ID

	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nConfigured workspace: %s (%s)\n", selected.Name, selected.ID)

	return nil
}

// waitForWorkspaceCreation opens the web workspace-creation page and polls
// until the user creates a workspace, returning the new workspace list.
func waitForWorkspaceCreation(cmd *cobra.Command, client *cli.APIClient) ([]struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}, error) {
	appURL := tryResolveAppURL(cmd)
	if appURL == "" {
		// No app URL available (e.g. token login without prior setup).
		// Can't open the browser — tell the user to create a workspace manually.
		fmt.Fprintln(os.Stderr, "\nNo workspaces found.")
		fmt.Fprintln(os.Stderr, "Create a workspace in the web dashboard, then run 'multica login' again.")
		return nil, nil
	}

	createWorkspaceURL := appURL + "/workspaces/new"

	fmt.Fprintln(os.Stderr, "\nNo workspaces found. Opening workspace creation in your browser...")
	if err := openBrowser(createWorkspaceURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser automatically.\n")
	}
	fmt.Fprintf(os.Stderr, "If the browser didn't open, visit:\n  %s\n", createWorkspaceURL)
	fmt.Fprintln(os.Stderr, "\nWaiting for workspace creation...")

	// Poll until a workspace appears or timeout (5 minutes).
	const pollInterval = 2 * time.Second
	const pollTimeout = 5 * time.Minute
	deadline := time.Now().Add(pollTimeout)

	// Per-poll request budget. We keep a short 10s floor so the loop stays
	// responsive (a hung request shouldn't block a single iteration for long),
	// but it still honors MULTICA_HTTP_TIMEOUT via AtLeastAPITimeout so a user
	// who raised the timeout for a slow network isn't capped below it. The
	// overall wait is bounded by pollTimeout regardless.
	pollRequestTimeout := cli.AtLeastAPITimeout(10 * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		ctx, cancel := context.WithTimeout(context.Background(), pollRequestTimeout)
		var workspaces []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		err := client.GetJSON(ctx, "/api/workspaces", &workspaces)
		cancel()

		if err != nil {
			continue // transient error, keep polling
		}
		if len(workspaces) > 0 {
			return workspaces, nil
		}
	}

	return nil, fmt.Errorf("timed out waiting for workspace creation")
}
